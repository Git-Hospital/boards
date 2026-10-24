package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Git-Hospital/boards/internal/adapter"
	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/execx"
	"github.com/Git-Hospital/boards/internal/score"
	"github.com/Git-Hospital/boards/internal/testbed"
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
