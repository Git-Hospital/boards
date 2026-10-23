// Package oracle evaluates a case's oracles against a live testbed: is the
// defect's condition there or not (architecture §4).
package oracle

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/execx"
	"github.com/Git-Hospital/boards/internal/prom"
)

// State is the answer of an oracle. NoData is its own state: an empty query
// result is neither "the defect is live" nor "the defect is gone", and it
// never counts as healed (D12).
type State string

const (
	Holds  State = "holds"
	Absent State = "absent"
	NoData State = "no-data"
)

// Run runs one command in dir, the way the testbed runs every command: with
// its private kubeconfig and nothing inherited.
type Run func(ctx context.Context, argv []string, dir string) (execx.Result, error)

// Evaluator answers oracles.
type Evaluator struct {
	Run  Run
	Prom *prom.Client
	// Timeout bounds each command an oracle runs.
	Timeout time.Duration
}

// Eval answers o for a case whose files are in dir.
func (e *Evaluator) Eval(ctx context.Context, o casefile.Oracle, dir string) (State, error) {
	switch {
	case o.PromQL != nil:
		return e.promql(ctx, o.PromQL)
	case o.Logs != nil:
		return e.logs(ctx, o.Logs, dir)
	case o.Exec != nil:
		return e.exec(ctx, o.Exec, dir)
	case o.Exists != nil:
		return e.exists(ctx, o.Exists, dir)
	case len(o.All) > 0:
		return e.all(ctx, o.All, dir)
	case len(o.Script) > 0:
		return e.script(ctx, o.Script, dir)
	}
	return "", fmt.Errorf("oracle: empty")
}

// promql holds when any series satisfies the comparison. A NaN sample, a
// ratio over no traffic, says nothing; with no usable sample the answer is
// NoData, unless the case declared that no data is the condition itself.
func (e *Evaluator) promql(ctx context.Context, o *casefile.PromQLOracle) (State, error) {
	if e.Prom == nil {
		return "", fmt.Errorf("oracle: no Prometheus on this testbed")
	}
	samples, err := e.Prom.Query(ctx, o.Query)
	if err != nil {
		return "", err
	}
	usable := 0
	for _, s := range samples {
		if math.IsNaN(s.Value) {
			continue
		}
		usable++
		if compare(s.Value, o.Op, o.Value) {
			return Holds, nil
		}
	}
	if usable == 0 {
		if o.NoData == "holds" {
			return Holds, nil
		}
		return NoData, nil
	}
	return Absent, nil
}

func compare(v float64, op string, x float64) bool {
	switch op {
	case ">":
		return v > x
	case ">=":
		return v >= x
	case "<":
		return v < x
	case "<=":
		return v <= x
	}
	return false
}

func (e *Evaluator) logs(ctx context.Context, o *casefile.LogsOracle, dir string) (State, error) {
	re, err := regexp.Compile("(?i)" + o.Pattern)
	if err != nil {
		return "", fmt.Errorf("oracle logs: %w", err)
	}
	argv := []string{"kubectl", "-n", o.Namespace, "logs", o.Workload, "--all-containers=true", "--tail=-1"}
	if o.Since.Duration > 0 {
		argv = append(argv, "--since="+o.Since.Duration.String())
	}
	res, err := e.run(ctx, argv, dir)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("oracle logs: %s", firstLine(res.Stderr))
	}
	if re.MatchString(res.Stdout) {
		return Holds, nil
	}
	return Absent, nil
}

// exec runs a command in a pod. kubectl passes the command's exit code
// through, so a failure of kubectl itself (no such pod) is told apart by its
// "Error from server" message and reported as an error, not as the
// command failing.
func (e *Evaluator) exec(ctx context.Context, o *casefile.ExecOracle, dir string) (State, error) {
	argv := append([]string{"kubectl", "-n", o.Namespace, "exec", o.Pod, "--"}, o.Command...)
	res, err := e.run(ctx, argv, dir)
	if err != nil {
		return "", err
	}
	if strings.Contains(res.Stderr, "Error from server") || strings.Contains(res.Stderr, "error: ") {
		return "", fmt.Errorf("oracle exec: %s", firstLine(res.Stderr))
	}
	succeeded := res.ExitCode == 0 && !res.TimedOut
	if succeeded == (o.HoldsWhen == "succeeds") {
		return Holds, nil
	}
	return Absent, nil
}

func (e *Evaluator) exists(ctx context.Context, o *casefile.ExistsOracle, dir string) (State, error) {
	argv := []string{"kubectl", "get", o.Kind, o.Name, "-o", "name"}
	if o.Namespace != "" {
		argv = append(argv, "-n", o.Namespace)
	}
	res, err := e.run(ctx, argv, dir)
	if err != nil {
		return "", err
	}
	switch {
	case res.ExitCode == 0:
		return Holds, nil
	case strings.Contains(res.Stderr, "NotFound"):
		return Absent, nil
	}
	return "", fmt.Errorf("oracle exists: %s", firstLine(res.Stderr))
}

// all holds when every part holds. One absent part makes it absent; short
// of that, one part without data makes the whole unknown.
func (e *Evaluator) all(ctx context.Context, parts []casefile.Oracle, dir string) (State, error) {
	out := Holds
	for _, p := range parts {
		st, err := e.Eval(ctx, p, dir)
		if err != nil {
			return "", err
		}
		switch st {
		case Absent:
			return Absent, nil
		case NoData:
			out = NoData
		}
	}
	return out, nil
}

// script holds on exit 0 and is absent on exit 1. Anything else is the
// script failing, not an answer.
func (e *Evaluator) script(ctx context.Context, argv []string, dir string) (State, error) {
	res, err := e.run(ctx, argv, dir)
	if err != nil {
		return "", err
	}
	switch {
	case res.TimedOut:
		return "", fmt.Errorf("oracle script: timed out")
	case res.ExitCode == 0:
		return Holds, nil
	case res.ExitCode == 1:
		return Absent, nil
	}
	return "", fmt.Errorf("oracle script: exit %d: %s", res.ExitCode, firstLine(res.Stderr))
}

func (e *Evaluator) run(ctx context.Context, argv []string, dir string) (execx.Result, error) {
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	return e.Run(ctx, argv, dir)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
