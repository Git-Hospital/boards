package istioctlanalyze_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/boards/internal/adapter"
	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/execx"
	"github.com/Git-Hospital/boards/internal/verdict"
)

// fakeIstioctl stands in for `istioctl`. It logs each call, then replays a
// recording: stdout, stderr and exit code from the directory in FAKE_REC.
const fakeIstioctl = `#!/bin/sh
echo "$*" >> "$FAKE_LOG"
if [ "$1" = version ]; then echo "client version: 9.9.1"; exit 0; fi
if [ -n "$FAKE_HANG" ]; then echo partial-out; echo partial-err >&2; exec sleep 60; fi
cat "$FAKE_REC.stdout"
cat "$FAKE_REC.stderr" >&2
exit "$(cat "$FAKE_REC.exit")"
`

// The recordings in testdata/rec are the real istioctl 1.24.1 (see
// testdata/README.md).
func rec(name string) string { return filepath.Join("testdata", "rec", name) }

// writeRec makes a recording that was NOT made with istioctl: hand-written.
func writeRec(t *testing.T, stdout string, exit int) string {
	t.Helper()
	base := filepath.Join(t.TempDir(), "hand")
	for ext, content := range map[string]string{".stdout": stdout, ".stderr": "", ".exit": strconv.Itoa(exit) + "\n"} {
		if err := os.WriteFile(base+ext, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

type result struct {
	v    *verdict.Verdict
	err  error // from reading verdict.json
	raw  string
	exit int
	log  []string
}

func run(t *testing.T, recording string) result { return runWith(t, recording, false, time.Minute) }

func runWith(t *testing.T, recording string, hang bool, window time.Duration) result {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "istioctl"), []byte(fakeIstioctl), 0o755); err != nil {
		t.Fatal(err)
	}
	recording, err := filepath.Abs(recording)
	if err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(t.TempDir(), "log")
	a, err := adapter.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	a.Secrets = append(a.Secrets, "FAKE_REC", "FAKE_LOG", "FAKE_HANG")
	t.Setenv("FAKE_REC", recording)
	t.Setenv("FAKE_LOG", logFile)
	if hang {
		t.Setenv("FAKE_HANG", "1")
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	work := t.TempDir()
	c := &casefile.Case{ID: "mesh/some-case", Dir: "/nowhere", Target: casefile.Target{Namespace: "demo", Service: "payments"}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := a.Run(ctx, adapter.Invocation{
		Case: c, Twin: casefile.TwinDefect, Work: work, Window: window, Budget: 25 * time.Second,
		Env: execx.BaseEnv(t.TempDir()),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(work, "raw.txt"))
	logged, _ := os.ReadFile(logFile)
	v, verr := verdict.Read(filepath.Join(work, verdict.FileName))
	return result{v: v, err: verr, raw: string(raw), exit: out.ExitCode, log: strings.Split(strings.TrimSpace(string(logged)), "\n")}
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

func TestCleanRunIsNotAnAlarm(t *testing.T) {
	r := run(t, rec("clean"))
	v := r.mustVerdict(t)
	if v.Alarmed || len(v.Events) != 0 || len(v.Culprits) != 0 || len(v.RuleIDs) != 0 {
		t.Errorf("verdict %+v", v)
	}
	if v.Tool.Name != "istioctl-analyze" || v.Tool.Version != "9.9.1" || v.Patch != nil {
		t.Errorf("tool %+v patch %+v", v.Tool, v.Patch)
	}
}

func TestErrorAlarmsWithTheNamedObject(t *testing.T) {
	r := run(t, rec("ist0101"))
	v := r.mustVerdict(t)
	if !v.Alarmed || len(v.Events) != 1 || v.Events[0].Type != "alarm" {
		t.Errorf("alarmed %v events %+v", v.Alarmed, v.Events)
	}
	if got := culprits(v); got != "VirtualService/demo/payments" {
		t.Errorf("culprits %q", got)
	}
	if len(v.RuleIDs) != 1 || v.RuleIDs[0] != "IST0101" {
		t.Errorf("ruleIds %v", v.RuleIDs)
	}
	if !strings.Contains(r.raw, "IST0101") {
		t.Errorf("raw output not kept:\n%s", r.raw)
	}
}

func TestWarningAloneAlarms(t *testing.T) {
	v := run(t, rec("envoyfilter")).mustVerdict(t)
	if !v.Alarmed || culprits(v) != "EnvoyFilter/demo/rate-limit" {
		t.Errorf("alarmed %v culprits %q", v.Alarmed, culprits(v))
	}
	if got := strings.Join(v.RuleIDs, " "); got != "IST0133 IST0151" {
		t.Errorf("ruleIds %q", got)
	}
}

func TestInfoAloneDoesNotAlarm(t *testing.T) {
	// NOT recorded: no Info message could be produced with istioctl 1.24.1 in
	// file mode. Hand-written in the shape of the recorded elements.
	info := `[{"code":"IST0102","level":"Info","message":"made up","origin":"Namespace demo"}]`
	v := run(t, writeRec(t, info, 0)).mustVerdict(t)
	if v.Alarmed || len(v.Events) != 0 || len(v.Culprits) != 0 || len(v.RuleIDs) != 0 {
		t.Errorf("verdict %+v", v)
	}
}

func TestTwoMessagesOnOneObjectGiveOneCulprit(t *testing.T) {
	v := run(t, rec("two-on-one")).mustVerdict(t)
	if got := culprits(v); got != "VirtualService/demo/payments" {
		t.Errorf("culprits %q, want one", got)
	}
	if got := strings.Join(v.RuleIDs, " "); got != "IST0101 IST0132" {
		t.Errorf("ruleIds %q", got)
	}
}

func TestErrorWithoutAKindNamespaceNameOriginAlarmsWithoutACulprit(t *testing.T) {
	// NOT recorded: hand-written origins that are not "Kind namespace/name".
	for _, origin := range []string{`"origin":"Namespace demo"`, `"origin":"Pod a.b c"`, `"origin":""`, `"x":1`} {
		stdout := `[{"code":"IST0101","level":"Error","message":"made up",` + origin + `}]`
		v := run(t, writeRec(t, stdout, 0)).mustVerdict(t)
		if !v.Alarmed || len(v.Culprits) != 0 {
			t.Errorf("%s: alarmed %v culprits %q, want an alarm with no culprit", origin, v.Alarmed, culprits(v))
		}
	}
}

func TestExitCode79WithMessagesIsAResult(t *testing.T) {
	// NOT recorded in JSON mode (istioctl exits 0 there); 79 is its text-mode code.
	stdout := `[{"code":"IST0101","level":"Error","message":"made up","origin":"VirtualService demo/payments"}]`
	if v := run(t, writeRec(t, stdout, 79)).mustVerdict(t); !v.Alarmed {
		t.Errorf("verdict %+v", v)
	}
}

func TestToolFailuresWriteNoVerdict(t *testing.T) {
	cases := map[string]string{
		"real failure (missing input, exit 1)": rec("missing"),
		"garbage stdout":                       writeRec(t, "Error: something broke\n", 0),
		"empty stdout":                         writeRec(t, "", 0),
		"not an array":                         writeRec(t, `{"level":"Error"}`, 0),
		"unknown level":                        writeRec(t, `[{"level":"Fatal","origin":"A b/c"}]`, 0),
		"valid JSON, exit 1":                   writeRec(t, `[]`, 1),
	}
	for name, recording := range cases {
		r := run(t, recording)
		if r.err == nil {
			t.Errorf("%s: a verdict was written: %+v", name, r.v)
		}
		if r.exit == 0 {
			t.Errorf("%s: adapter exit 0, the failure must be visible", name)
		}
	}
	if r := run(t, rec("missing")); !strings.Contains(r.raw, "no such file") {
		t.Errorf("stderr not kept in raw:\n%s", r.raw)
	}
}

func TestHangIsATimeoutWithPartialOutputKept(t *testing.T) {
	r := runWith(t, rec("clean"), true, 3*time.Second) // timeout 0.8 * 3s
	if r.err == nil || r.exit == 0 {
		t.Errorf("verdict err %v exit %d, want a failure", r.err, r.exit)
	}
	if !strings.Contains(r.raw, "partial-out") || !strings.Contains(r.raw, "partial-err") {
		t.Errorf("partial output not kept:\n%s", r.raw)
	}
}

func TestAnalyzesTheTargetNamespaceAsJSONThenAsksTheVersion(t *testing.T) {
	r := run(t, rec("clean"))
	r.mustVerdict(t)
	if len(r.log) != 2 || r.log[0] != "analyze -n demo -o json" || !strings.HasPrefix(r.log[1], "version") {
		t.Errorf("istioctl calls %q, want the analyze call, then the version call", r.log)
	}
}
