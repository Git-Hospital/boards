// Package score turns what happened in a case's runs into axis results
// (architecture §5). It never sums them (D16).
package score

import (
	"math"
	"slices"
	"strings"
	"time"

	"github.com/Git-Hospital/boards/internal/adapter"
	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/verdict"
)

// Status of one axis for one case.
type Status string

const (
	Pass    Status = "pass"
	Partial Status = "partial"
	Fail    Status = "fail"
	Null    Status = "null"
)

// Axis names. CleanTwin passes when the tool stayed quiet where nothing was
// wrong: on a clean twin, or through a whole control case.
const (
	Detection   = "detection"
	Diagnosis   = "diagnosis"
	Remediation = "remediation"
	Resolves    = "resolves"
	CleanTwin   = "cleanTwin"
	Accuracy    = "accuracy"
	NullHonesty = "nullHonesty"
)

// AxisOrder is the order axes appear in reports.
var AxisOrder = []string{Detection, Diagnosis, Remediation, Resolves, CleanTwin, Accuracy, NullHonesty}

// Axis is one scored axis.
type Axis struct {
	Status Status   `json:"status"`
	Reason string   `json:"reason,omitempty"`
	Value  *float64 `json:"value,omitempty"`
}

func pass(v *float64) Axis              { return Axis{Status: Pass, Value: v} }
func fail(reason string) Axis           { return Axis{Status: Fail, Reason: reason} }
func null(reason string) Axis           { return Axis{Status: Null, Reason: reason} }
func num(f float64) *float64            { return &f }
func secs(d time.Duration) *float64     { return num(math.Round(d.Seconds()*10) / 10) }
func withValue(a Axis, v *float64) Axis { a.Value = v; return a }

// Run is what happened in one twin of a case. The runner fills it.
type Run struct {
	Twin casefile.Twin `json:"twin"`
	// Inconclusive is set when the run could not test the tool: the testbed
	// failed or the defect never went live. The tool is not blamed.
	Inconclusive string `json:"inconclusive,omitempty"`

	Verdict    *verdict.Verdict `json:"-"`
	VerdictErr string           `json:"verdictError,omitempty"`
	ExitCode   int              `json:"exitCode"`
	TimedOut   bool             `json:"timedOut,omitempty"`

	InjectedAt time.Time `json:"injectedAt"`
	// DetectFrom starts the detection clock: the defect was injected, the
	// case's settle time passed and its oracle saw it. An alarm raised
	// earlier counts as immediate.
	DetectFrom time.Time `json:"detectFrom"`
	// DefectEndedAt is when boards began the reset. A healed patch does not
	// end the defect, because boards removes the patch before resetting. An
	// alarm after it is not a detection.
	DefectEndedAt time.Time `json:"defectEndedAt"`

	// Patch is the patch boards acted on, read from the verdict at the end
	// of the window. A watcher may rewrite its verdict afterwards; the score
	// must be about the patch that was applied.
	Patch  *verdict.Patch `json:"patch,omitempty"`
	Remedy *Remedy        `json:"remedy,omitempty"`
	Cost   Cost           `json:"cost"`
}

// Remedy is what happened when boards applied the tool's patch (D12).
type Remedy struct {
	Applied bool `json:"applied"`
	// AppliedAt is when the patch went in. A tool that resolves after its
	// own patch healed the defect resolved rightly.
	AppliedAt time.Time `json:"appliedAt,omitempty"`
	Healed    bool      `json:"healed"`
	// HealedAt is when boards saw the defect's condition stop holding after
	// the patch. It is informational: boards removes the patch once checked,
	// so the defect returns until the reset, and scoring uses DefectEndedAt.
	HealedAt time.Time `json:"healedAt,omitempty"`
	CleanOK  bool      `json:"cleanOk"`
	Removed  bool      `json:"removed"`
	Detail   string    `json:"detail,omitempty"`
}

