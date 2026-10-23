package testbed

import (
	"context"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const twoObjects = `apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata: {name: payments, namespace: demo}
spec: {host: payments}
---
# an empty document is dropped
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata: {name: payments-shift, namespace: demo}
spec: {hosts: [payments]}
`

func TestSplitDocs(t *testing.T) {
	docs, err := splitDocs(twoObjects)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || !strings.Contains(docs[1], "payments-shift") {
		t.Fatalf("%d docs: %q", len(docs), docs)
	}
	for _, bad := range []string{"", "kind: X\n", "apiVersion: v1\nkind: ConfigMap\nmetadata: {}\n", ": : :"} {
		if _, err := splitDocs(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestRestorableDropsServerFields(t *testing.T) {
	out, err := restorable(`apiVersion: v1
kind: ConfigMap
metadata:
  name: x
  namespace: demo
  resourceVersion: "42"
  uid: abc
  managedFields: [{}]
  labels: {keep: me}
data: {a: b}
status: {}
`)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	meta := m["metadata"].(map[string]any)
	for _, k := range []string{"resourceVersion", "uid", "managedFields"} {
		if _, ok := meta[k]; ok {
			t.Errorf("%s kept", k)
		}
	}
	if _, ok := m["status"]; ok {
		t.Error("status kept")
	}
	if meta["labels"] == nil || m["data"] == nil {
		t.Error("lost the object's own fields")
	}
}

// fakeCluster records kubectl calls. The DestinationRule exists, the
// VirtualService does not.
type fakeCluster struct{ calls []string }

func (f *fakeCluster) kubectl(_ context.Context, stdin string, args ...string) (string, string, int, error) {
	verb := args[0]
	obj := "dr"
	if strings.Contains(stdin, "VirtualService") {
		obj = "vs"
	}
	f.calls = append(f.calls, verb+" "+obj)
	if verb == "get" {
		if obj == "vs" {
			return "", `Error from server (NotFound): virtualservices "payments-shift" not found`, 1, nil
		}
		// kubectl get -f prints a List even for one document.
		return "apiVersion: v1\nkind: List\nmetadata: {resourceVersion: ''}\nitems:\n- apiVersion: networking.istio.io/v1\n  kind: DestinationRule\n  metadata: {name: payments, namespace: demo, resourceVersion: '7'}\n  spec: {host: payments, trafficPolicy: {}}\n", "", 0, nil
	}
	return "", "", 0, nil
}

func TestApplyThenRemoveRestoresWhatWasThere(t *testing.T) {
	f := &fakeCluster{}
	saved, err := applyPatch(context.Background(), f.kubectl, twoObjects)
	if err != nil {
		t.Fatal(err)
	}
	if err := removePatch(context.Background(), f.kubectl, saved); err != nil {
		t.Fatal(err)
	}
	want := "get dr,get vs,apply dr,apply vs,delete vs,replace dr"
	if got := strings.Join(f.calls, ","); got != want {
		t.Errorf("calls\n got %s\nwant %s", got, want)
	}
	if strings.Contains(saved[0].before, "resourceVersion") || !strings.Contains(saved[0].before, "kind: DestinationRule") {
		t.Errorf("the saved object is not the bare DestinationRule:\n%s", saved[0].before)
	}
}
