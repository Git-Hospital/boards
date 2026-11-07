package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/boards/internal/adapter"
	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/execx"
	"github.com/Git-Hospital/boards/internal/score"
	"github.com/Git-Hospital/boards/internal/testbed"
	"github.com/Git-Hospital/boards/internal/verdict"
)

// recording wraps the fake testbed and remembers whether each reset ran
// with a live context.
type recording struct {
	*testbed.Fake
	resets    int
	resetErrs []error
}

func (r *recording) Reset(ctx context.Context, c *casefile.Case, twin casefile.Twin) error {
	r.resets++
	r.resetErrs = append(r.resetErrs, ctx.Err())
	return r.Fake.Reset(ctx, c, twin)
}

func testCase(window, resolves time.Duration) *casefile.Case {
	d := func(x time.Duration) casefile.Duration { return casefile.Duration{Duration: x} }
	step := []casefile.Step{{Run: []string{"true"}}}
	return &casefile.Case{
		ID: "mesh/t", Track: casefile.TrackMesh, Kind: casefile.KindFault,
		Inject: step, Reset: step, Window: d(window),
		CleanTwin: &casefile.CleanTwin{Checks: []casefile.Oracle{{Exists: &casefile.ExistsOracle{Kind: "pod", Name: "x"}}}},
		Expect: casefile.Expect{
			AlarmWithin: d(window), ResolvesWithin: d(resolves),
			Culprits:    []casefile.CulpritGroup{{Level: 2, All: []casefile.Entity{{Kind: "Deployment", Name: "x"}}}},
			Remediation: &casefile.Remediation{Accept: []string{"rollback"}, NoPatch: "fail", HealsWithin: d(time.Second)},
		},
	}
}

// tool writes a shell adapter into a temp directory.
func tool(t *testing.T, mode, script string) *adapter.Adapter {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tool.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &adapter.Adapter{
		Name: "t", Category: "dummy", Tracks: []string{"mesh"}, Mode: mode, Output: "structured",
		ProposesFixes: true, Command: []string{"/bin/sh", "{{dir}}/tool.sh"}, Dir: dir,
	}
}

func newRunner(t *testing.T, tb testbed.Testbed, a *adapter.Adapter) *Runner {
	return New(Config{Work: t.TempDir(), Poll: 50 * time.Millisecond}, tb, a)
}

const verdictWithPatch = `{"schemaVersion":"boards.verdict/v1","alarmed":true,` +
	`"events":[{"at":"'"$(date -u +%Y-%m-%dT%H:%M:%SZ)"'","type":"alarm"}],` +
	`"culprits":[{"kind":"Deployment","name":"x"}],"patch":{"class":"rollback","format":"kubernetes-yaml","content":""}}`

const verdictWithoutPatch = `{"schemaVersion":"boards.verdict/v1","alarmed":true,` +
	`"events":[{"at":"'"$(date -u +%Y-%m-%dT%H:%M:%SZ)"'","type":"alarm"}],` +
	`"culprits":[{"kind":"Deployment","name":"x"}]}`

// After Ctrl-C the reset must still run with a live context, or the defect
// stays in the cluster; and the case is interrupted, not a tool timeout.
func TestInterruptStillResets(t *testing.T) {
	tb := &recording{Fake: testbed.NewFake(execx.BaseEnv(t.TempDir()))}
	a := tool(t, "one-shot", "sleep 30\n")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)

	run := newRunner(t, tb, a).twin(ctx, testCase(time.Minute, time.Minute), casefile.TwinDefect)

	if run.Inconclusive != "interrupted" {
		t.Errorf("inconclusive %q, want interrupted", run.Inconclusive)
	}
	if tb.resets == 0 {
		t.Fatal("reset never ran")
	}
	for i, err := range tb.resetErrs {
		if err != nil {
			t.Errorf("reset %d ran with a cancelled context: %v", i, err)
		}
	}
}