// Cost is what the run cost, measured by boards.
type Cost struct {
	WallSeconds    float64  `json:"wallSeconds"`
	ObjectsCreated []string `json:"objectsCreated"`
	ObjectsDeleted []string `json:"objectsDeleted"`
	// InventoryError is set when the cluster could not be listed, in which
	// case the object lists are empty and mean nothing.
	InventoryError string `json:"inventoryError,omitempty"`
	Tokens         *int   `json:"tokens"`
}

// crashed returns why a tool produced no usable answer, or "".
func (r *Run) crashed() string {
	switch {
	case r.TimedOut:
		return "timeout"
	case r.Verdict == nil:
		return "tool_crashed"
	}
	return ""
}

// Metric is one code-track measurement against its hand-verified value.
type Metric struct {
	ID       string   `json:"id"`
	Expected *float64 `json:"expected"`
	Reported *float64 `json:"reported"`
	AbsError *float64 `json:"absError,omitempty"`
	RelError *float64 `json:"relError,omitempty"`
	// Result is pass, fail, fabricated (a number where the answer is null),
	// abstained (null where a number was expected) or missing (the tool left
	// the key out, which is no answer).
	Result string `json:"result"`
}

// Case is one case's score for one tool.
type Case struct {
	ID           string          `json:"id"`
	Track        string          `json:"track"`
	Kind         string          `json:"kind"`
	Set          string          `json:"set"`
	Provenance   string          `json:"provenance"`
	Contaminated bool            `json:"contaminated"`
	Inconclusive string          `json:"inconclusive,omitempty"`
	Axes         map[string]Axis `json:"axes"`
	Metrics      []Metric        `json:"metrics,omitempty"`
	Runs         []Run           `json:"runs"`
}

// Score scores one case from its runs.
func Score(c *casefile.Case, a *adapter.Adapter, runs []Run) Case {
	out := Case{
		ID: c.ID, Track: c.Track, Kind: c.Kind, Set: c.Set,
		Provenance:   c.Provenance.Kind,
		Contaminated: c.IsContaminatedFor(a.Name),
		Axes:         map[string]Axis{},
		Runs:         runs,
	}
	for _, r := range runs {
		if r.Inconclusive != "" {
			out.Inconclusive = r.Inconclusive
			for _, name := range axesFor(c) {
				out.Axes[name] = null(r.Inconclusive)
			}
			return out
		}
	}
	for _, r := range runs {
		switch r.Twin {
		case casefile.TwinDefect:
			out.Axes[Detection] = detection(c, &r)
			out.Axes[Diagnosis] = diagnosis(c, a, &r)
			out.Axes[Remediation] = remediation(c, a, &r)
			out.Axes[Resolves] = resolves(c, a, &r)
		case casefile.TwinClean:
			out.Axes[CleanTwin] = quiet(&r)
		case casefile.TwinControl:
			out.Axes[CleanTwin] = quiet(&r)
			out.Axes[Remediation] = controlRemediation(a, &r)
		case casefile.TwinFixture:
			out.Metrics = metrics(c, &r)
			out.Axes[Accuracy], out.Axes[NullHonesty] = accuracy(out.Metrics, &r)
		}
	}
	return out
}

func axesFor(c *casefile.Case) []string {
	switch {
	case c.Track == casefile.TrackCode:
		return []string{Accuracy, NullHonesty}
	case c.Kind == casefile.KindControl:
		return []string{CleanTwin, Remediation}
	default:
		return []string{Detection, Diagnosis, Remediation, Resolves, CleanTwin}
	}
}

func detection(c *casefile.Case, r *Run) Axis {
	if why := r.crashed(); why != "" {
		return fail(why)
	}
	at, ok := r.Verdict.FirstAlarm(r.InjectedAt)
	if !ok || (!r.DefectEndedAt.IsZero() && at.After(r.DefectEndedAt)) {
		// An alarm raised once the defect was gone is not a detection.
		return fail("no_alarm")
	}
	from := r.DetectFrom
	if from.IsZero() {
		from = r.InjectedAt
	}
	after := max(at.Sub(from), 0)
	if after <= c.Expect.AlarmWithin.Duration {
		return pass(secs(after))
	}
	return Axis{Status: Partial, Reason: "alarm_after_deadline", Value: secs(after)}
}

