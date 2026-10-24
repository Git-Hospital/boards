package testbed

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// objects reads every object in a YAML file, keyed by kind/namespace/name.
func objects(t *testing.T, path string) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := splitDocs(string(raw))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	out := map[string]map[string]any{}
	for _, d := range docs {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(d), &m); err != nil {
			t.Fatal(err)
		}
		meta := m["metadata"].(map[string]any)
		ns, _ := meta["namespace"].(string)
		out[m["kind"].(string)+"/"+ns+"/"+meta["name"].(string)] = m
	}
	return out
}

// Reference fixes that restore an object of the testbed must restore it as
// the testbed ships it, or a live run would prove a different fix. The
// lock's hashes cover the app manifests, not the cases' copies.
func TestReferenceFixesMatchTheTestbed(t *testing.T) {
	app := objects(t, "../../testbed/mesh/app/payments.yaml")
	cases := "../../cases/mesh"
	for file, key := range map[string]string{
		"client-dns-typo/fix-client-fix.yaml":        "Deployment/demo/loadgen",
		"client-wrong-port/fix-client-fix.yaml":      "Deployment/demo/loadgen",
		"client-wrong-scheme/fix-client-fix.yaml":    "Deployment/demo/loadgen",
		"fault-injection-left/fix-remove-fault.yaml": "VirtualService/demo/payments",
	} {
		fix := objects(t, filepath.Join(cases, file))
		if !reflect.DeepEqual(fix[key], app[key]) {
			t.Errorf("%s: %s differs from testbed/mesh/app/payments.yaml", file, key)
		}
	}
	a := objects(t, filepath.Join(cases, "error-surge/fix-traffic-shift.yaml"))
	b := objects(t, filepath.Join(cases, "canary-latency/fix-traffic-shift.yaml"))
	if !reflect.DeepEqual(a, b) {
		t.Error("the two traffic-shift fixes differ")
	}
}
