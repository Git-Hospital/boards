package meshmedic_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/boards/internal/adapter"
	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/execx"
	"github.com/Git-Hospital/boards/internal/verdict"
)

// fakeMeshmedic stands in for `meshmedic`: version prints a version, watch
// appends the scripted events to its --events file and waits to be stopped.
const fakeMeshmedic = `#!/bin/sh
if [ "$1" = version ]; then echo "meshmedic v9.9"; exit 0; fi
while [ $# -gt 0 ]; do
  case "$1" in --events) out="$2"; shift ;; --target) echo "target $2" ;; esac
  shift
done
cat "$FAKE_EVENTS" >> "$out"
trap 'exit 0' TERM
while :; do sleep 1; done
`

const events = `{"time":"2026-10-23T12:00:00Z","type":"incident","scenario":"error-surge-outlier-ejection","params":{"namespace":"demo","service":"payments"},"culprits":[{"kind":"Deployment","namespace":"demo","name":"payments-v2","evidence":"errors-by-workload"}],"action":"enable-outlier-detection","patch":"kind: DestinationRule\n"}
{"time":"2026-10-23T12:00:30Z","type":"incident","scenario":"latency-regression-vs-baseline","params":{"namespace":"demo","service":"payments"},"culprits":[],"action":"report-only"}
{"time":"2026-10-23T12:01:00Z","type":"incident","scenario":"connection-pool-overflow","params":{"namespace":"demo","service":"payments"},"culprits":[{"kind":"Deployment","namespace":"demo","name":"payments-v2","evidence":"x"}],"action":"raise-connection-pool-limits","patch":"kind: DestinationRule\nmetadata: {name: other}\n"}
{"time":"2026-10-23T12:05:00Z","type":"resolved","scenario":"error-surge-outlier-ejection","params":{"service":"payments","namespace":"demo"}}
{"time":"2026-10-23T12:06:00Z","type":"resolved","scenario":"latency-regression-vs-baseline","params":{"namespace":"demo","service":"payments"}}
{"time":"2026-10-23T12:07:00Z","type":"resolved","scenario":"connection-pool-overflow","params":{"namespace":"demo","service":"payments"}}
`

func TestAdapterTurnsEventsIntoAVerdict(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "meshmedic"), []byte(fakeMeshmedic), 0o755); err != nil {
		t.Fatal(err)
	}
	evFile := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(evFile, []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := adapter.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	a.Secrets = append(a.Secrets, "FAKE_EVENTS")
	t.Setenv("FAKE_EVENTS", evFile)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	work := t.TempDir()
	c := &casefile.Case{ID: "mesh/error-surge", Dir: "/nowhere", Target: casefile.Target{Namespace: "demo", Service: "payments"}}
	env := append(execx.BaseEnv(t.TempDir()), "BOARDS_PROMETHEUS=http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := a.Run(ctx, adapter.Invocation{Case: c, Twin: casefile.TwinDefect, Work: work, Window: time.Minute, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(work, "raw.txt"))
	if !out.Stopped || out.ExitCode != 0 {
		t.Fatalf("outcome %+v\n%s", out, raw)
	}
	if !strings.Contains(string(raw), "target namespace=demo,service=payments") {
		t.Errorf("meshmedic was not pointed at the target:\n%s", raw)
	}

	v, err := verdict.Read(filepath.Join(work, verdict.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if v.Tool.Version != "v9.9" || !v.Alarmed {
		t.Errorf("tool %+v alarmed %v", v.Tool, v.Alarmed)
	}
	// Three alarms; one resolve, when the last open incident cleared.
	var kinds []string
	for _, e := range v.Events {
		kinds = append(kinds, e.Type+"@"+e.At.Format("15:04:05"))
	}
	if got := strings.Join(kinds, " "); got != "alarm@12:00:00 alarm@12:00:30 alarm@12:01:00 resolve@12:07:00" {
		t.Errorf("events %s", got)
	}
	if len(v.Culprits) != 1 || v.Culprits[0].Name != "payments-v2" {
		t.Errorf("culprits %+v, want payments-v2 once", v.Culprits)
	}
	if v.Patch == nil || v.Patch.Class != "outlier-ejection" || v.Patch.Content != "kind: DestinationRule\n" {
		t.Errorf("patch %+v, want the first proposal as outlier-ejection", v.Patch)
	}
}