// A watcher may rewrite its verdict after the window. The patch boards
// applied is the one scored.
func TestWatcherPatchIsTheOneApplied(t *testing.T) {
	tb := testbed.NewFake(execx.BaseEnv(t.TempDir()))
	script := "echo '" + verdictWithPatch + "' > verdict.json\n" +
		"sleep 2\n" +
		"echo '" + verdictWithoutPatch + "' > verdict.json\n" +
		"sleep 30\n"
	a := tool(t, "watcher", script)
	c := testCase(time.Second, 3*time.Second)

	run := newRunner(t, tb, a).twin(context.Background(), c, casefile.TwinDefect)

	if run.Inconclusive != "" || run.VerdictErr != "" {
		t.Fatalf("run failed: %q %q", run.Inconclusive, run.VerdictErr)
	}
	if run.Verdict.Patch != nil {
		t.Fatal("the test needs the final verdict to have dropped its patch")
	}
	if run.Patch == nil || run.Patch.Class != "rollback" {
		t.Fatalf("applied patch %+v, want the rollback from the end of the window", run.Patch)
	}
	if run.Remedy == nil || !run.Remedy.Healed || !run.Remedy.Removed {
		t.Errorf("remedy %+v, want healed and removed", run.Remedy)
	}
	got := score.Score(c, a, []score.Run{run})
	if ax := got.Axes[score.Remediation]; ax.Status != score.Pass {
		t.Errorf("remediation %s (%s), want pass", ax.Status, ax.Reason)
	}
}

// stuckPatch is a testbed whose patches cannot be removed.
type stuckPatch struct{ *testbed.Fake }

func (stuckPatch) RemovePatch(context.Context, *casefile.Case, *verdict.Patch) error {
	return errors.New("conflict")
}

// A patch left in the cluster stops the run, since later cases would not
// run on the testbed, but the case itself is still scored.
func TestUnremovedPatchIsDirty(t *testing.T) {
	tb := stuckPatch{testbed.NewFake(execx.BaseEnv(t.TempDir()))}
	a := tool(t, "one-shot", "echo '"+verdictWithPatch+"' > verdict.json\n")
	c := testCase(time.Minute, time.Minute)
	got := newRunner(t, tb, a).Case(context.Background(), c)
	if got.Runs[0].Dirty != DirtyPatch || !Dirty(got) || got.Inconclusive != "" {
		t.Errorf("dirty %q %v, inconclusive %q", got.Runs[0].Dirty, Dirty(got), got.Inconclusive)
	}
	// The tool's irreversible patch is its own failure, not a lost case.
	if ax := got.Axes[score.Remediation]; ax.Status != score.Fail || ax.Reason != "not_removable" {
		t.Errorf("remediation %s (%s), want fail not_removable", ax.Status, ax.Reason)
	}
}

// halfPatch fails to apply part way and cannot be removed either.
type halfPatch struct{ stuckPatch }

func (halfPatch) ApplyPatch(context.Context, *casefile.Case, *verdict.Patch) error {
	return errors.New("second document rejected")
}

func TestHalfAppliedPatchIsDirty(t *testing.T) {
	tb := halfPatch{stuckPatch{testbed.NewFake(execx.BaseEnv(t.TempDir()))}}
	a := tool(t, "one-shot", "echo '"+verdictWithPatch+"' > verdict.json\n")
	if got := newRunner(t, tb, a).Case(context.Background(), testCase(time.Minute, time.Minute)); !Dirty(got) {
		t.Error("a half-applied patch that could not be removed left the run clean")
	}
}

// brokenService is a testbed where every patch removes the symptom and
// breaks the service: the clean-twin checks never hold afterwards.
type brokenService struct{ *testbed.Fake }

func (b brokenService) Check(ctx context.Context, c *casefile.Case, o casefile.Oracle) (testbed.State, error) {
	return testbed.Absent, nil
}

