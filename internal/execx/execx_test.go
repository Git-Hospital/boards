package execx

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestEnvIsNotInherited(t *testing.T) {
	t.Setenv("KUBECONFIG", "/home/someone/.kube/config")
	res, err := Run(context.Background(), Cmd{Argv: []string{"/usr/bin/env"}, Env: BaseEnv(t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Stdout, "KUBECONFIG") {
		t.Errorf("the caller's KUBECONFIG leaked into the command:\n%s", res.Stdout)
	}
}

func TestExitCodeIsNotAnError(t *testing.T) {
	res, err := Run(context.Background(), Cmd{Argv: []string{"/bin/sh", "-c", "exit 3"}, Env: BaseEnv(t.TempDir())})
	if err != nil || res.ExitCode != 3 {
		t.Errorf("got %d, %v", res.ExitCode, err)
	}
}

func TestTimeoutStopsTheProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	// The child sleeps too; killing only the shell would leave it running
	// and the pipe open.
	res, err := Run(ctx, Cmd{Argv: []string{"/bin/sh", "-c", "sleep 30 & wait"}, Env: BaseEnv(t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Error("not marked as timed out")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %s to stop", took)
	}
}
