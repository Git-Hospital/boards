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
		run.Inconclusive = "testbed_failed"
		return run
	}

	if err := r.tb.Quiesce(ctx, r.cfg.Quiesce); err != nil {
		run.Inconclusive = "testbed_failed"
		return run
	}

	// Reset runs on every exit path, including a failed inject, so a broken
	// twin never leaves its defect behind for the next case.
	injected := false
	defer func() {
		if !injected {
			return
		}
		if err := r.tb.Reset(context.WithoutCancel(ctx), c, twin); err != nil {
			r.cfg.Log("  reset failed: %v", err)
			if run.Inconclusive == "" {
				run.Inconclusive = "reset_failed"
			}
		}
		if run.DefectEndedAt.IsZero() {
			run.DefectEndedAt = r.now()
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
		run.Inconclusive = "testbed_failed"
		stopTool()
		return run
	}
	run.InjectedAt = r.now()

	if why := r.waitReady(ctx, c, twin); why != "" {
		run.Inconclusive = why
		stopTool()
		return run
	}

	before, _ := r.tb.Inventory(ctx)
	var res toolResult
	if watcher {
		if err := sleepUntil(ctx, run.InjectedAt.Add(c.Window.Duration), r.now); err != nil {
			run.Inconclusive = "interrupted"
			stopTool()
			return run
		}
	} else {
		inv.Budget = c.Window.Duration
		res = <-r.start(ctx, inv)
	}
	after, _ := r.tb.Inventory(ctx)
	run.Cost.ObjectsCreated, run.Cost.ObjectsDeleted = diff(before, after)

	// The patch is read from the verdict as it stands at the end of the
	// window; a watcher keeps its verdict.json current while it runs.
	if twin == casefile.TwinDefect && c.Expect.Remediation != nil && r.tool.ProposesFixes {
		if v, err := verdict.Read(filepath.Join(work, verdict.FileName)); err == nil && v.Patch != nil &&
			!slices.Contains(c.Expect.Remediation.Forbid, v.Patch.Class) {
			run.Remedy = r.remedy(ctx, c, v.Patch)
		}
	}

	if err := r.tb.Reset(ctx, c, twin); err != nil {
		r.cfg.Log("  reset failed: %v", err)
		run.Inconclusive = "reset_failed"
	}
	injected = false
	run.DefectEndedAt = r.now()

	if watcher {
		// Let the tool see the recovery before stopping it.
		_ = sleep(ctx, c.Expect.ResolvesWithin.Duration)
		stopTool()
		res = <-toolDone
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
func (r *Runner) remedy(ctx context.Context, c *casefile.Case, p *verdict.Patch) *score.Remedy {
	m := &score.Remedy{}
	if err := r.tb.ApplyPatch(ctx, c, p); err != nil {
		m.Detail = err.Error()
		return m
	}
	m.Applied = true
	deadline := r.now().Add(c.Expect.Remediation.HealsWithin.Duration)
	for {
		st, err := r.tb.Defect(ctx, c)
		if err == nil && st != testbed.Holds {
			m.Healed = true
			break
		}
		if !r.now().Before(deadline) || sleep(ctx, r.cfg.Poll) != nil {
			break
		}
	}
	if m.Healed {
		m.CleanOK = true
		for i, o := range c.CleanTwin.Checks {
			st, err := r.tb.Check(ctx, c, o)
			if err != nil || st != testbed.Holds {
				m.CleanOK = false
				m.Detail = fmt.Sprintf("clean-twin check %d: %s", i, describe(st, err))
				break
			}
		}
	}
	if err := r.tb.RemovePatch(ctx, c, p); err != nil {
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

// ErrNoCases is returned when a selection is empty.
var ErrNoCases = errors.New("no cases selected")
