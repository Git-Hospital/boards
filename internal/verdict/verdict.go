// Package verdict is the shared answer every adapter writes (decision D10):
// what the tool said, in a form boards can score without reading prose.
package verdict

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Git-Hospital/boards/internal/casefile"
)

// SchemaVersion is the only verdict version this boards reads.
const SchemaVersion = "boards.verdict/v1"

// FileName is where an adapter keeps its verdict, inside the work directory.
const FileName = "verdict.json"

// Verdict is one tool's answer for one run of a case.
type Verdict struct {
	SchemaVersion string `json:"schemaVersion"`
	Tool          Tool   `json:"tool"`
	// Alarmed is whether the tool raised anything during the run.
	Alarmed bool `json:"alarmed"`
	// Events are the times the tool opened and closed alarms. boards measures
	// time to detect and time to resolve from these, against its own clock.
	Events   []Event           `json:"events"`
	Culprits []casefile.Entity `json:"culprits"`
	RuleIDs  []string          `json:"ruleIds"`
	Patch    *Patch            `json:"patch"`
	// Measurements are a code-track tool's numbers; a nil value is the tool
	// saying it could not measure that metric.
	Measurements map[string]*float64 `json:"measurements"`
	Prose        *string             `json:"prose"`
	Tokens       *int                `json:"tokens"`
}

// Tool identifies what produced the verdict.
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Event is an alarm opening or closing.
type Event struct {
	At   time.Time `json:"at"`
	Type string    `json:"type"` // alarm | resolve
}

// Patch is a fix the tool proposed. Class is assigned by the adapter's
// published mapping (D10), never by the tool itself.
type Patch struct {
	Class   string `json:"class"`
	Format  string `json:"format"` // kubernetes-yaml
	Content string `json:"content"`
}

// FirstAlarm returns the first alarm at or after since.
func (v *Verdict) FirstAlarm(since time.Time) (time.Time, bool) {
	return v.first("alarm", since)
}

// FirstResolve returns the first resolve at or after since.
func (v *Verdict) FirstResolve(since time.Time) (time.Time, bool) {
	return v.first("resolve", since)
}

func (v *Verdict) first(kind string, since time.Time) (time.Time, bool) {
	var best time.Time
	found := false
	for _, e := range v.Events {
		if e.Type != kind || e.At.Before(since) {
			continue
		}
		if !found || e.At.Before(best) {
			best, found = e.At, true
		}
	}
	return best, found
}

// Read loads and checks a verdict file.
func Read(path string) (*Verdict, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v Verdict
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if v.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%s: schemaVersion %q, want %q", path, v.SchemaVersion, SchemaVersion)
	}
	for _, e := range v.Events {
		if e.Type != "alarm" && e.Type != "resolve" {
			return nil, fmt.Errorf("%s: event type %q, want alarm or resolve", path, e.Type)
		}
	}
	if v.Alarmed != hasAlarm(v.Events) {
		return nil, fmt.Errorf("%s: alarmed is %v but the events say otherwise", path, v.Alarmed)
	}
	if v.Patch != nil && v.Patch.Class == "" {
		return nil, fmt.Errorf("%s: a patch without a class cannot be scored; the adapter's mapping must assign one", path)
	}
	return &v, nil
}

func hasAlarm(events []Event) bool {
	for _, e := range events {
		if e.Type == "alarm" {
			return true
		}
	}
	return false
}

// Write stores a verdict atomically, so a reader never sees half a file.
// Adapters of watcher tools rewrite it while the tool runs.
func Write(dir string, v *Verdict) error {
	v.SchemaVersion = SchemaVersion
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".verdict-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, FileName))
}
