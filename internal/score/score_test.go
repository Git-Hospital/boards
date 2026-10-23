package score

import (
	"testing"
	"time"

	"github.com/Git-Hospital/boards/internal/adapter"
	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/verdict"
)

var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func meshCase() *casefile.Case {
	return &casefile.Case{
		ID: "mesh/x", Track: casefile.TrackMesh, Kind: casefile.KindFault,
		ContaminatedFor: []string{"meshmedic"},
		Expect: casefile.Expect{
			AlarmWithin:    casefile.Duration{Duration: 2 * time.Minute},
			ResolvesWithin: casefile.Duration{Duration: 2 * time.Minute},
			Culprits: []casefile.CulpritGroup{
				{Level: 2, All: []casefile.Entity{{Kind: "Deployment", Namespace: "demo", Name: "payments-v2"}}},
				{Level: 1, All: []casefile.Entity{{Kind: "Service", Name: "payments"}}},
			},
			Quiet: []casefile.Entity{{Kind: "Deployment", Name: "payments-v1"}},
			Remediation: &casefile.Remediation{
				Accept: []string{"rollback"}, Forbid: []string{"restart"}, NoPatch: "fail",
			},
		},
	}
}

func tool(mode string) *adapter.Adapter {
	return &adapter.Adapter{Name: "t", Mode: mode, Output: "structured", ProposesFixes: true}
}

func alarmAt(d time.Duration) []verdict.Event {
	return []verdict.Event{{At: t0.Add(d), Type: "alarm"}}
}

func defect(v *verdict.Verdict, m *Remedy) Run {
	return Run{Twin: casefile.TwinDefect, Verdict: v, InjectedAt: t0, DefectEndedAt: t0.Add(10 * time.Minute), Remedy: m}
}

func TestDetection(t *testing.T) {
	c := meshCase()
	for name, tc := range map[string]struct {
		v    *verdict.Verdict
		want Status
	}{
		"in time":    {&verdict.Verdict{Alarmed: true, Events: alarmAt(time.Minute)}, Pass},
		"late":       {&verdict.Verdict{Alarmed: true, Events: alarmAt(3 * time.Minute)}, Partial},
		"never":      {&verdict.Verdict{}, Fail},
		"crashed":    {nil, Fail},
		"before inj": {&verdict.Verdict{Alarmed: true, Events: alarmAt(-time.Minute)}, Fail},
		// defect() ends the defect at 10m: an alarm after it is no detection.
		"after the defect ended": {&verdict.Verdict{Alarmed: true, Events: alarmAt(11 * time.Minute)}, Fail},
	} {
		r := defect(tc.v, nil)
		if got := detection(c, &r); got.Status != tc.want {
			t.Errorf("%s: %s (%s), want %s", name, got.Status, got.Reason, tc.want)
		}
	}
}

func TestDiagnosis(t *testing.T) {
	c := meshCase()
	e := func(kind, ns, name string) casefile.Entity {
		return casefile.Entity{Kind: kind, Namespace: ns, Name: name}
	}
	for name, tc := range map[string]struct {
		named []casefile.Entity
		want  Status
	}{
		"full":                    {[]casefile.Entity{e("deployment", "demo", "payments-v2")}, Pass},
		"bare name counts":        {[]casefile.Entity{e("Deployment", "", "payments-v2")}, Pass},
		"wrong namespace":         {[]casefile.Entity{e("Deployment", "prod", "payments-v2")}, Fail},
		"partial":                 {[]casefile.Entity{e("Service", "demo", "payments")}, Partial},
		"false blame beats a hit": {[]casefile.Entity{e("Deployment", "demo", "payments-v2"), e("Deployment", "demo", "payments-v1")}, Fail},
		"nothing":                 {nil, Fail},
	} {
		r := defect(&verdict.Verdict{Culprits: tc.named}, nil)
		if got := diagnosis(c, tool("one-shot"), &r); got.Status != tc.want {
			t.Errorf("%s: %s (%s), want %s", name, got.Status, got.Reason, tc.want)
		}
	}
	prose := tool("one-shot")
	prose.Output = "prose"
	r := defect(&verdict.Verdict{}, nil)
	if got := diagnosis(c, prose, &r); got.Status != Null || got.Reason != "prose_unrated" {
		t.Errorf("prose: %+v, want null prose_unrated", got)
	}
}