func TestPatchThatBreaksTheServiceIsNotHealed(t *testing.T) {
	tb := brokenService{testbed.NewFake(execx.BaseEnv(t.TempDir()))}
	a := tool(t, "one-shot", "echo '"+verdictWithPatch+"' > verdict.json\n")
	got := newRunner(t, tb, a).Case(context.Background(), testCase(time.Minute, time.Minute))
	if ax := got.Axes[score.Remediation]; ax.Status != score.Fail || ax.Reason != "broke_clean_twin" {
		t.Errorf("remediation %s (%s), want fail broke_clean_twin", ax.Status, ax.Reason)
	}
}

// A real tool runs in a directory named after nothing, with a fresh HOME outside the report, so
// its working directory, --work and HOME do not give away the case or the twin and it carries no
// state from one twin to the next; its files still land under work/<case>/<twin> (#9).
func TestToolWorkDirNamesNothing(t *testing.T) {
	tb := testbed.NewFake(execx.BaseEnv(t.TempDir()))
	a := tool(t, "one-shot", "pwd > where.txt\necho \"$@\" >> where.txt\necho \"$HOME\" >> where.txt\n"+
		"if [ -e \"$HOME/marker\" ]; then echo carried-state >> where.txt; fi\ntouch \"$HOME/marker\"\n"+
		"mkdir sub\nln -s /etc/hosts link\necho '"+verdictWithoutPatch+"' > verdict.json\n")
	a.Category = "mesh-specific"
	c := testCase(time.Second, time.Second)
	c.ID = "mesh/error-surge"
	r := newRunner(t, tb, a)

	for _, twin := range []casefile.Twin{casefile.TwinDefect, casefile.TwinClean} {
		run := r.twin(context.Background(), c, twin)
		if run.VerdictErr != "" || run.Inconclusive != "" {
			t.Fatalf("%s: run failed: %q %q", twin, run.VerdictErr, run.Inconclusive)
		}
		keep := filepath.Join(r.cfg.Work, "mesh_error-surge", string(twin))
		where, err := os.ReadFile(filepath.Join(keep, "where.txt"))
		if err != nil {
			t.Fatalf("%s: the tool's files were not kept for the report: %v", twin, err)
		}
		for _, leak := range []string{"error-surge", string(twin), r.cfg.Work, "carried-state"} {
			if strings.Contains(string(where), leak) {
				t.Errorf("%s: the tool's directory or arguments name %q:\n%s", twin, leak, where)
			}
		}
		for _, name := range []string{"verdict.json", "raw.txt"} {
			if _, err := os.Stat(filepath.Join(keep, name)); err != nil {
				t.Errorf("%s: %s not kept: %v", twin, name, err)
			}
		}
		// Only regular files are kept: no directory, and no symlink out of the work directory.
		for _, name := range []string{"sub", "link"} {
			if _, err := os.Lstat(filepath.Join(keep, name)); err == nil {
				t.Errorf("%s: %s was copied to the report", twin, name)
			}
		}
	}
}

// failInject is a testbed whose defect cannot be injected.
type failInject struct{ *testbed.Fake }

func (failInject) Inject(context.Context, *casefile.Case, casefile.Twin) error {
	return errors.New("inject failed")
}

// A run that ends early stops its watcher and waits for it before copying the files, and does
// not hang doing so.
func TestEarlyEndWaitsForTheWatcher(t *testing.T) {
	tb := failInject{testbed.NewFake(execx.BaseEnv(t.TempDir()))}
	a := tool(t, "watcher", "echo started > seen.txt\nsleep 30\n")
	a.Warmup.Duration = 500 * time.Millisecond // time to write seen.txt before the inject fails
	r := newRunner(t, tb, a)
	done := make(chan score.Run, 1)
	go func() { done <- r.twin(context.Background(), testCase(time.Second, time.Second), casefile.TwinDefect) }()
	select {
	case run := <-done:
		if run.Inconclusive != DirtyTestbed {
			t.Errorf("inconclusive %q, want %q", run.Inconclusive, DirtyTestbed)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the run did not end after the inject failed")
	}
	if _, err := os.Stat(filepath.Join(r.cfg.Work, "mesh_t", "defect", "seen.txt")); err != nil {
		t.Errorf("the watcher's files were not kept: %v", err)
	}
}
