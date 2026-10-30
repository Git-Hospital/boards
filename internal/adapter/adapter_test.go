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

// A real tool must never be handed the case directory: it holds the
// expected culprits, the reference fix and the rubric.
func TestOnlyDummiesSeeTheCase(t *testing.T) {
	c := &casefile.Case{ID: "mesh/x", Dir: "/cases/mesh/x", Target: casefile.Target{Namespace: "demo", Service: "payments"}}
	for category, want := range map[string]string{
		"mesh-specific": "--case-id mesh/x",
		"dummy":         "--case /cases/mesh/x",
	} {
		work := t.TempDir()
		_, err := echoArgs(t, category).Run(context.Background(), Invocation{
			Case: c, Twin: casefile.TwinDefect, Work: work, Window: time.Minute, Budget: time.Minute,
			Env: execx.BaseEnv(t.TempDir()),
		})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filepath.Join(work, "raw.txt"))
		out := string(raw)
		if !strings.Contains(out, want) {
			t.Errorf("%s: arguments lack %q:\n%s", category, want, out)
		}
		if category != "dummy" && strings.Contains(out, c.Dir) {
			t.Errorf("%s was given the case directory", category)
		}
		if !strings.Contains(out, "BOARDS_TARGET_SERVICE=payments") || strings.Contains(out, "BOARDS_TARGET_WORKLOAD") {
			t.Errorf("%s: target variables wrong:\n%s", category, out)
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
