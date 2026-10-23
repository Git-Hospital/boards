// Package runner takes one case through its twins against one tool
// (architecture §1): quiesce, inject, wait for the oracle, run the tool,
// apply its patch, reset, and record what happened for the scorer.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Git-Hospital/boards/internal/adapter"
	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/score"
	"github.com/Git-Hospital/boards/internal/testbed"
	"github.com/Git-Hospital/boards/internal/verdict"
)

// Config tunes the waits. Dry runs set them to zero.
type Config struct {
	// Quiesce is the pause before each twin, so the previous one's signals
	// decay out of the rate windows. Learned the hard way: a stale UO tail
	// from pool-overflow leaked into the next run of the old harness.
	Quiesce time.Duration
	// LiveTimeout is how long to wait for the oracle to see the defect.
	LiveTimeout time.Duration
	// Poll is how often oracles are evaluated.
	Poll time.Duration
	// SkipSettle skips each case's settle time, for the dry-run testbed.
	SkipSettle bool
	// Work is the directory each run's verdict and raw output go into.
	Work string
	// Log receives progress lines. Nil discards them.
	Log func(format string, a ...any)
}

// Runner runs cases against one tool on one testbed.
type Runner struct {
	cfg  Config
	tb   testbed.Testbed
	tool *adapter.Adapter
	now  func() time.Time
}

// New returns a runner.
func New(cfg Config, tb testbed.Testbed, tool *adapter.Adapter) *Runner {
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	if cfg.Poll == 0 {
		cfg.Poll = 10 * time.Second
	}
	return &Runner{cfg: cfg, tb: tb, tool: tool, now: time.Now}
}

// Case runs every twin of c and scores it.
func (r *Runner) Case(ctx context.Context, c *casefile.Case) score.Case {
	var runs []score.Run
	for _, twin := range c.Twins() {
		run := r.twin(ctx, c, twin)
		runs = append(runs, run)
		if run.Inconclusive != "" {
			break
		}
	}
	return score.Score(c, r.tool, runs)
}

