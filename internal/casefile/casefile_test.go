package casefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryCasesAreValid(t *testing.T) {
	cases, err := LoadDir("../../cases")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no cases found")
	}
	for _, c := range cases {
		if err := Validate(c); err != nil {
			t.Error(err)
		}
	}
}

const valid = `id: mesh/sample
track: mesh
kind: fault
set: open
title: sample
provenance: {kind: synthetic}
author: test
inject: [{run: [kubectl, -n, demo, set, env, deploy/x, A=1]}]
reset: [{run: [kubectl, -n, demo, set, env, deploy/x, A-]}]
cleanTwin:
  checks: [{promql: {query: up, op: ">", value: 0}}]
oracle: {promql: {query: up, op: ">", value: 0}}
window: 5m
expect:
  alarmWithin: 5m
  resolvesWithin: 5m
  culprits: [{level: 2, all: [{kind: Deployment, name: x}]}]
  remediation: {accept: [rollback], noPatch: fail, healsWithin: 5m}
`

func write(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sample")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidSample(t *testing.T) {
	c, err := Load(write(t, valid))
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaRejects(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field":    valid + "colour: red\n",
		"two oracle kinds": strings.Replace(valid, `oracle: {promql: {query: up, op: ">", value: 0}}`, `oracle: {promql: {query: up, op: ">", value: 0}, exists: {kind: pod, name: x}}`, 1),
		"bad duration":     strings.Replace(valid, "window: 5m", "window: five minutes", 1),
		"bad track":        strings.Replace(valid, "track: mesh", "track: network", 1),
		"culprit level 3":  strings.Replace(valid, "level: 2", "level: 3", 1),
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: loaded without error", name)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"context flag": {
			strings.Replace(valid, "[kubectl, -n, demo, set, env, deploy/x, A=1]", "[kubectl, --context=prod, -n, demo, set, env, deploy/x, A=1]", 1),
			"selects a cluster",
		},
		"kubeconfig in env": {
			strings.Replace(valid, "[kubectl, -n, demo, set, env, deploy/x, A=1]", "[env, KUBECONFIG=/home/me/.kube/config, kubectl, get, ns]", 1),
			"selects a cluster",
		},
		"helm kube-context": {
			strings.Replace(valid, "[kubectl, -n, demo, set, env, deploy/x, A=1]", "[helm, upgrade, x, ./chart, --kube-context, prod]", 1),
			"selects a cluster",
		},
		"kubectl cluster": {
			strings.Replace(valid, "[kubectl, -n, demo, set, env, deploy/x, A=1]", "[kubectl, --cluster=prod, apply, -f, x.yaml]", 1),
			"selects a cluster",
		},
		"target as culprit": {
			strings.Replace(strings.Replace(valid, "title: sample", "title: sample\ntarget: {namespace: demo, service: x}", 1), "{kind: Deployment, name: x}", "{kind: Service, namespace: demo, name: x}", 1),
			"is the target every tool is given",
		},
		"unwinnable window": {
			strings.Replace(valid, "window: 5m", "window: 1m", 1),
			"no tool could pass",
		},
		"exam without a source": {
			strings.Replace(valid, "set: open", "set: exam", 1),
			"real, public source",
		},
		"no full-marks culprit": {
			strings.Replace(valid, "level: 2", "level: 1", 1),
			"level 2",
		},
		"remediation without clean checks": {
			strings.Replace(valid, "cleanTwin:\n  checks: [{promql: {query: up, op: \">\", value: 0}}]\n", "cleanTwin: {}\n", 1),
			"cleanTwin.checks are required",
		},
		"accepted and forbidden": {
			strings.Replace(valid, "accept: [rollback]", "accept: [rollback], forbid: [rollback]", 1),
			"both accepted and forbidden",
		},
	} {
		c, err := Load(write(t, tc.body))
		if err != nil {
			t.Errorf("%s: load: %v", name, err)
			continue
		}
		err = Validate(c)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}

func TestExamCaseWithSourcePasses(t *testing.T) {
	body := strings.Replace(valid, "set: open", "set: exam", 1)
	body = strings.Replace(body, "provenance: {kind: synthetic}", "provenance: {kind: issue, url: 'https://github.com/istio/istio/issues/1'}", 1)
	c, err := Load(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestSelectRejectsUnknownID(t *testing.T) {
	cases := []*Case{{ID: "mesh/a", Track: "mesh"}}
	if _, err := Select(cases, "", []string{"mesh/b"}); err == nil {
		t.Error("an unknown id selected nothing without an error")
	}
}