func diagnosis(c *casefile.Case, a *adapter.Adapter, r *Run) Axis {
	if why := r.crashed(); why != "" {
		return fail(why)
	}
	if a.Output == "prose" {
		// Two people score prose against the rubric (D11); the runner never
		// guesses. Their scores are merged from scores.csv later.
		return null("prose_unrated")
	}
	named := r.Verdict.Culprits
	for _, q := range c.Expect.Quiet {
		if slices.ContainsFunc(named, func(e casefile.Entity) bool { return matches(q, e) }) {
			return fail("false_blame:" + q.String())
		}
	}
	best := 0
	for _, g := range c.Expect.Culprits {
		if g.Level > best && coversAll(named, g.All) {
			best = g.Level
		}
	}
	switch best {
	case 2:
		return pass(num(2))
	case 1:
		return Axis{Status: Partial, Reason: "culprit_partial", Value: num(1)}
	}
	return fail("culprit_not_named")
}

func remediation(c *casefile.Case, a *adapter.Adapter, r *Run) Axis {
	if !a.ProposesFixes {
		return null("tool_proposes_no_fixes")
	}
	exp := c.Expect.Remediation
	if exp == nil {
		return null("no_remediation_expected")
	}
	if why := r.crashed(); why != "" {
		return fail(why)
	}
	p := r.Patch
	if p == nil {
		if exp.NoPatch == "pass" {
			return pass(nil)
		}
		return fail("no_patch")
	}
	if slices.Contains(exp.Forbid, p.Class) {
		return fail("forbidden_class:" + p.Class)
	}
	m := r.Remedy
	switch {
	case m == nil || !m.Applied:
		return fail("patch_not_applied")
	case !m.Healed:
		return fail("not_healed")
	case !m.CleanOK:
		return fail("broke_clean_twin")
	case !m.Removed:
		return fail("not_removable")
	case !slices.Contains(exp.Accept, p.Class):
		return Axis{Status: Partial, Reason: "healed_outside_accepted_class:" + p.Class}
	}
	return pass(nil)
}

func resolves(c *casefile.Case, a *adapter.Adapter, r *Run) Axis {
	if a.Mode != "watcher" {
		return null("tool_one_shot")
	}
	if why := r.crashed(); why != "" {
		return fail(why)
	}
	alarm, ok := r.Verdict.FirstAlarm(r.InjectedAt)
	if !ok || (!r.DefectEndedAt.IsZero() && alarm.After(r.DefectEndedAt)) {
		return null("no_alarm")
	}
	if r.DefectEndedAt.IsZero() {
		return null("not_measured")
	}
	// The alarm must be clear resolvesWithin after the reset. A tool that
	// resolved when its own patch healed the defect, and stayed clear while
	// boards removed the patch and reset, passes with no delay; one that
	// re-alarmed when the defect came back must resolve again.
	deadline := r.DefectEndedAt.Add(c.Expect.ResolvesWithin.Duration)
	since, clear := r.Verdict.ClearedAt(deadline)
	// Clearing while the defect was still live is not a resolve. The
	// defect was gone after the reset, or after the tool's own patch went in
	// if that patch healed it.
	earliest := r.DefectEndedAt
	if m := r.Remedy; m != nil && m.Healed && !m.AppliedAt.IsZero() {
		earliest = m.AppliedAt
	}
	if clear && since.Before(earliest) {
		return fail("resolved_while_live")
	}
	if !clear {
		if last, late := r.Verdict.FinallyCleared(); late && last.After(deadline) {
			return withValue(fail("resolved_after_deadline"), secs(last.Sub(r.DefectEndedAt)))
		}
		return fail("never_resolved")
	}
	return pass(secs(max(since.Sub(r.DefectEndedAt), 0)))
}

