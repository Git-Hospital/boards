package oracle

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/execx"
	"github.com/Git-Hospital/boards/internal/prom"
)

// promServer answers every query with the given result JSON.
func promServer(t *testing.T, result string) *prom.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":%s}}`, result)
	}))
	t.Cleanup(srv.Close)
	return prom.New(srv.URL)
}

func sample(v string) string { return `[{"metric":{},"value":[1,"` + v + `"]}]` }

func TestPromQL(t *testing.T) {
	q := func(noData string) casefile.Oracle {
		return casefile.Oracle{PromQL: &casefile.PromQLOracle{Query: "x", Op: ">", Value: 0.15, NoData: noData}}
	}
	for name, tc := range map[string]struct {
		result, noData string
		want           State
	}{
		"above":               {sample("0.9"), "", Holds},
		"below":               {sample("0.01"), "", Absent},
		"empty":               {`[]`, "", NoData},
		"NaN is no traffic":   {sample("NaN"), "", NoData},
		"empty declared live": {`[]`, "holds", Holds},
		"any series":          {`[{"metric":{"a":"1"},"value":[1,"0.01"]},{"metric":{"a":"2"},"value":[1,"0.5"]}]`, "", Holds},
	} {
		e := &Evaluator{Prom: promServer(t, tc.result)}
		got, err := e.Eval(context.Background(), q(tc.noData), "")
		if err != nil || got != tc.want {
			t.Errorf("%s: %v %v, want %v", name, got, err, tc.want)
		}
	}
}

// fakeRun answers kubectl commands from a table keyed by the command line.
func fakeRun(answers map[string]execx.Result) Run {
	return func(_ context.Context, argv []string, _ string) (execx.Result, error) {
		line := strings.Join(argv, " ")
		for prefix, r := range answers {
			if strings.HasPrefix(line, prefix) {
				return r, nil
			}
		}
		return execx.Result{}, fmt.Errorf("unexpected command %q", line)
	}
}

func TestExists(t *testing.T) {
	o := casefile.Oracle{Exists: &casefile.ExistsOracle{Kind: "pod", Namespace: "default", Name: "plain-client"}}
	for name, tc := range map[string]struct {
		res     execx.Result
		want    State
		wantErr bool
	}{
		"found":     {execx.Result{Stdout: "pod/plain-client"}, Holds, false},
		"not found": {execx.Result{ExitCode: 1, Stderr: `Error from server (NotFound): pods "plain-client" not found`}, Absent, false},
		"forbidden": {execx.Result{ExitCode: 1, Stderr: "Error from server (Forbidden)"}, "", true},
	} {
		e := &Evaluator{Run: fakeRun(map[string]execx.Result{"kubectl get pod plain-client": tc.res})}
		got, err := e.Eval(context.Background(), o, "")
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("%s: %v %v", name, got, err)
		}
	}
}

func TestExec(t *testing.T) {
	o := casefile.Oracle{Exec: &casefile.ExecOracle{Namespace: "default", Pod: "c", Command: []string{"curl", "x"}, HoldsWhen: "fails"}}
	for name, tc := range map[string]struct {
		res     execx.Result
		want    State
		wantErr bool
	}{
		"command failed":        {execx.Result{ExitCode: 56, Stderr: "command terminated with exit code 56"}, Holds, false},
		"command failed loudly": {execx.Result{ExitCode: 7, Stderr: "error: connection refused\ncommand terminated with exit code 7"}, Holds, false},
		"command succeeded":     {execx.Result{}, Absent, false},
		"kubectl failed":        {execx.Result{ExitCode: 1, Stderr: `Error from server (NotFound): pods "c" not found`}, "", true},
		"kubectl timed out":     {execx.Result{TimedOut: true}, "", true},
	} {
		e := &Evaluator{Run: fakeRun(map[string]execx.Result{"kubectl -n default exec c -- curl x": tc.res})}
		got, err := e.Eval(context.Background(), o, "")
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("%s: %v %v, want %v", name, got, err, tc.want)
		}
	}
}

func TestLogsAndAll(t *testing.T) {
	logs := casefile.Oracle{Logs: &casefile.LogsOracle{Namespace: "demo", Workload: "deploy/loadgen", Pattern: "could not resolve"}}
	e := &Evaluator{
		Run:  fakeRun(map[string]execx.Result{"kubectl -n demo logs deploy/loadgen": {Stdout: "curl: (6) Could not resolve host: paymnts"}}),
		Prom: promServer(t, `[]`),
	}
	if got, err := e.Eval(context.Background(), logs, ""); err != nil || got != Holds {
		t.Errorf("logs: %v %v", got, err)
	}
	both := casefile.Oracle{All: []casefile.Oracle{logs, {PromQL: &casefile.PromQLOracle{Query: "x", Op: ">", Value: 0}}}}
	if got, err := e.Eval(context.Background(), both, ""); err != nil || got != NoData {
		t.Errorf("all with an empty query: %v %v, want no-data", got, err)
	}
}

func TestScript(t *testing.T) {
	o := casefile.Oracle{Script: []string{"./check.sh"}}
	for code, want := range map[int]State{0: Holds, 1: Absent} {
		e := &Evaluator{Run: fakeRun(map[string]execx.Result{"./check.sh": {ExitCode: code}})}
		if got, err := e.Eval(context.Background(), o, ""); err != nil || got != want {
			t.Errorf("exit %d: %v %v", code, got, err)
		}
	}
	e := &Evaluator{Run: fakeRun(map[string]execx.Result{"./check.sh": {ExitCode: 2, Stderr: "boom"}})}
	if _, err := e.Eval(context.Background(), o, ""); err == nil {
		t.Error("exit 2 is the script failing, not an answer")
	}
}
