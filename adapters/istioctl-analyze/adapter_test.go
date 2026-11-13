package istioctlanalyze_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/boards/internal/adapter"
	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/execx"
	"github.com/Git-Hospital/boards/internal/verdict"
)

// fakeIstioctl stands in for `istioctl`: version prints a version, analyze
// logs its arguments and prints the scripted output with the scripted exit.
const fakeIstioctl = `#!/bin/sh
if [ "$1" = version ]; then echo "client version: 9.9.1"; exit 0; fi
echo "$*" >> "$FAKE_LOG"
cat "$FAKE_OUT"
exit "${FAKE_EXIT:-0}"
`

// Lines recorded in results/istioctl-analyze.md (Istio 1.24.1).
const (
	ist0101 = `Error [IST0101] (VirtualService demo/payments) Referenced host+subset in destinationrule not found: "payments+v3"` + "\n"
	clean   = "No validation issues found\n"
)

// Not recorded: results/istioctl-analyze.md names the IST0133 and IST0151
// warnings but not their lines. These are written in istioctl's documented
// message format, "Level [code] (Kind namespace/name) text", with made-up text.
const (
	warning = "Warning [IST0133] (EnvoyFilter demo/rate-limit) Made-up warning text for a test.\n"
	// Info messages have the same shape; this one names a cluster-scoped
	// object in the documented "(Namespace name)" form, with made-up text.
	info = "Info [IST0102] (Namespace demo) Made-up info text for a test.\n"
)

type result struct {
	v    *verdict.Verdict
	err  error // from reading verdict.json
	raw  string
	exit int
	log  string
}