func TestRemediation(t *testing.T) {
	c := meshCase()
	healed := &Remedy{Applied: true, Healed: true, CleanOK: true, Removed: true}
	patch := func(class string) *verdict.Verdict {
		return &verdict.Verdict{Patch: &verdict.Patch{Class: class}}
	}
	// The scorer reads the patch boards applied (Run.Patch), which the
	// runner copies from the verdict at the end of the window.
	for name, tc := range map[string]struct {
		v    *verdict.Verdict
		m    *Remedy
		want Status
	}{
		"accepted and healed":     {patch("rollback"), healed, Pass},
		"healed outside accepted": {patch("traffic-shift"), healed, Partial},
		"forbidden":               {patch("restart"), nil, Fail},
		"did not heal":            {patch("rollback"), &Remedy{Applied: true, Removed: true}, Fail},
		"broke the clean twin":    {patch("rollback"), &Remedy{Applied: true, Healed: true, Removed: true}, Fail},
		"no patch where needed":   {&verdict.Verdict{}, nil, Fail},
		"crashed":                 {nil, nil, Fail},
	} {
		r := defect(tc.v, tc.m)
		if tc.v != nil {
			r.Patch = tc.v.Patch
		}
		if got := remediation(c, tool("one-shot"), &r); got.Status != tc.want {
			t.Errorf("%s: %s (%s), want %s", name, got.Status, got.Reason, tc.want)
		}
	}
	noFix := tool("one-shot")
	noFix.ProposesFixes = false
	r := defect(&verdict.Verdict{}, nil)
	if got := remediation(c, noFix, &r); got.Status != Null {
		t.Errorf("tool without fixes: %s, want null", got.Status)
	}
}

func TestResolves(t *testing.T) {
	c := meshCase()
	ended := t0.Add(10 * time.Minute)
	v := func(resolveAfter time.Duration) *verdict.Verdict {
		return &verdict.Verdict{Alarmed: true, Events: []verdict.Event{
			{At: t0.Add(time.Minute), Type: "alarm"},
			{At: ended.Add(resolveAfter), Type: "resolve"},
		}}
	}
	for name, tc := range map[string]struct {
		v    *verdict.Verdict
		mode string
		want Status
	}{
		"in time":    {v(time.Minute), "watcher", Pass},
		"late":       {v(5 * time.Minute), "watcher", Fail},
		"before end": {v(-time.Minute), "watcher", Fail},
		"one-shot":   {v(time.Minute), "one-shot", Null},
	} {
		r := defect(tc.v, nil)
		if got := resolves(c, tool(tc.mode), &r); got.Status != tc.want {
			t.Errorf("%s: %s (%s), want %s", name, got.Status, got.Reason, tc.want)
		}
	}
}

// The detection clock starts when the defect is observable, after settle
// and the oracle wait, so a slow oracle is not charged to the tool.
func TestDetectionClockStartsWhenObservable(t *testing.T) {
	c := meshCase()
	r := defect(&verdict.Verdict{Alarmed: true, Events: alarmAt(4 * time.Minute)}, nil)
	r.DetectFrom = t0.Add(3 * time.Minute)
	if got := detection(c, &r); got.Status != Pass || *got.Value != 60 {
		t.Errorf("%+v, want pass after 60s", got)
	}
	early := defect(&verdict.Verdict{Alarmed: true, Events: alarmAt(time.Second)}, nil)
	early.DetectFrom = t0.Add(time.Minute)
	if got := detection(c, &early); got.Status != Pass || *got.Value != 0 {
		t.Errorf("an alarm before the clock started: %+v, want pass at 0", got)
	}
}

