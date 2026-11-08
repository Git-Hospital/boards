package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/execx"
)

// echoArgs is an adapter that writes its arguments and environment to raw.txt.
func echoArgs(t *testing.T, category string) *Adapter {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.sh"), []byte("echo \"$@\"; env\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Adapter{Name: "t", Category: category, Mode: "one-shot", Command: []string{"/bin/sh", "{{dir}}/a.sh"}, Dir: dir}
}

// A real tool must never be handed anything that names the case: the case directory holds the
// expected culprits, the reference fix and the rubric, the case id names the defect and the twin
// says whether there is one (#9). Only a dummy gets the directory and the twin.
func TestOnlyDummiesSeeTheCase(t *testing.T) {
	c := &casefile.Case{ID: "mesh/error-surge", Dir: "/cases/mesh/error-surge", Target: casefile.Target{Namespace: "demo", Service: "payments"}}
	for _, twin := range []casefile.Twin{casefile.TwinDefect, casefile.TwinClean} {
		for _, category := range []string{"mesh-specific", "dummy"} {
			work := t.TempDir()
			_, err := echoArgs(t, category).Run(context.Background(), Invocation{
				Case: c, Twin: twin, Work: work, Window: time.Minute, Budget: time.Minute,
				Env: execx.BaseEnv(t.TempDir()),
			})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(filepath.Join(work, "raw.txt"))
			out := string(raw)
			args, _, _ := strings.Cut(out, "\n") // the first line is the arguments, the rest the environment
			if category == "dummy" {
				if want := "run --case /cases/mesh/error-surge --twin " + string(twin) + " --work "; !strings.HasPrefix(args, want) {
					t.Errorf("dummy arguments %q, want them to start with %q", args, want)
				}
			} else {
				if want := "run --work " + work + " --window 1m0s"; args != want {
					t.Errorf("%s arguments %q, want exactly %q", category, args, want)
				}
				for _, leak := range []string{c.ID, "error-surge", c.Dir, "--twin", "--case", string(twin)} {
					if strings.Contains(out, leak) {
						t.Errorf("%s [%s] was given %q:\n%s", category, twin, leak, out)
					}
				}
			}
			if !strings.Contains(out, "BOARDS_TARGET_SERVICE=payments") || strings.Contains(out, "BOARDS_TARGET_WORKLOAD") {
				t.Errorf("%s: target variables wrong:\n%s", category, out)
			}
		}
	}
}

func TestArchitectureAdapterExampleLoads(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "architecture.md"))
	if err != nil {
		t.Fatal(err)
	}
	const heading = "### Adapter protocol\n"
	start := strings.Index(string(doc), heading)
	if start < 0 {
		t.Fatalf("architecture document is missing %q", strings.TrimSpace(heading))
	}
	section := string(doc[start+len(heading):])
	const fence = "```yaml\n"
	start = strings.Index(section, fence)
	if start < 0 {
		t.Fatal("adapter protocol section has no fenced YAML example")
	}
	yamlStart := start + len(fence)
	end := strings.Index(section[yamlStart:], "\n```")
	if end < 0 {
		t.Fatal("adapter protocol YAML example has no closing fence")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(section[yamlStart:yamlStart+end]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatalf("adapter example does not load: %v", err)
	}
}
