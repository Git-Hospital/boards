// Package dummy holds the three tools that test boards itself (D32). They run
// against the dry-run testbed and must get exactly their expected scores, or
// the scorer is broken:
//
//   - oracle cheats by reading the answer from case.yaml: full marks.
//   - silent never says anything: detection fails, clean twins pass.
//   - panic alarms on everything: every clean twin is a false alarm.
package dummy

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/verdict"
)

// Kinds are the dummy tools.
var Kinds = []string{"oracle", "silent", "panic"}

// Main runs one dummy with the adapter protocol's arguments:
// run --case DIR --twin TWIN --work DIR --window D.
func Main(kind string, args []string) error {
	if len(args) == 0 || args[0] != "run" {
		return errors.New("usage: boards dummy <oracle|silent|panic> run --case DIR --twin TWIN --work DIR --window D")
	}
	fs := flag.NewFlagSet("dummy", flag.ContinueOnError)
	caseDir := fs.String("case", "", "")
	twin := fs.String("twin", "", "")
	work := fs.String("work", "", "")
	fs.String("window", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	c, err := casefile.Load(filepath.Join(*caseDir, casefile.FileName))
	if err != nil {
		return err
	}
	v, err := answer(kind, c, casefile.Twin(*twin), time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("dummy %s: %s [%s] alarmed=%v\n", kind, c.ID, *twin, v.Alarmed)
	return verdict.Write(*work, v)
}

func answer(kind string, c *casefile.Case, twin casefile.Twin, now time.Time) (*verdict.Verdict, error) {
	v := &verdict.Verdict{Tool: verdict.Tool{Name: "dummy-" + kind, Version: "0"}}
	alarm := func() {
		v.Alarmed = true
		v.Events = append(v.Events, verdict.Event{At: now, Type: "alarm"})
	}
	switch kind {
	case "oracle":
		switch twin {
		case casefile.TwinDefect:
			alarm()
			v.Culprits = bestCulprits(c)
			if r := c.Expect.Remediation; r != nil && len(r.Accept) > 0 {
				v.Patch = &verdict.Patch{Class: r.Accept[0], Format: "kubernetes-yaml"}
			}
		case casefile.TwinFixture:
			v.Measurements = map[string]*float64{}
			for id, val := range c.Expect.Values {
				if val == nil {
					v.Measurements[id] = nil
					continue
				}
				x := val.Expected
				v.Measurements[id] = &x
			}
		}
	case "silent":
		if twin == casefile.TwinFixture {
			v.Measurements = map[string]*float64{}
			for id := range c.Expect.Values {
				v.Measurements[id] = nil
			}
		}
	case "panic":
		if twin == casefile.TwinFixture {
			v.Measurements = map[string]*float64{}
			for id := range c.Expect.Values {
				zero := 0.0
				v.Measurements[id] = &zero
			}
			break
		}
		alarm()
	default:
		return nil, fmt.Errorf("unknown dummy %q, want one of %v", kind, Kinds)
	}
	return v, nil
}

func bestCulprits(c *casefile.Case) []casefile.Entity {
	for _, g := range c.Expect.Culprits {
		if g.Level == 2 {
			return g.All
		}
	}
	return nil
}
