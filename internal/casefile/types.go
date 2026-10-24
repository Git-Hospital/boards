// Package casefile reads and validates case.yaml, the one file that describes
// a boards case (decision D9): the defect, how to inject it, how to tell it
// is live, and what a correct answer looks like.
package casefile

import (
	"fmt"
	"slices"
	"time"

	"gopkg.in/yaml.v3"
)

// Tracks are the domains boards measures (D5). A case belongs to exactly one.
const (
	TrackMesh = "mesh"
	TrackHelm = "helm"
	TrackCode = "code"
)

// Kinds of case. A fault case has a defect twin and a clean twin. A control
// case has no defect at all: the cluster carries harmless noise, and silence
// is the only correct answer (today's noise-only scenario).
const (
	KindFault   = "fault"
	KindControl = "control"
)

// Sets. Open cases are public and anyone can run them; exam cases are the
// rotating holdout (D14) and carry stricter provenance rules (D15).
const (
	SetOpen = "open"
	SetExam = "exam"
)

// Case is one parsed case.yaml.
type Case struct {
	ID              string        `yaml:"id"`
	Track           string        `yaml:"track"`
	Kind            string        `yaml:"kind"`
	Set             string        `yaml:"set"`
	Title           string        `yaml:"title"`
	Provenance      Provenance    `yaml:"provenance"`
	Author          string        `yaml:"author"`
	AuthorRelation  []string      `yaml:"authorRelation"`
	ContaminatedFor []string      `yaml:"contaminatedFor"`
	Target          Target        `yaml:"target"`
	Inject          []Step        `yaml:"inject"`
	Reset           []Step        `yaml:"reset"`
	CleanTwin       *CleanTwin    `yaml:"cleanTwin"`
	Oracle          Oracle        `yaml:"oracle"`
	Settle          Duration      `yaml:"settle"`
	Window          Duration      `yaml:"window"`
	Expect          Expect        `yaml:"expect"`
	Rubric          string        `yaml:"rubric"`
	Verification    *Verification `yaml:"verification"`

	// Dir is the directory the case was loaded from. Not part of the file.
	Dir string `yaml:"-"`
}

// Provenance says where a case comes from. Exam cases in the mesh and Helm
// tracks must point at a real, public event (D15).
type Provenance struct {
	Kind string `yaml:"kind"` // synthetic | issue | postmortem | cve
	URL  string `yaml:"url"`
	Note string `yaml:"note"`
}

// Verification records how a code-track value was checked by hand, since
// code cases have no issue or postmortem to cite (D15).
type Verification struct {
	Method     string `yaml:"method"`
	VerifiedBy string `yaml:"verifiedBy"`
}

// Target names what the case is about. Adapters receive it as BOARDS_TARGET_*.
type Target struct {
	Namespace string `yaml:"namespace"`
	Service   string `yaml:"service"`
	Workload  string `yaml:"workload"`
	Chart     string `yaml:"chart"`
	Repo      string `yaml:"repo"`
}

// Step is one command, in argv form. A step that needs shell quoting breaks
// on someone else's machine for reasons unrelated to the case.
type Step struct {
	Desc      string   `yaml:"desc"`
	Run       []string `yaml:"run"`
	Stdin     string   `yaml:"stdin"`
	AllowFail bool     `yaml:"allowFail"`
}

// CleanTwin is the healthy counterpart of a fault case.
type CleanTwin struct {
	// Inject sets up the clean twin. Empty means the healthy testbed itself.
	Inject []Step `yaml:"inject"`
	Reset  []Step `yaml:"reset"`
	// Checks must all hold on a healthy testbed with traffic present. They
	// are re-run after a patch, so a patch that heals by removing the service
	// fails here (D12).
	Checks []Oracle `yaml:"checks"`
}

// Oracle decides whether a condition holds right now. Exactly one kind is set.
type Oracle struct {
	PromQL *PromQLOracle `yaml:"promql"`
	Logs   *LogsOracle   `yaml:"logs"`
	Exec   *ExecOracle   `yaml:"exec"`
	Exists *ExistsOracle `yaml:"exists"`
	All    []Oracle      `yaml:"all"`
	Script []string      `yaml:"script"`
}

// PromQLOracle compares one instant query against a threshold.
type PromQLOracle struct {
	Query string  `yaml:"query"`
	Op    string  `yaml:"op"` // > >= < <=
	Value float64 `yaml:"value"`
	// NoData says what an empty result means. "unknown" (the default) is
	// neither live nor absent, and never counts as healed. "holds" is for
	// absence oracles, where traffic vanishing is the condition itself.
	NoData string `yaml:"noData"`
}

// LogsOracle holds while a workload's recent logs match a pattern.
type LogsOracle struct {
	Namespace string   `yaml:"namespace"`
	Workload  string   `yaml:"workload"` // e.g. deploy/loadgen
	Since     Duration `yaml:"since"`
	Pattern   string   `yaml:"pattern"` // case-insensitive regular expression
}

