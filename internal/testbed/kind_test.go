package testbed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryLockIsValid(t *testing.T) {
	l, err := LoadLock("../../testbed/testbed.lock")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gatewayAPI", "prometheus", "kubeStateMetrics", "payments", "ledger", "ingress", "sidecarOrders"} {
		if _, err := l.File(name); err != nil {
			t.Error(err)
		}
	}
	if !strings.Contains(l.Kind.NodeImage, "@sha256:") {
		t.Errorf("node image %q is not pinned by digest", l.Kind.NodeImage)
	}
}

func TestLockRejectsAChangedManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.yaml"), []byte("kind: Namespace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := `schemaVersion: boards.testbed/v1
kind: {version: v0.31.0, nodeImage: "kindest/node:v1.35.0@sha256:00"}
istio: {version: 1.24.1, profile: ambient}
manifests:
  app: {path: app.yaml, sha256: "0000"}
`
	path := filepath.Join(dir, "testbed.lock")
	if err := os.WriteFile(path, []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLock(path); err == nil || !strings.Contains(err.Error(), "update the lock") {
		t.Errorf("got %v", err)
	}
}

// A cluster boards did not name cannot be used, reused or not, and nothing
// is run before that check.
func TestKindRefusesForeignClusterNames(t *testing.T) {
	l, err := LoadLock("../../testbed/testbed.lock")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kind-meshmedic-demo", "prod", ""} {
		_, err := StartKind(context.Background(), KindOptions{Lock: l, Name: name, Reuse: true, Dir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), ClusterPrefix) {
			t.Errorf("%q: %v", name, err)
		}
	}
}

func TestHostEnvDropsClusterSelection(t *testing.T) {
	t.Setenv("KUBECONFIG", "/home/me/.kube/config")
	t.Setenv("HELM_KUBECONTEXT", "prod")
	t.Setenv("HELM_NAMESPACE", "prod")
	for _, kv := range hostEnv() {
		if strings.HasPrefix(kv, "KUBECONFIG=") || strings.HasPrefix(kv, "HELM_") {
			t.Errorf("%s leaked into kind's environment", kv)
		}
	}
}

func TestClusterEnvIsPrivate(t *testing.T) {
	t.Setenv("KUBECONFIG", "/home/me/.kube/config")
	k := &Kind{kubeconfig: "/private/kc", home: "/private/home"}
	env := strings.Join(k.clusterEnv(), "\n")
	if !strings.Contains(env, "KUBECONFIG=/private/kc") || !strings.Contains(env, "HOME=/private/home") || strings.Contains(env, "/home/me") {
		t.Errorf("cluster env:\n%s", env)
	}
}

func TestReadMarker(t *testing.T) {
	for name, tc := range map[string]struct {
		js   string
		ok   bool
		lock string
	}{
		"boards":        {`{"metadata":{"labels":{"app.kubernetes.io/managed-by":"boards"}},"data":{"lock":"abc"}}`, true, "abc"},
		"no label":      {`{"metadata":{},"data":{"lock":"abc"}}`, false, ""},
		"another owner": {`{"metadata":{"labels":{"app.kubernetes.io/managed-by":"helm"}},"data":{"lock":"abc"}}`, false, ""},
		"no lock":       {`{"metadata":{"labels":{"app.kubernetes.io/managed-by":"boards"}}}`, false, ""},
		"not json":      {`Error from server (NotFound)`, false, ""},
	} {
		lock, ok := readMarker(tc.js)
		if ok != tc.ok || lock != tc.lock {
			t.Errorf("%s: %q %v", name, lock, ok)
		}
	}
}
