// Package report writes a run's results as report.json, Markdown and a
// terminal table (D23). Every number in it comes from the scorer; a summary
// is a rate per axis with its n, never a total (D16).
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Git-Hospital/boards/internal/adapter"
	"github.com/Git-Hospital/boards/internal/score"
	"github.com/Git-Hospital/boards/internal/testbed"
)

// SchemaVersion of report.json.
const SchemaVersion = "boards.report/v1"

// Report is one tool's run over a selection of cases.
type Report struct {
	SchemaVersion string       `json:"schemaVersion"`
	Boards        string       `json:"boards"`
	Suite         string       `json:"suite"`
	Testbed       testbed.Info `json:"testbed"`
	Tool          Tool         `json:"tool"`
	Started       time.Time    `json:"started"`
	Finished      time.Time    `json:"finished"`
	Cases         []score.Case `json:"cases"`
	// Summary is split: cases the tool was developed against are summarised
	// apart from the rest, and only "clean" carries standing (D13).
	Summary Summary `json:"summary"`
}

// Tool identifies the tool and the adapter that ran it. The adapter's
// commit is part of the result, since its mapping decides what counts (D10).
type Tool struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	Category      string `json:"category"`
	Adapter       string `json:"adapter"`
	AdapterCommit string `json:"adapterCommit,omitempty"`
}

// Summary holds the per-axis tallies.
type Summary struct {
	Clean        map[string]Tally `json:"clean"`
	Contaminated map[string]Tally `json:"contaminated,omitempty"`
	Inconclusive int              `json:"inconclusive"`
}

// Tally counts one axis. Rate is passes over the cases where the axis was
// scored (pass, partial or fail); null cases are counted, not hidden.
type Tally struct {
	N       int      `json:"n"`
	Pass    int      `json:"pass"`
	Partial int      `json:"partial"`
	Fail    int      `json:"fail"`
	Null    int      `json:"null"`
	Rate    *float64 `json:"rate"`
}

// New builds a report and its summary.
func New(boardsVersion, suite string, tb testbed.Info, a *adapter.Adapter, toolVersion string, cases []score.Case, started time.Time) *Report {
	r := &Report{
		SchemaVersion: SchemaVersion,
		Boards:        boardsVersion,
		Suite:         suite,
		Testbed:       tb,
		Tool:          Tool{Name: a.Name, Version: toolVersion, Category: a.Category, Adapter: a.Dir},
		Started:       started.UTC(),
		Finished:      time.Now().UTC(),
		Cases:         cases,
	}
	r.Summary = summarise(cases)
	return r
}

func summarise(cases []score.Case) Summary {
	s := Summary{Clean: map[string]Tally{}}
	for _, c := range cases {
		if c.Inconclusive != "" {
			s.Inconclusive++
			continue
		}
		bucket := s.Clean
		if c.Contaminated {
			if s.Contaminated == nil {
				s.Contaminated = map[string]Tally{}
			}
			bucket = s.Contaminated
		}
		for name, ax := range c.Axes {
			t := bucket[name]
			switch ax.Status {
			case score.Pass:
				t.Pass++
			case score.Partial:
				t.Partial++
			case score.Fail:
				t.Fail++
			case score.Null:
				t.Null++
			}
			bucket[name] = t
		}
	}
	for _, bucket := range []map[string]Tally{s.Clean, s.Contaminated} {
		for name, t := range bucket {
			t.N = t.Pass + t.Partial + t.Fail
			if t.N > 0 {
				rate := float64(t.Pass) / float64(t.N)
				t.Rate = &rate
			}
			bucket[name] = t
		}
	}
	return s
}

// WriteJSON writes report.json.
func (r *Report) WriteJSON(path string) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// Read loads a report.json, for --baseline.
func Read(path string) (*Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%s: schemaVersion %q, want %q", path, r.SchemaVersion, SchemaVersion)
	}
	return &r, nil
}

// Regressions lists every case axis that is worse than in the baseline. A
// case missing from the baseline is not a regression; one missing from this
// run is, since a gate must not pass by running less.
func (r *Report) Regressions(base *Report) []string {
	now := map[string]score.Case{}
	for _, c := range r.Cases {
		now[c.ID] = c
	}
	var out []string
	for _, b := range base.Cases {
		c, ok := now[b.ID]
		if !ok {
			out = append(out, b.ID+": not run")
			continue
		}
		for _, name := range score.AxisOrder {
			was, okb := b.Axes[name]
			is, okc := c.Axes[name]
			if okb && okc && rank(is.Status) < rank(was.Status) {
				out = append(out, fmt.Sprintf("%s %s: %s -> %s", b.ID, name, was.Status, is.Status))
			}
		}
	}
	return out
}

