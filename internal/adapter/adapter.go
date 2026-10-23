// Package adapter connects one tool to boards (decision D10). An adapter is a
// directory with an adapter.yaml and a command; boards runs the command, and
// the command runs the tool and writes a verdict.
package adapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/execx"
)

// FileName is the adapter description inside an adapter directory.
const FileName = "adapter.yaml"

// Categories group tools on the leaderboard. Comparisons are made within a
// category, never across (D26).
var Categories = []string{
	"mesh-specific", "config-linter", "general-ai-agent",
	"helm-tester", "static-linter", "code-metrics", "dummy",
}

// Adapter is one parsed adapter.yaml.
type Adapter struct {
	Name     string   `yaml:"name"`
	Category string   `yaml:"category"`
	Tracks   []string `yaml:"tracks"`
	// Mode is watcher (runs until stopped) or one-shot (exits on its own).
	Mode   string `yaml:"mode"`
	Output string `yaml:"output"` // structured | prose
	// ProposesFixes says whether the tool ever proposes a patch. When false,
	// the remediation axis is null for this tool, not 0 (D12).
	ProposesFixes bool `yaml:"proposesFixes"`
	NeedsLLM      bool `yaml:"needsLLM"`
	// Warmup is how long the tool watches the healthy testbed before the
	// defect is injected, for tools that learn a baseline.
	Warmup casefile.Duration `yaml:"warmup"`
	// Secrets are the names of environment variables passed through to the
	// tool, such as an API key. Nothing else from the caller's environment is.
	Secrets []string `yaml:"secrets"`
	// Alarm and Culprits state, in words, how the tool's output is mapped.
	// They are frozen per suite and published (D10).
	Alarm    string `yaml:"alarm"`
	Culprits string `yaml:"culprits"`
	// Command runs the adapter. "{{boards}}" is replaced by the boards binary
	// and "{{dir}}" by the adapter directory.
	Command []string `yaml:"command"`

	Dir string `yaml:"-"`
}

// Load reads and checks an adapter directory.
func Load(dir string) (*Adapter, error) {
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var a Adapter
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	if a.Dir, err = filepath.Abs(dir); err != nil {
		return nil, err
	}
	return &a, a.validate()
}

func (a *Adapter) validate() error {
	var errs []error
	if a.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if !slices.Contains(Categories, a.Category) {
		errs = append(errs, fmt.Errorf("category %q, want one of %s", a.Category, strings.Join(Categories, ", ")))
	}
	if len(a.Tracks) == 0 {
		errs = append(errs, errors.New("tracks is required"))
	}
	if a.Mode != "watcher" && a.Mode != "one-shot" {
		errs = append(errs, fmt.Errorf("mode %q, want watcher or one-shot", a.Mode))
	}
	if a.Output != "structured" && a.Output != "prose" {
		errs = append(errs, fmt.Errorf("output %q, want structured or prose", a.Output))
	}
	if a.Alarm == "" || a.Culprits == "" {
		errs = append(errs, errors.New("alarm and culprits must say how the tool's output is mapped (D10)"))
	}
	if len(a.Command) == 0 {
		errs = append(errs, errors.New("command is required"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("adapter %s: %w", a.Dir, err)
	}
	return nil
}

// Supports reports whether the adapter runs cases of a track.
func (a *Adapter) Supports(track string) bool { return slices.Contains(a.Tracks, track) }

// Invocation is one run of the adapter against one twin of one case.
type Invocation struct {
	Case   *casefile.Case
	Twin   casefile.Twin
	Work   string        // directory the adapter writes verdict.json and raw.txt into
	Window time.Duration // the detection window, passed to the adapter
	// Budget is when a one-shot tool is timed out. Zero means no deadline
	// beyond ctx: boards stops a watcher by cancelling ctx, after the reset,
	// so it can see whether the tool's alarm resolves.
	Budget time.Duration
	// Env is the environment boards built: kubeconfig, scratch HOME,
	// Prometheus address. Target variables and secrets are added here.
	Env []string
}

// Outcome is how the adapter process ended.
type Outcome struct {
	Elapsed  time.Duration
	ExitCode int
	TimedOut bool
	// Stopped is true when boards ended a watcher at the end of its window,
	// which is the normal end for a watcher and a timeout for a one-shot.
	Stopped bool
}

// Run starts the adapter and waits for it to exit or for the window to end.
// The tool's own output goes to raw.txt in the work directory, unedited.
func (a *Adapter) Run(ctx context.Context, inv Invocation) (Outcome, error) {
	self, err := os.Executable()
	if err != nil {
		return Outcome{}, err
	}
	argv := make([]string, 0, len(a.Command)+8)
	for _, part := range a.Command {
		part = strings.ReplaceAll(part, "{{boards}}", self)
		part = strings.ReplaceAll(part, "{{dir}}", a.Dir)
		argv = append(argv, part)
	}
	// Only a dummy gets the case directory: it holds the expected culprits,
	// the reference fix and the rubric. A real tool gets the case id, the
	// target and the cluster, as an operator would.
	caseArg := []string{"--case-id", inv.Case.ID}
	if a.Category == "dummy" {
		caseArg = []string{"--case", inv.Case.Dir}
	}
	argv = append(argv, "run")
	argv = append(argv, caseArg...)
	argv = append(argv,
		"--twin", string(inv.Twin),
		"--work", inv.Work,
		"--window", inv.Window.String(),
	)

	env := slices.Clone(inv.Env)
	t := inv.Case.Target
	env = append(env,
		"BOARDS_TARGET_NAMESPACE="+t.Namespace,
		"BOARDS_TARGET_SERVICE="+t.Service,
		"BOARDS_TARGET_CHART="+t.Chart,
		"BOARDS_TARGET_REPO="+t.Repo,
	)
	for _, name := range a.Secrets {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}

	raw, err := os.Create(filepath.Join(inv.Work, "raw.txt"))
	if err != nil {
		return Outcome{}, err
	}
	defer raw.Close()

	// A one-shot tool gets its window and is timed out after it. A watcher
	// runs until boards cancels ctx, which is the normal way it ends.
	wctx := ctx
	if inv.Budget > 0 {
		var cancel context.CancelFunc
		wctx, cancel = context.WithTimeout(ctx, inv.Budget)
		defer cancel()
	}
	start := time.Now()
	res, err := execx.Run(wctx, execx.Cmd{Argv: argv, Env: env, Dir: inv.Work, Stdout: raw, Stderr: raw})
	out := Outcome{Elapsed: time.Since(start), ExitCode: res.ExitCode}
	if res.TimedOut {
		out.Stopped = a.Mode == "watcher"
		out.TimedOut = a.Mode != "watcher"
	}
	return out, err
}