// A tool that resolves when its own patch heals the defect resolved in
// time, even though boards resets later.
func TestResolveAfterHeal(t *testing.T) {
	c := meshCase()
	healedAt := t0.Add(6 * time.Minute)
	r := defect(&verdict.Verdict{Alarmed: true, Events: []verdict.Event{
		{At: t0.Add(time.Minute), Type: "alarm"},
		{At: healedAt.Add(30 * time.Second), Type: "resolve"},
	}}, &Remedy{Applied: true, Healed: true, HealedAt: healedAt})
	if got := resolves(c, tool("watcher"), &r); got.Status != Pass {
		t.Errorf("%s (%s), want pass", got.Status, got.Reason)
	}
}

// A crash is never null: a flaky tool would otherwise leave the
// denominator and raise its own rates (architecture §2).
func TestCrashIsAFailure(t *testing.T) {
	c := meshCase()
	got := Score(c, tool("watcher"), []Run{
		{Twin: casefile.TwinDefect, InjectedAt: t0, VerdictErr: "boom"},
		{Twin: casefile.TwinClean},
	})
	for _, name := range []string{Detection, Diagnosis, Remediation, Resolves, CleanTwin} {
		if got.Axes[name].Status != Fail {
			t.Errorf("%s: %s, want fail", name, got.Axes[name].Status)
		}
	}
}

func TestInconclusiveScoresNothing(t *testing.T) {
	got := Score(meshCase(), tool("one-shot"), []Run{{Twin: casefile.TwinDefect, Inconclusive: "oracle_never_live"}})
	if got.Inconclusive == "" {
		t.Fatal("not marked inconclusive")
	}
	for name, ax := range got.Axes {
		if ax.Status != Null {
			t.Errorf("%s: %s, want null", name, ax.Status)
		}
	}
}

func TestContamination(t *testing.T) {
	c := meshCase()
	mm := tool("one-shot")
	mm.Name = "meshmedic"
	if !Score(c, mm, nil).Contaminated {
		t.Error("meshmedic should be contaminated on this case")
	}
	if Score(c, tool("one-shot"), nil).Contaminated {
		t.Error("another tool should not be")
	}
}

func TestOmittedNullIsNotHonest(t *testing.T) {
	c := &casefile.Case{ID: "code/x", Track: casefile.TrackCode, Kind: casefile.KindFault, Expect: casefile.Expect{
		Values: map[string]*casefile.Value{"coverage": nil},
	}}
	run := Run{Twin: casefile.TwinFixture, Verdict: &verdict.Verdict{Measurements: map[string]*float64{}}}
	if got := Score(c, tool("one-shot"), []Run{run}).Axes[NullHonesty]; got.Status != Fail {
		t.Errorf("an empty answer scored %s on null honesty, want fail", got.Status)
	}
}

func TestCodeMetrics(t *testing.T) {
	f := func(x float64) *float64 { return &x }
	c := &casefile.Case{ID: "code/x", Track: casefile.TrackCode, Kind: casefile.KindFault, Expect: casefile.Expect{
		Values: map[string]*casefile.Value{
			"cyclomatic": {Expected: 7, Tolerance: 0},
			"coverage":   nil, // no coverage report: the honest answer is null
			"truck":      {Expected: 2, Tolerance: 0},
			"churn":      nil,
			"modules":    {Expected: 4, Tolerance: 0},
		},
	}}
	run := Run{Twin: casefile.TwinFixture, Verdict: &verdict.Verdict{Measurements: map[string]*float64{
		"cyclomatic": f(9),
		"coverage":   f(64.3),
		"truck":      nil,
	}}}
	got := Score(c, tool("one-shot"), []Run{run})
	results := map[string]string{}
	for _, m := range got.Metrics {
		results[m.ID] = m.Result
	}
	// churn and modules are left out of the verdict: no answer, not honesty.
	want := map[string]string{"cyclomatic": "fail", "coverage": "fabricated", "truck": "abstained", "churn": "missing", "modules": "missing"}
	for id, w := range want {
		if results[id] != w {
			t.Errorf("%s: %s, want %s", id, results[id], w)
		}
	}
	if got.Axes[NullHonesty].Status != Fail {
		t.Errorf("null honesty: %s, want fail", got.Axes[NullHonesty].Status)
	}
	if got.Axes[Accuracy].Status != Fail {
		t.Errorf("accuracy: %s, want fail (abstentions do not count)", got.Axes[Accuracy].Status)
	}
}