func (r *Runner) twin(ctx context.Context, c *casefile.Case, twin casefile.Twin) (run score.Run) {
	run.Twin = twin
	r.cfg.Log("%s [%s]", c.ID, twin)

	work := filepath.Join(r.cfg.Work, strings.ReplaceAll(c.ID, "/", "_"), string(twin))
	if err := os.MkdirAll(work, 0o755); err != nil {
		run.Inconclusive = DirtyTestbed
		return run
	}

	if err := r.tb.Quiesce(ctx, r.cfg.Quiesce); err != nil {
		run.Inconclusive = interrupted(ctx, DirtyTestbed)
		return run
	}

	// Cleanup must run even after Ctrl-C, so it never uses the run's ctx: a
	// cancelled ctx would fail the reset at once and leave the defect, or an
	// applied patch, in the cluster for the next run.
	cleanup := context.WithoutCancel(ctx)
	injected := false
	defer func() {
		if !injected {
			return
		}
		if run.DefectEndedAt.IsZero() {
			run.DefectEndedAt = r.now()
		}
		if err := r.tb.Reset(cleanup, c, twin); err != nil {
			r.cfg.Log("  reset failed: %v", err)
			if run.Inconclusive == "" {
				run.Inconclusive = DirtyReset
			}
		}
	}()

	watcher := r.tool.Mode == "watcher" && c.Track != casefile.TrackCode
	inv := adapter.Invocation{Case: c, Twin: twin, Work: work, Window: c.Window.Duration, Env: r.tb.Env()}

	// A watcher is started first and given its warm-up on the healthy
	// testbed, as it would run in production. A one-shot tool is called
	// once the defect is live.
	var toolDone <-chan toolResult
	stopTool := func() {}
	if watcher {
		toolCtx, cancel := context.WithCancel(ctx)
		stopTool = cancel
		toolDone = r.start(toolCtx, inv)
		defer cancel()
		if err := sleep(ctx, r.tool.Warmup.Duration); err != nil {
			run.Inconclusive = "interrupted"
			return run
		}
	}

	injected = true
	if err := r.tb.Inject(ctx, c, twin); err != nil {
		r.cfg.Log("  inject failed: %v", err)
		run.Inconclusive = interrupted(ctx, DirtyTestbed)
		stopTool()
		return run
	}
	run.InjectedAt = r.now()

	// Settle lets the rate windows fill before the detection clock starts
	// (the meaning meshmedic-prove gave it).
	settle := c.Settle.Duration
	if r.cfg.SkipSettle {
		settle = 0
	}
	if err := sleep(ctx, settle); err != nil {
		run.Inconclusive = "interrupted"
		stopTool()
		return run
	}
	if why := r.waitReady(ctx, c, twin); why != "" {
		run.Inconclusive = why
		stopTool()
		return run
	}
	run.DetectFrom = r.now()

	before, beforeErr := r.tb.Inventory(ctx)
	var res toolResult
	if watcher {
		if err := sleepUntil(ctx, run.DetectFrom.Add(c.Window.Duration), r.now); err != nil {
			run.Inconclusive = "interrupted"
			stopTool()
			return run
		}
	} else {
		inv.Budget = c.Window.Duration
		res = <-r.start(ctx, inv)
		if ctx.Err() != nil {
			// The user stopped boards; the tool did not time out.
			run.Inconclusive = "interrupted"
			return run
		}
	}
	after, afterErr := r.tb.Inventory(ctx)
	if err := errors.Join(beforeErr, afterErr); err != nil {
		run.Cost.InventoryError = err.Error()
	} else {
		run.Cost.ObjectsCreated, run.Cost.ObjectsDeleted = diff(before, after)
	}

	// The patch is read from the verdict as it stands at the end of the
	// window; a watcher keeps its verdict.json current while it runs. The
	// score is about this patch, whatever the verdict says later.
	if v, err := verdict.Read(filepath.Join(work, verdict.FileName)); err == nil {
		run.Patch = v.Patch
	}
	if p := run.Patch; p != nil && twin == casefile.TwinDefect && c.Expect.Remediation != nil &&
		r.tool.ProposesFixes && !slices.Contains(c.Expect.Remediation.Forbid, p.Class) {
		run.Remedy = r.remedy(ctx, cleanup, c, p)
		if !run.Remedy.Removed {
			// Some of the tool's patch may still be in the cluster, so
			// nothing that runs after this case can be trusted. The case is
			// still scored: an irreversible patch is the tool's failure.
			run.Dirty = DirtyPatch
		}
	}

	run.DefectEndedAt = r.now()
	injected = false
	if err := r.tb.Reset(cleanup, c, twin); err != nil {
		r.cfg.Log("  reset failed: %v", err)
		run.Inconclusive = DirtyReset
	}

	if watcher {
		// Let the tool see the recovery before stopping it.
		_ = sleep(ctx, c.Expect.ResolvesWithin.Duration)
		stopTool()
		res = <-toolDone
	}
	if ctx.Err() != nil && run.Inconclusive == "" {
		run.Inconclusive = "interrupted"
	}

	run.ExitCode, run.TimedOut = res.out.ExitCode, res.out.TimedOut
	run.Cost.WallSeconds = res.out.Elapsed.Seconds()
	if res.err != nil {
		run.VerdictErr = res.err.Error()
		return run
	}
	v, err := verdict.Read(filepath.Join(work, verdict.FileName))
	if err != nil {
		run.VerdictErr = err.Error()
		return run
	}
	run.Verdict = v
	run.Cost.Tokens = v.Tokens
	return run
}

// interrupted returns "interrupted" when the user stopped boards, so a
// cancelled step is not reported as a broken testbed.
func interrupted(ctx context.Context, otherwise string) string {
	if ctx.Err() != nil {
		return "interrupted"
	}
	return otherwise
}

type toolResult struct {
	out adapter.Outcome
	err error
}

func (r *Runner) start(ctx context.Context, inv adapter.Invocation) <-chan toolResult {
	ch := make(chan toolResult, 1)
	go func() {
		out, err := r.tool.Run(ctx, inv)
		ch <- toolResult{out, err}
	}()
	return ch
}