func quiet(r *Run) Axis {
	if why := r.crashed(); why != "" {
		return fail(why) // a crash is not silence
	}
	if r.Verdict.Alarmed {
		return fail("false_alarm")
	}
	return pass(nil)
}

func controlRemediation(a *adapter.Adapter, r *Run) Axis {
	if !a.ProposesFixes {
		return null("tool_proposes_no_fixes")
	}
	if why := r.crashed(); why != "" {
		return fail(why)
	}
	// Any patch against a healthy service counts, including one a watcher
	// wrote after the window.
	if r.Patch != nil || r.Verdict.Patch != nil {
		return fail("patched_a_healthy_service")
	}
	return pass(nil)
}

func metrics(c *casefile.Case, r *Run) []Metric {
	ids := make([]string, 0, len(c.Expect.Values))
	for id := range c.Expect.Values {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var out []Metric
	for _, id := range ids {
		exp := c.Expect.Values[id]
		var rep *float64
		answered := false
		if r.Verdict != nil {
			rep, answered = r.Verdict.Measurements[id]
		}
		m := Metric{ID: id, Reported: rep}
		if exp != nil {
			m.Expected = num(exp.Expected)
		}
		switch {
		case !answered:
			// Only an explicit null means "could not measure". A key left
			// out is no answer at all, and counts against the tool.
			m.Result = "missing"
		case exp == nil && rep == nil:
			m.Result = "pass"
		case exp == nil:
			m.Result = "fabricated"
		case rep == nil:
			m.Result = "abstained"
		default:
			abs := math.Abs(*rep - exp.Expected)
			m.AbsError = num(abs)
			if exp.Expected != 0 {
				m.RelError = num(abs / math.Abs(exp.Expected))
			}
			m.Result = "fail"
			if abs <= exp.Tolerance {
				m.Result = "pass"
			}
		}
		out = append(out, m)
	}
	return out
}

// accuracy summarises metrics into the two code-track axes. Abstentions do
// not count against accuracy; they are reported separately as a coverage
// gap, never as dishonesty (architecture §5).
func accuracy(ms []Metric, r *Run) (acc, honesty Axis) {
	if why := r.crashed(); why != "" {
		return fail(why), fail(why)
	}
	var passed, failed, honest, fabricated int
	for _, m := range ms {
		switch {
		case m.Expected == nil && m.Result == "pass":
			honest++
		case m.Result == "fabricated", m.Result == "missing" && m.Expected == nil:
			fabricated++
		case m.Result == "missing":
			failed++
		case m.Result == "pass":
			passed++
		case m.Result == "fail":
			failed++
		}
	}
	acc = ratio(passed, failed, "all_abstained")
	honesty = ratio(honest, fabricated, "no_null_expected")
	if honesty.Status == Fail || honesty.Status == Partial {
		honesty.Reason = "fabricated_or_missing"
	}
	return acc, honesty
}

func ratio(good, bad int, emptyReason string) Axis {
	switch {
	case good+bad == 0:
		return null(emptyReason)
	case bad == 0:
		return pass(num(1))
	case good == 0:
		return withValue(fail("all_wrong"), num(0))
	}
	return Axis{Status: Partial, Value: num(float64(good) / float64(good+bad))}
}

func coversAll(named, want []casefile.Entity) bool {
	for _, w := range want {
		if !slices.ContainsFunc(named, func(e casefile.Entity) bool { return matches(w, e) }) {
			return false
		}
	}
	return true
}

// matches compares an expected entity with a named one. Kinds compare without
// case; a namespace counts only when both sides give one, since many tools
// report bare names inside the case's namespace.
func matches(want, got casefile.Entity) bool {
	if !strings.EqualFold(want.Kind, got.Kind) || want.Name != got.Name {
		return false
	}
	return want.Namespace == "" || got.Namespace == "" || want.Namespace == got.Namespace
}
