package report

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Git-Hospital/boards/internal/adapter"
	"github.com/Git-Hospital/boards/internal/score"
	"github.com/Git-Hospital/boards/internal/testbed"
)

// Architecture section 8: report.json and the Markdown for a fixed run are
// compared byte for byte. The terminal table is compared too. A change that
// reorders axes, drops a null reason or shifts a column changes published
// results, and it fails here first.
//
// To accept a change on purpose, rewrite the files and read the diff:
//
//	UPDATE_GOLDEN=1 go test ./internal/report/

func value(f float64) *float64 { return &f }

// fixedCases is one case of each kind a report has to show: every axis
// passing, a partial, a fail with its reason, a null axis with its reason, a
// contaminated case, and an inconclusive one that is counted and not scored.
func fixedCases() []score.Case {
	return []score.Case{
		{
			ID: "mesh-all-pass", Track: "mesh", Kind: "fault", Set: "public", Provenance: "incident",
			Axes: map[string]score.Axis{
				score.Detection:   {Status: score.Pass, Value: value(12.5)},
				score.Diagnosis:   {Status: score.Pass},
				score.Remediation: {Status: score.Pass},
				score.Resolves:    {Status: score.Pass},
				score.CleanTwin:   {Status: score.Pass},
			},
			Runs: []score.Run{},
		},
		{
			ID: "mesh-partial", Track: "mesh", Kind: "fault", Set: "public", Provenance: "synthetic",
			Axes: map[string]score.Axis{
				score.Detection: {Status: score.Pass, Value: value(40)},
				score.Diagnosis: {Status: score.Partial, Reason: "culprit named | quiet neighbour blamed"},
				score.CleanTwin: {Status: score.Pass},
			},
			Runs: []score.Run{},
		},
		{
			ID: "mesh-fail", Track: "mesh", Kind: "fault", Set: "holdout", Provenance: "incident",
			Axes: map[string]score.Axis{
				score.Detection: {Status: score.Fail, Reason: "no alarm within 5m0s"},
				score.Diagnosis: {Status: score.Fail, Reason: "no alarm"},
				score.CleanTwin: {Status: score.Fail, Reason: "alarm on the clean twin"},
			},
			Runs: []score.Run{},
		},
		{
			ID: "mesh-null-axis", Track: "mesh", Kind: "fault", Set: "public", Provenance: "incident",
			Axes: map[string]score.Axis{
				score.Detection:   {Status: score.Pass, Value: value(3)},
				score.Remediation: {Status: score.Null, Reason: "tool proposes no fixes"},
				score.NullHonesty: {Status: score.Pass},
			},
			Runs: []score.Run{},
		},
		{
			ID: "mesh-contaminated", Track: "mesh", Kind: "fault", Set: "public", Provenance: "incident",
			Contaminated: true,
			Axes: map[string]score.Axis{
				score.Detection: {Status: score.Pass, Value: value(8)},
				score.Diagnosis: {Status: score.Fail, Reason: "wrong culprit"},
			},
			Runs: []score.Run{},
		},
		{
			ID: "mesh-inconclusive", Track: "mesh", Kind: "fault", Set: "public", Provenance: "incident",
			Inconclusive: "the defect never went live",
			Axes:         map[string]score.Axis{},
			Runs:         []score.Run{},
		},
	}
}

// fixedReport builds the report through New, as a run does, and then pins the
// one field New takes from the clock.
func fixedReport() *Report {
	started := time.Date(2026, 10, 30, 9, 0, 0, 0, time.FixedZone("CET", 3600))
	a := &adapter.Adapter{Name: "dummy-tool", Category: "detector", Dir: "adapters/dummy/tool"}
	tb := testbed.Info{Name: "dry-run", Standard: false, Notes: []string{"dry run: no cluster"}}
	r := New("0.0.0-test", "fixed", tb, a, "1.2.3", fixedCases(), started)
	r.Finished = r.Started.Add(90 * time.Second)
	return r
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1 to write it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file.\n--- got\n%s\n--- want\n%s\nIf the change is meant, run UPDATE_GOLDEN=1 go test ./internal/report/ and read the diff.", name, got, want)
	}
}

