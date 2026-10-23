package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/report"
	"github.com/Git-Hospital/boards/internal/score"
)

// The dummy adapters call "{{boards}} dummy <kind> run ...", and under test
// the boards binary is this test binary. TestMain plays the CLI for them.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "dummy" {
		os.Exit(run(os.Args[1:]))
	}
	os.Exit(m.Run())
}

const casesDir = "../../cases"

func dryRun(t *testing.T, kind string, extra ...string) (*report.Report, int) {
	t.Helper()
	out := t.TempDir()
	args := append([]string{"run", "--adapter", "../../adapters/dummy/" + kind, "--dry-run", "--cases", casesDir, "--out", out}, extra...)
	code := run(args)
	rep, err := report.Read(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	return rep, code
}

func loadCases(t *testing.T) map[string]*casefile.Case {
	t.Helper()
	cases, err := casefile.LoadDir(casesDir)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]*casefile.Case{}
	for _, c := range cases {
		m[c.ID] = c
	}
	return m
}

// TestDummies is decision D32: unless all three dummy tools get exactly
// their expected scores on every case, the scorer is broken.
func TestDummies(t *testing.T) {
	cases := loadCases(t)
	if len(cases) == 0 {
		t.Fatal("no cases")
	}

	t.Run("oracle scores full marks", func(t *testing.T) {
		rep, code := dryRun(t, "oracle")
		if code != exitPass {
			t.Errorf("exit %d", code)
		}
		if len(rep.Cases) != len(cases) {
			t.Fatalf("ran %d cases, have %d", len(rep.Cases), len(cases))
		}
		for _, c := range rep.Cases {
			for name, ax := range c.Axes {
				if ax.Status == score.Fail || ax.Status == score.Partial {
					t.Errorf("%s %s: %s (%s)", c.ID, name, ax.Status, ax.Reason)
				}
				if ax.Status == score.Null && ax.Reason != "tool_one_shot" {
					t.Errorf("%s %s: null for %q; the oracle must be scored everywhere it can be", c.ID, name, ax.Reason)
				}
			}
		}
	})

	t.Run("silent fails detection and keeps every clean twin", func(t *testing.T) {
		rep, _ := dryRun(t, "silent")
		for _, c := range rep.Cases {
			src := cases[c.ID]
			if src.Kind == casefile.KindFault {
				expectStatus(t, c, score.Detection, score.Fail, "no_alarm")
				expectStatus(t, c, score.Diagnosis, score.Fail, "culprit_not_named")
				want := score.Fail
				if src.Expect.Remediation.NoPatch == "pass" {
					want = score.Pass
				}
				expectStatus(t, c, score.Remediation, want, "")
			}
			expectStatus(t, c, score.CleanTwin, score.Pass, "")
		}
	})

	t.Run("panic false-alarms on every clean twin", func(t *testing.T) {
		rep, _ := dryRun(t, "panic")
		for _, c := range rep.Cases {
			if cases[c.ID].Kind == casefile.KindFault {
				expectStatus(t, c, score.Detection, score.Pass, "")
			}
			expectStatus(t, c, score.CleanTwin, score.Fail, "false_alarm")
		}
		if got := rep.Summary.Clean[score.CleanTwin].Rate; got == nil || *got != 0 {
			t.Errorf("clean twin rate %v, want 0", got)
		}
	})
}

func expectStatus(t *testing.T, c score.Case, axis string, want score.Status, reason string) {
	t.Helper()
	ax, ok := c.Axes[axis]
	if !ok {
		t.Errorf("%s: no %s axis", c.ID, axis)
		return
	}
	if ax.Status != want || (reason != "" && ax.Reason != reason) {
		t.Errorf("%s %s: got %s (%s), want %s (%s)", c.ID, axis, ax.Status, ax.Reason, want, reason)
	}
}

// A gate must fail when a tool gets worse than its baseline.
func TestBaselineRegression(t *testing.T) {
	out := t.TempDir()
	if code := run([]string{"run", "--adapter", "../../adapters/dummy/oracle", "--dry-run", "--cases", casesDir, "--out", out}); code != exitPass {
		t.Fatalf("baseline run exit %d", code)
	}
	base := filepath.Join(out, "report.json")
	if _, code := dryRun(t, "oracle", "--baseline", base); code != exitPass {
		t.Errorf("same tool against its own baseline: exit %d, want %d", code, exitPass)
	}
	if _, code := dryRun(t, "silent", "--baseline", base); code != exitFail {
		t.Errorf("silent against the oracle's baseline: exit %d, want %d", code, exitFail)
	}
}

func TestReportIsMeasuredOrNull(t *testing.T) {
	out := t.TempDir()
	run([]string{"run", "--adapter", "../../adapters/dummy/panic", "--dry-run", "--cases", casesDir, "--out", out})
	raw, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rep map[string]any
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	if _, ok := rep["total"]; ok {
		t.Error("report has a total; D16 forbids one")
	}
	md, err := os.ReadFile(filepath.Join(out, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "not standard") {
		t.Error("a dry-run report must say it is not standard")
	}
}

// A live run refuses before touching any cluster when the lock is bad or
// the cluster to reuse is not one boards built.
func TestLiveRunRefusesEarly(t *testing.T) {
	for name, extra := range map[string][]string{
		"missing lock":  {"--lock", filepath.Join(t.TempDir(), "none.lock")},
		"foreign reuse": {"--lock", "../../testbed/testbed.lock", "--reuse", "kind-meshmedic-demo"},
	} {
		args := append([]string{"run", "--adapter", "../../adapters/dummy/oracle", "--cases", casesDir, "--out", t.TempDir()}, extra...)
		if code := run(args); code != exitUsage {
			t.Errorf("%s: exit %d, want %d", name, code, exitUsage)
		}
	}
}
