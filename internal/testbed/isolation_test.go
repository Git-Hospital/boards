package testbed

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Git-Hospital/boards/internal/execx"
)

// TestIsolationOnLiveCluster is architecture §8's isolation test. It needs
// a running boards cluster, named in BOARDS_TEST_CLUSTER (for example one
// left by `boards run --keep`), and is skipped otherwise. Whatever the
// caller's kubeconfig says, a command in the testbed's environment must see
// that cluster and nothing else.
func TestIsolationOnLiveCluster(t *testing.T) {
	name := os.Getenv("BOARDS_TEST_CLUSTER")
	if name == "" {
		t.Skip("BOARDS_TEST_CLUSTER is not set")
	}
	l, err := LoadLock("../../testbed/testbed.lock")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	k, err := StartKind(ctx, KindOptions{Lock: l, Name: name, Reuse: true, Dir: t.TempDir(), Log: t.Logf})
	if k != nil {
		defer k.Close(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}

	run := func(argv ...string) string {
		t.Helper()
		res, err := execx.Run(ctx, execx.Cmd{Argv: argv, Env: k.Env()})
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("%v: %v %s", argv, err, res.Stderr)
		}
		return strings.TrimSpace(res.Stdout)
	}
	if got := run("kubectl", "config", "get-contexts", "-o", "name"); got != "kind-"+name {
		t.Errorf("kubectl sees contexts %q, want only kind-%s", got, name)
	}
	if got := run("kubectl", "-n", markerNamespace, "get", "configmap", markerName, "-o", "name"); got != "configmap/"+markerName {
		t.Errorf("the cluster kubectl reaches has no boards marker: %q", got)
	}
	if got := run("helm", "list", "-A", "-q"); got != "" {
		t.Errorf("helm sees releases %q; the mesh testbed installs none", got)
	}
	if got := run("helm", "env", "HELM_KUBECONTEXT"); got != "" {
		t.Errorf("helm has a context set: %q", got)
	}
}