func TestGoldenReports(t *testing.T) {
	r := fixedReport()

	jsonPath := filepath.Join(t.TempDir(), "report.json")
	if err := r.WriteJSON(jsonPath); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "report.json", raw)

	var md bytes.Buffer
	if err := r.WriteMarkdown(&md); err != nil {
		t.Fatal(err)
	}
	golden(t, "report.md", md.Bytes())

	var term bytes.Buffer
	if err := r.WriteTerminal(&term); err != nil {
		t.Fatal(err)
	}
	golden(t, "report.txt", term.Bytes())

	// What Read gives back writes the same bytes again, so a baseline is the
	// report it was written from.
	back, err := Read(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	again := filepath.Join(t.TempDir(), "again.json")
	if err := back.WriteJSON(again); err != nil {
		t.Fatal(err)
	}
	if rewritten, _ := os.ReadFile(again); !bytes.Equal(rewritten, raw) {
		t.Error("a report read back and written again is not the same bytes")
	}
}

// The summary is a rate per axis with its n; a null is counted beside the
// rate and never in it, and an inconclusive case is in no tally (D16).
func TestSummaryCountsNullsBesideTheRate(t *testing.T) {
	s := fixedReport().Summary
	det := s.Clean[score.Detection]
	if det.N != 4 || det.Pass != 3 || det.Fail != 1 || det.Rate == nil || *det.Rate != 0.75 {
		t.Errorf("clean detection = %+v, want 3 of 4 passing", det)
	}
	rem := s.Clean[score.Remediation]
	if rem.N != 1 || rem.Null != 1 || rem.Rate == nil || *rem.Rate != 1 {
		t.Errorf("clean remediation = %+v, want one pass scored and one null beside it", rem)
	}
	if got := s.Contaminated[score.Diagnosis]; got.N != 1 || got.Fail != 1 {
		t.Errorf("contaminated diagnosis = %+v, want the contaminated case alone", got)
	}
	if s.Inconclusive != 1 {
		t.Errorf("inconclusive = %d, want 1", s.Inconclusive)
	}
	onlyNull := summarise([]score.Case{{Axes: map[string]score.Axis{score.Remediation: {Status: score.Null}}}})
	if got := onlyNull.Clean[score.Remediation]; got.Rate != nil || got.N != 0 || got.Null != 1 {
		t.Errorf("an axis that was never scored = %+v, want a null rate with the null counted", got)
	}
}

func TestRegressions(t *testing.T) {
	axes := func(detection, diagnosis score.Status) map[string]score.Axis {
		return map[string]score.Axis{score.Detection: {Status: detection}, score.Diagnosis: {Status: diagnosis}}
	}
	base := &Report{Cases: []score.Case{
		{ID: "same", Axes: axes(score.Pass, score.Partial)},
		{ID: "worse", Axes: axes(score.Pass, score.Pass)},
		{ID: "better", Axes: axes(score.Fail, score.Partial)},
		{ID: "dropped", Axes: axes(score.Pass, score.Pass)},
		{ID: "to-null", Axes: axes(score.Pass, score.Pass)},
		{ID: "from-null", Axes: axes(score.Null, score.Pass)},
	}}
	now := &Report{Cases: []score.Case{
		{ID: "same", Axes: axes(score.Pass, score.Partial)},
		{ID: "worse", Axes: axes(score.Partial, score.Fail)},
		{ID: "better", Axes: axes(score.Pass, score.Pass)},
		{ID: "to-null", Axes: axes(score.Null, score.Pass)},
		{ID: "from-null", Axes: axes(score.Fail, score.Pass)},
		{ID: "new-in-this-run", Axes: axes(score.Fail, score.Fail)},
	}}
	want := []string{
		"worse detection: pass -> partial",
		"worse diagnosis: pass -> fail",
		"dropped: not run",
		"to-null detection: pass -> null",
	}
	if got := now.Regressions(base); !reflect.DeepEqual(got, want) {
		t.Errorf("Regressions:\n got  %q\n want %q", got, want)
	}
	if got := base.Regressions(base); len(got) != 0 {
		t.Errorf("a report regresses against itself: %q", got)
	}
}