func run(t *testing.T, output string, exit int) result {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "istioctl"), []byte(fakeIstioctl), 0o755); err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	outFile, logFile := filepath.Join(scratch, "out"), filepath.Join(scratch, "log")
	if err := os.WriteFile(outFile, []byte(output), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := adapter.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	a.Secrets = append(a.Secrets, "FAKE_OUT", "FAKE_LOG", "FAKE_EXIT")
	t.Setenv("FAKE_OUT", outFile)
	t.Setenv("FAKE_LOG", logFile)
	t.Setenv("FAKE_EXIT", string(rune('0'+exit)))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	work := t.TempDir()
	c := &casefile.Case{ID: "mesh/some-case", Dir: "/nowhere", Target: casefile.Target{Namespace: "demo", Service: "payments"}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := a.Run(ctx, adapter.Invocation{
		Case: c, Twin: casefile.TwinDefect, Work: work, Window: time.Minute, Budget: 15 * time.Second,
		Env: execx.BaseEnv(t.TempDir()),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(work, "raw.txt"))
	logged, _ := os.ReadFile(logFile)
	v, verr := verdict.Read(filepath.Join(work, verdict.FileName))
	return result{v: v, err: verr, raw: string(raw), exit: out.ExitCode, log: string(logged)}
}

func (r result) mustVerdict(t *testing.T) *verdict.Verdict {
	t.Helper()
	if r.err != nil {
		t.Fatalf("no usable verdict: %v\nraw:\n%s", r.err, r.raw)
	}
	return r.v
}

func culprits(v *verdict.Verdict) string {
	var s []string
	for _, c := range v.Culprits {
		s = append(s, c.String())
	}
	return strings.Join(s, " ")
}

func TestErrorAlarmsWithTheNamedObject(t *testing.T) {
	r := run(t, ist0101, 1) // istioctl exits nonzero when it finds issues
	v := r.mustVerdict(t)
	if r.exit != 0 {
		t.Errorf("adapter exit %d, a nonzero istioctl exit with messages is a normal result", r.exit)
	}
	if !v.Alarmed || len(v.Events) != 1 || v.Events[0].Type != "alarm" {
		t.Errorf("alarmed %v events %+v", v.Alarmed, v.Events)
	}
	if got := culprits(v); got != "VirtualService/demo/payments" {
		t.Errorf("culprits %q", got)
	}
	if len(v.RuleIDs) != 1 || v.RuleIDs[0] != "IST0101" {
		t.Errorf("ruleIds %v", v.RuleIDs)
	}
	if v.Tool.Name != "istioctl-analyze" || v.Tool.Version != "9.9.1" || v.Patch != nil {
		t.Errorf("tool %+v patch %+v", v.Tool, v.Patch)
	}
	if !strings.Contains(r.raw, "IST0101") {
		t.Errorf("raw output not kept:\n%s", r.raw)
	}
}

func TestNoIssuesDoesNotAlarm(t *testing.T) {
	v := run(t, clean, 0).mustVerdict(t)
	if v.Alarmed || len(v.Events) != 0 || len(v.Culprits) != 0 {
		t.Errorf("verdict %+v", v)
	}
}

func TestWarningAloneAlarms(t *testing.T) {
	v := run(t, warning, 1).mustVerdict(t)
	if !v.Alarmed || culprits(v) != "EnvoyFilter/demo/rate-limit" {
		t.Errorf("alarmed %v culprits %q", v.Alarmed, culprits(v))
	}
}

func TestInfoAloneDoesNotAlarm(t *testing.T) {
	v := run(t, info, 0).mustVerdict(t)
	if v.Alarmed || len(v.Events) != 0 || len(v.Culprits) != 0 || len(v.RuleIDs) != 0 {
		t.Errorf("verdict %+v", v)
	}
}

func TestInfoDoesNotAddToAnAlarm(t *testing.T) {
	// The Info message's object is neither a culprit nor a rule.
	v := run(t, info+ist0101, 1).mustVerdict(t)
	if !v.Alarmed || culprits(v) != "VirtualService/demo/payments" || len(v.RuleIDs) != 1 {
		t.Errorf("verdict %+v culprits %q", v, culprits(v))
	}
}

func TestTwoMessagesOnOneObjectGiveOneCulprit(t *testing.T) {
	second := `Warning [IST0108] (VirtualService demo/payments) Made-up second message for a test.` + "\n"
	v := run(t, ist0101+second, 1).mustVerdict(t)
	if got := culprits(v); got != "VirtualService/demo/payments" {
		t.Errorf("culprits %q, want one", got)
	}
	if len(v.RuleIDs) != 2 {
		t.Errorf("ruleIds %v, want both codes", v.RuleIDs)
	}
}

func TestClusterScopedObjectIsNoCulprit(t *testing.T) {
	// Documented form for an object without a namespace; made-up text.
	scoped := "Warning [IST0101] (ClusterThing some-name) Made-up cluster-scoped warning.\n"
	v := run(t, scoped, 1).mustVerdict(t)
	if !v.Alarmed || len(v.Culprits) != 0 {
		t.Errorf("alarmed %v culprits %q, want an alarm without a guessed culprit", v.Alarmed, culprits(v))
	}
}

func TestGarbageWithNonzeroExitIsAToolFailure(t *testing.T) {
	r := run(t, "Error: unable to connect to the server\n", 1)
	if r.err == nil {
		t.Fatalf("a verdict was written: %+v", r.v)
	}
	if r.exit == 0 {
		t.Errorf("adapter exit 0, the failure must be visible")
	}
	if !strings.Contains(r.raw, "unable to connect") {
		t.Errorf("raw output not kept:\n%s", r.raw)
	}
}

func TestGarbageWithZeroExitIsAlsoAToolFailure(t *testing.T) {
	if r := run(t, "something unexpected\n", 0); r.err == nil || r.exit == 0 {
		t.Errorf("verdict err %v exit %d, want a failure", r.err, r.exit)
	}
}

func TestAnalyzesTheTargetNamespace(t *testing.T) {
	r := run(t, clean, 0)
	r.mustVerdict(t)
	if got := strings.TrimSpace(r.log); got != "analyze -n demo" {
		t.Errorf("istioctl was called as %q, want %q", got, "analyze -n demo")
	}
}