func rank(s score.Status) int {
	switch s {
	case score.Pass:
		return 3
	case score.Partial:
		return 2
	case score.Fail:
		return 1
	}
	return 0 // null: not comparable, never a regression on its own
}

// axesIn returns the axes that occur in the cases, in report order.
func axesIn(cases []score.Case) []string {
	seen := map[string]bool{}
	for _, c := range cases {
		for name := range c.Axes {
			seen[name] = true
		}
	}
	var out []string
	for _, name := range score.AxisOrder {
		if seen[name] {
			out = append(out, name)
		}
	}
	return out
}

func cell(a score.Axis, ok bool) string {
	if !ok {
		return ""
	}
	s := string(a.Status)
	if a.Reason != "" && a.Status != score.Pass {
		s += " (" + a.Reason + ")"
	}
	return s
}

// WriteMarkdown writes the human report.
func (r *Report) WriteMarkdown(w io.Writer) error {
	axes := axesIn(r.Cases)
	var b strings.Builder
	fmt.Fprintf(&b, "# boards report: %s %s\n\n", r.Tool.Name, r.Tool.Version)
	fmt.Fprintf(&b, "- Suite %s, boards %s, testbed %s", r.Suite, r.Boards, r.Testbed.Name)
	if !r.Testbed.Standard {
		b.WriteString(" (**not standard**: this result never enters the official table)")
	}
	fmt.Fprintf(&b, "\n- Tool category: %s; adapter: `%s`\n", r.Tool.Category, r.Tool.Adapter)
	fmt.Fprintf(&b, "- Ran %s to %s\n\n", r.Started.Format(time.RFC3339), r.Finished.Format(time.RFC3339))

	b.WriteString("There is no total: each axis is reported on its own, with the cases it could not score counted beside it.\n\n")
	writeSummary(&b, "Summary", r.Summary.Clean, axes)
	if len(r.Summary.Contaminated) > 0 {
		writeSummary(&b, "Summary, contaminated cases (the tool was developed against these; they carry no standing)", r.Summary.Contaminated, axes)
	}
	if r.Summary.Inconclusive > 0 {
		fmt.Fprintf(&b, "%d case(s) were inconclusive: the testbed could not put the tool in front of the defect, so nothing was scored.\n\n", r.Summary.Inconclusive)
	}

	b.WriteString("## Cases\n\n| case | set |")
	for _, a := range axes {
		b.WriteString(" " + a + " |")
	}
	b.WriteString("\n|---|---|" + strings.Repeat("---|", len(axes)) + "\n")
	for _, c := range r.Cases {
		set := c.Set
		if c.Contaminated {
			set += ", contaminated"
		}
		fmt.Fprintf(&b, "| `%s` | %s |", c.ID, set)
		for _, name := range axes {
			ax, ok := c.Axes[name]
			b.WriteString(" " + strings.ReplaceAll(cell(ax, ok), "|", `\|`) + " |")
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeSummary(b *strings.Builder, title string, tallies map[string]Tally, axes []string) {
	fmt.Fprintf(b, "## %s\n\n| axis | rate | pass | partial | fail | null |\n|---|---|---|---|---|---|\n", title)
	for _, name := range axes {
		t, ok := tallies[name]
		if !ok {
			continue
		}
		fmt.Fprintf(b, "| %s | %s | %d | %d | %d | %d |\n", name, rateString(t.Rate), t.Pass, t.Partial, t.Fail, t.Null)
	}
	b.WriteString("\n")
}

func rateString(r *float64) string {
	if r == nil {
		return "null"
	}
	return fmt.Sprintf("%.0f%%", *r*100)
}

// WriteTerminal writes a compact table for the console.
func (r *Report) WriteTerminal(w io.Writer) error {
	axes := axesIn(r.Cases)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "CASE\t%s\n", strings.ToUpper(strings.Join(axes, "\t")))
	for _, c := range r.Cases {
		row := []string{c.ID}
		if c.Contaminated {
			row[0] += "*"
		}
		for _, name := range axes {
			ax, ok := c.Axes[name]
			if !ok {
				row = append(row, "-")
				continue
			}
			row = append(row, string(ax.Status))
		}
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	var parts []string
	for _, name := range axes {
		if t, ok := r.Summary.Clean[name]; ok {
			parts = append(parts, fmt.Sprintf("%s %s (n=%d, null=%d)", name, rateString(t.Rate), t.N, t.Null))
		}
	}
	fmt.Fprintf(w, "\n%s\n", strings.Join(parts, "; "))
	if slices.ContainsFunc(r.Cases, func(c score.Case) bool { return c.Contaminated }) {
		fmt.Fprintln(w, "* contaminated for this tool: summarised separately")
	}
	if !r.Testbed.Standard {
		fmt.Fprintf(w, "testbed %s is not standard: this result never enters the official table\n", r.Testbed.Name)
	}
	return nil
}
