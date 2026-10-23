package casefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Validate runs the checks the JSON Schema cannot express. It returns every
// problem it finds, joined.
func Validate(c *Case) error {
	var errs []error
	add := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{c.ID}, a...)...))
	}

	if want := c.Track + "/" + filepath.Base(c.Dir); c.Dir != "" && c.ID != want {
		add("id must be %q, from its track and directory", want)
	}
	if !strings.HasPrefix(c.ID, c.Track+"/") {
		add("id must start with its track %q", c.Track)
	}

	switch {
	case c.Track == TrackCode:
		validateCode(c, add)
	case c.Kind == KindControl:
		validateControl(c, add)
	default:
		validateFault(c, add)
	}

	if c.Set == SetExam {
		validateExam(c, add)
	}

	for _, s := range allSteps(c) {
		if bad := clusterOverride(s); bad != "" {
			add("command %q selects a cluster with %q; boards supplies the cluster itself, and a case must never choose one (architecture §7)", strings.Join(s, " "), bad)
		}
	}

	if c.Rubric != "" {
		if _, err := os.Stat(filepath.Join(c.Dir, c.Rubric)); err != nil {
			add("rubric %s: %v", c.Rubric, err)
		}
	}
	return errors.Join(errs...)
}

func validateFault(c *Case, add func(string, ...any)) {
	if len(c.Inject) == 0 || len(c.Reset) == 0 {
		add("a fault case needs inject and reset steps")
	}
	if c.CleanTwin == nil {
		add("a fault case needs a cleanTwin")
	}
	if c.Expect.AlarmWithin.Duration <= 0 {
		add("expect.alarmWithin must be set")
	}
	if c.Expect.ResolvesWithin.Duration <= 0 {
		add("expect.resolvesWithin must be set")
	}
	// The winnability rule from meshmedic-prove: a deadline the window
	// cannot contain is a case no tool can pass.
	if c.Window.Duration < c.Expect.AlarmWithin.Duration {
		add("window %s is shorter than alarmWithin %s, so no tool could pass", c.Window.Duration, c.Expect.AlarmWithin.Duration)
	}
	if len(c.Expect.Culprits) == 0 {
		add("a fault case needs at least one culprit group")
	}
	hasFull := false
	for _, g := range c.Expect.Culprits {
		hasFull = hasFull || g.Level == 2
	}
	if len(c.Expect.Culprits) > 0 && !hasFull {
		add("at least one culprit group must be level 2, or full marks are impossible")
	}
	if len(c.Expect.Values) > 0 {
		add("expect.values belongs to the code track")
	}
	validateRemediation(c, add)
}

func validateControl(c *Case, add func(string, ...any)) {
	if c.CleanTwin != nil {
		add("a control case is its own clean run; it has no cleanTwin")
	}
	if len(c.Expect.Culprits) > 0 {
		add("a control case has no culprits: nothing is wrong")
	}
	if r := c.Expect.Remediation; r == nil || r.NoPatch != "pass" || len(r.Accept) > 0 {
		add("a control case needs expect.remediation with noPatch: pass and no accepted classes")
	}
}

func validateCode(c *Case, add func(string, ...any)) {
	if c.Kind != KindFault {
		add("a code case is kind fault")
	}
	if c.Target.Repo == "" {
		add("a code case needs target.repo")
	}
	if len(c.Expect.Values) == 0 {
		add("a code case needs expect.values")
	}
	if c.Expect.Remediation != nil || len(c.Expect.Culprits) > 0 || c.CleanTwin != nil {
		add("a code case measures accuracy only (D22): no remediation, culprits or cleanTwin")
	}
}

func validateRemediation(c *Case, add func(string, ...any)) {
	r := c.Expect.Remediation
	if r == nil {
		return
	}
	for _, a := range r.Accept {
		for _, f := range r.Forbid {
			if a == f {
				add("remediation class %q is both accepted and forbidden", a)
			}
		}
	}
	// Any patch outside Forbid is applied, even where no class is accepted
	// (a fix that heals there scores partial), so every fault case with a
	// remediation expectation needs a heal deadline and clean-twin checks.
	if r.HealsWithin.Duration <= 0 {
		add("expect.remediation.healsWithin must be set")
	}
	if c.CleanTwin == nil || len(c.CleanTwin.Checks) == 0 {
		add("cleanTwin.checks are required with a remediation expectation, so a patch that heals by removing the service fails (D12)")
	}
}

func validateExam(c *Case, add func(string, ...any)) {
	if c.Track == TrackCode {
		if c.Verification == nil {
			add("a code exam case needs verification: method and verifiedBy (D15)")
		}
		return
	}
	if c.Provenance.Kind == "synthetic" || c.Provenance.URL == "" {
		add("an exam case needs a real, public source: provenance kind issue, postmortem or cve, with a url (D15)")
	}
}

func allSteps(c *Case) [][]string {
	var out [][]string
	for _, s := range c.Inject {
		out = append(out, s.Run)
	}
	for _, s := range c.Reset {
		out = append(out, s.Run)
	}
	if t := c.CleanTwin; t != nil {
		for _, s := range t.Inject {
			out = append(out, s.Run)
		}
		for _, s := range t.Reset {
			out = append(out, s.Run)
		}
		for _, o := range t.Checks {
			out = append(out, oracleCommands(o)...)
		}
	}
	return append(out, oracleCommands(c.Oracle)...)
}

func oracleCommands(o Oracle) [][]string {
	var out [][]string
	if o.Exec != nil {
		out = append(out, o.Exec.Command)
	}
	if o.Script != nil {
		out = append(out, o.Script)
	}
	for _, sub := range o.All {
		out = append(out, oracleCommands(sub)...)
	}
	return out
}

// clusterOverride returns the argument that would point a command at a
// cluster other than the one boards supplies, or "". This is a lint: the
// private kubeconfig is the enforcement, and a script can hide anything.
func clusterOverride(argv []string) string {
	// kubectl's and helm's spellings: helm has --kube-context and
	// --kube-apiserver where kubectl has --context and --server.
	flags := []string{"--context", "--kubeconfig", "--server", "--cluster", "--kube-context", "--kube-apiserver"}
	for i, a := range argv {
		for _, f := range flags {
			if a == f || strings.HasPrefix(a, f+"=") {
				return a
			}
		}
		switch {
		case a == "-s" && i > 0 && argv[0] == "kubectl":
			return a
		case strings.Contains(a, "KUBECONFIG="):
			return a
		case a == "use-context", a == "set-context", a == "set-cluster":
			return a
		}
	}
	return ""
}