// ExecOracle runs a command in a pod. It holds when the command's outcome
// matches HoldsWhen.
type ExecOracle struct {
	Namespace string   `yaml:"namespace"`
	Pod       string   `yaml:"pod"`
	Command   []string `yaml:"command"`
	HoldsWhen string   `yaml:"holdsWhen"` // fails | succeeds
}

// ExistsOracle holds while an object exists.
type ExistsOracle struct {
	Kind      string `yaml:"kind"`
	Namespace string `yaml:"namespace"`
	Name      string `yaml:"name"`
}

// Kinds returns the oracle kinds that are set, for validation.
func (o Oracle) Kinds() []string {
	var k []string
	if o.PromQL != nil {
		k = append(k, "promql")
	}
	if o.Logs != nil {
		k = append(k, "logs")
	}
	if o.Exec != nil {
		k = append(k, "exec")
	}
	if o.Exists != nil {
		k = append(k, "exists")
	}
	if o.All != nil {
		k = append(k, "all")
	}
	if o.Script != nil {
		k = append(k, "script")
	}
	return k
}

// Expect is the ground truth a tool is scored against.
type Expect struct {
	AlarmWithin    Duration       `yaml:"alarmWithin"`
	ResolvesWithin Duration       `yaml:"resolvesWithin"`
	Culprits       []CulpritGroup `yaml:"culprits"`
	Quiet          []Entity       `yaml:"quiet"`
	Remediation    *Remediation   `yaml:"remediation"`
	// Values are the hand-verified answers of a code case (D22). A nil
	// value means the correct answer is null.
	Values map[string]*Value `yaml:"values"`
}

// CulpritGroup is one acceptable diagnosis. A tool that names every entity
// in All earns Level; the best level reached wins.
type CulpritGroup struct {
	Level int      `yaml:"level"` // 2 full, 1 partial
	All   []Entity `yaml:"all"`
}

// Entity is a named object a diagnosis can point at.
type Entity struct {
	Kind      string `yaml:"kind" json:"kind"`
	Namespace string `yaml:"namespace" json:"namespace,omitempty"`
	Name      string `yaml:"name" json:"name"`
}

func (e Entity) String() string {
	if e.Namespace == "" {
		return e.Kind + "/" + e.Name
	}
	return e.Kind + "/" + e.Namespace + "/" + e.Name
}

// Remediation describes which fixes count.
type Remediation struct {
	// Accept are the patch classes an operator would take. A patch in one of
	// them that heals is a pass.
	Accept []string `yaml:"accept"`
	// Forbid are the classes that make things worse here, such as loosening
	// authorization or a mesh patch for a client-side fault. Proposing one
	// fails the axis without applying it.
	Forbid []string `yaml:"forbid"`
	// NoPatch is the score for proposing nothing: "pass" where the right
	// output is a report, "fail" where a mechanical fix exists.
	NoPatch     string   `yaml:"noPatch"`
	HealsWithin Duration `yaml:"healsWithin"`
	// Reference is a known fix in an accepted class, as a kubernetes-yaml
	// file next to the case. The oracle dummy proposes it, so a live run
	// proves the case can be healed and that its heal checks work.
	Reference *Reference `yaml:"reference"`
}

// Reference names a known fix.
type Reference struct {
	Class string `yaml:"class"`
	File  string `yaml:"file"`
}

// Value is one expected measurement and how close a tool must come.
type Value struct {
	Expected  float64 `yaml:"expected"`
	Tolerance float64 `yaml:"tolerance"`
}

// Duration is a time.Duration written as "90s" or "5m" in YAML.
type Duration struct{ time.Duration }

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	d.Duration = v
	return nil
}

// Twin names one of the runs of a case.
type Twin string

const (
	TwinDefect Twin = "defect"
	TwinClean  Twin = "clean"
	// TwinControl is the only run of a control case.
	TwinControl Twin = "control"
	// TwinFixture is the only run of a code case: the tool measures a
	// fixture repository once (D22).
	TwinFixture Twin = "fixture"
)

// Steps returns the inject and reset steps of one twin. The clean twin's
// may be empty: it then runs on the untouched testbed.
func (c *Case) Steps(twin Twin) (inject, reset []Step) {
	switch twin {
	case TwinClean:
		if c.CleanTwin != nil {
			return c.CleanTwin.Inject, c.CleanTwin.Reset
		}
		return nil, nil
	case TwinFixture:
		return nil, nil
	}
	return c.Inject, c.Reset
}

// Twins returns the runs a case needs, in order.
func (c *Case) Twins() []Twin {
	switch {
	case c.Kind == KindControl:
		return []Twin{TwinControl}
	case c.Track == TrackCode:
		return []Twin{TwinFixture}
	default:
		return []Twin{TwinDefect, TwinClean}
	}
}

// IsContaminatedFor reports whether tool was developed against this case.
func (c *Case) IsContaminatedFor(tool string) bool {
	return slices.Contains(c.ContaminatedFor, tool)
}