// waitReady waits until the twin is in the state the tool must face: the
// defect live, the clean twin free of it, the control's noise in place.
func (r *Runner) waitReady(ctx context.Context, c *casefile.Case, twin casefile.Twin) string {
	if c.Track == casefile.TrackCode {
		return ""
	}
	deadline := r.now().Add(r.cfg.LiveTimeout)
	for {
		st, err := r.tb.Defect(ctx, c)
		if err == nil {
			switch twin {
			case casefile.TwinDefect, casefile.TwinControl:
				if st == testbed.Holds {
					return ""
				}
			case casefile.TwinClean:
				// An empty ratio on a healthy service reads as no data; the
				// clean twin only needs the defect's condition not to hold.
				if st != testbed.Holds {
					return ""
				}
			}
		}
		if !r.now().Before(deadline) {
			if twin == casefile.TwinClean {
				return "clean_twin_not_clean"
			}
			return "oracle_never_live"
		}
		if err := sleep(ctx, r.cfg.Poll); err != nil {
			return "interrupted"
		}
	}
}

// remedy applies a patch and checks that it heals (D12). Healed needs the
// defect's condition to stop holding AND every clean-twin check to hold with
// traffic present: an oracle alone cannot tell a healed service from a
// deleted one, since both stop reporting errors.
func (r *Runner) remedy(ctx, cleanup context.Context, c *casefile.Case, p *verdict.Patch) *score.Remedy {
	m := &score.Remedy{}
	if err := r.tb.ApplyPatch(ctx, c, p); err != nil {
		m.Detail = err.Error()
		// A patch can fail halfway, or be interrupted, and leave objects
		// behind; the reset only reverts the defect.
		if err := r.tb.RemovePatch(cleanup, c, p); err == nil {
			m.Removed = true
		}
		return m
	}
	m.Applied, m.AppliedAt = true, r.now()
	// The patch must reach, within healsWithin, a state where the defect's
	// condition is gone and every clean-twin check holds. The checks lag
	// the defect by their rate window, so both are polled until then.
	// Healed with CleanOK false at the deadline is a patch that removed the
	// symptom by breaking the service (D12).
	deadline := r.now().Add(c.Expect.Remediation.HealsWithin.Duration)
	for {
		st, err := r.tb.Defect(ctx, c)
		m.Healed = err == nil && st != testbed.Holds
		m.CleanOK = false
		if m.Healed {
			if m.HealedAt.IsZero() {
				m.HealedAt = r.now()
			}
			m.CleanOK, m.Detail = true, ""
			for i, o := range c.CleanTwin.Checks {
				st, err := r.tb.Check(ctx, c, o)
				if err != nil || st != testbed.Holds {
					m.CleanOK = false
					m.Detail = fmt.Sprintf("clean-twin check %d: %s", i, describe(st, err))
					break
				}
			}
			if m.CleanOK {
				break
			}
		}
		if !r.now().Before(deadline) || sleep(ctx, r.cfg.Poll) != nil {
			break
		}
	}
	if err := r.tb.RemovePatch(cleanup, c, p); err != nil {
		m.Detail = strings.TrimSpace(m.Detail + "; remove: " + err.Error())
	} else {
		m.Removed = true
	}
	return m
}

func describe(st testbed.State, err error) string {
	if err != nil {
		return err.Error()
	}
	return string(st)
}

func diff(before, after []string) (created, deleted []string) {
	b := map[string]bool{}
	for _, o := range before {
		b[o] = true
	}
	a := map[string]bool{}
	for _, o := range after {
		a[o] = true
		if !b[o] {
			created = append(created, o)
		}
	}
	for _, o := range before {
		if !a[o] {
			deleted = append(deleted, o)
		}
	}
	return created, deleted
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func sleepUntil(ctx context.Context, at time.Time, now func() time.Time) error {
	return sleep(ctx, at.Sub(now()))
}

// Reasons a twin leaves the testbed in an unknown state. The run must stop:
// every later case would run on a cluster that is not the testbed.
const (
	DirtyReset   = "reset_failed"
	DirtyPatch   = "patch_not_removed"
	DirtyTestbed = "testbed_failed"
)

// Dirty reports whether a case left the testbed unusable.
func Dirty(c score.Case) bool {
	for _, r := range c.Runs {
		if r.Dirty != "" {
			return true
		}
		switch r.Inconclusive {
		case DirtyReset, DirtyTestbed:
			return true
		}
	}
	return false
}

// ErrNoCases is returned when a selection is empty.
var ErrNoCases = errors.New("no cases selected")
