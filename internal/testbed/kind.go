package testbed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/execx"
	"github.com/Git-Hospital/boards/internal/oracle"
	"github.com/Git-Hospital/boards/internal/prom"
	"github.com/Git-Hospital/boards/internal/verdict"
)

// ClusterPrefix starts the name of every cluster boards creates. --reuse
// accepts nothing else (D17).
const ClusterPrefix = "boards-"

// markerConfigMap is written into every cluster boards creates, so that
// --reuse can tell one from a cluster that merely has a matching name.
const (
	markerNamespace = "kube-system"
	markerName      = "boards-testbed"
	markerLabel     = "app.kubernetes.io/managed-by=boards"
)

// KindOptions configure the mesh testbed.
type KindOptions struct {
	Lock *Lock
	// Name is the cluster name. It must start with ClusterPrefix.
	Name string
	// Reuse takes an existing boards cluster instead of creating one. The
	// run is then not standard: nobody can vouch for what happened to the
	// cluster since it was built.
	Reuse bool
	// Keep leaves the cluster running after Close.
	Keep bool
	// Dir is private to this run: the kubeconfig and the scratch HOME go there.
	Dir string
	Log func(format string, a ...any)
}

// Kind is the mesh testbed: a kind cluster with Istio ambient, Prometheus
// and the payments/ledger app, built from testbed.lock.
type Kind struct {
	opts       KindOptions
	kubeconfig string
	home       string
	promURL    string
	eval       *oracle.Evaluator
	notes      []string
	created    bool
	stopPF     func()

	mu      sync.Mutex
	patches map[*verdict.Patch][]saved
}

// StartKind creates (or, with Reuse, attaches to) the cluster and brings
// the testbed up. On error the caller still calls Close, which deletes a
// half-built cluster.
func StartKind(ctx context.Context, opts KindOptions) (*Kind, error) {
	if opts.Log == nil {
		opts.Log = func(string, ...any) {}
	}
	if !strings.HasPrefix(opts.Name, ClusterPrefix) {
		return nil, fmt.Errorf("testbed: cluster name %q must start with %q", opts.Name, ClusterPrefix)
	}
	k := &Kind{
		opts:       opts,
		kubeconfig: filepath.Join(opts.Dir, "kubeconfig"),
		home:       filepath.Join(opts.Dir, "home"),
		patches:    map[*verdict.Patch][]saved{},
	}
	if err := os.MkdirAll(k.home, 0o700); err != nil {
		return k, err
	}
	k.checkVersions(ctx)

	if opts.Reuse {
		if err := k.attach(ctx); err != nil {
			return k, err
		}
		k.notes = append(k.notes, "reused cluster "+opts.Name)
	} else {
		if err := k.create(ctx); err != nil {
			return k, err
		}
		if err := k.provision(ctx); err != nil {
			return k, err
		}
	}
	if err := k.startPrometheus(ctx); err != nil {
		return k, err
	}
	return k, nil
}

// checkVersions compares the tools on PATH with the lock. A mismatch does
// not stop the run; it makes it not standard, and says why.
func (k *Kind) checkVersions(ctx context.Context) {
	if out, err := k.host(ctx, time.Minute, "kind", "version"); err == nil {
		if !strings.Contains(out, k.opts.Lock.Kind.Version+" ") {
			k.notes = append(k.notes, fmt.Sprintf("kind is %q, the lock says %s", strings.TrimSpace(out), k.opts.Lock.Kind.Version))
		}
	}
	out, err := k.cluster(ctx, time.Minute, "", "istioctl", "version", "--remote=false", "--short")
	got := strings.TrimPrefix(strings.TrimSpace(out), "client version: ")
	if err != nil || got != k.opts.Lock.Istio.Version {
		k.notes = append(k.notes, fmt.Sprintf("istioctl is %q, the lock says %s", got, k.opts.Lock.Istio.Version))
	}
}

func (k *Kind) create(ctx context.Context) error {
	clusters, err := k.host(ctx, time.Minute, "kind", "get", "clusters")
	if err != nil {
		return err
	}
	if slices.Contains(strings.Fields(clusters), k.opts.Name) {
		return fmt.Errorf("testbed: a cluster named %s already exists; pass --reuse to use it, or delete it", k.opts.Name)
	}
	k.opts.Log("testbed: creating kind cluster %s", k.opts.Name)
	k.created = true
	if _, err := k.host(ctx, 10*time.Minute, "kind", "create", "cluster",
		"--name", k.opts.Name, "--image", k.opts.Lock.Kind.NodeImage,
		"--kubeconfig", k.kubeconfig, "--wait", "180s"); err != nil {
		return err
	}
	_, err = k.cluster(ctx, time.Minute, "", "sh", "-c",
		fmt.Sprintf("kubectl -n %s create configmap %s --from-literal=lock=%s && kubectl -n %s label configmap %s %s",
			markerNamespace, markerName, k.opts.Lock.Digest, markerNamespace, markerName, markerLabel))
	return err
}

// attach takes an existing cluster only if boards built it.
func (k *Kind) attach(ctx context.Context) error {
	clusters, err := k.host(ctx, time.Minute, "kind", "get", "clusters")
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Fields(clusters), k.opts.Name) {
		return fmt.Errorf("testbed: no kind cluster named %s to reuse", k.opts.Name)
	}
	if _, err := k.host(ctx, time.Minute, "kind", "export", "kubeconfig", "--name", k.opts.Name, "--kubeconfig", k.kubeconfig); err != nil {
		return err
	}
	out, err := k.cluster(ctx, time.Minute, "", "kubectl", "-n", markerNamespace, "get", "configmap", markerName, "-o", "json")
	lock, ok := readMarker(out)
	if err != nil || !ok {
		return fmt.Errorf("testbed: cluster %s has no %s/%s labelled %s, so boards did not build it; refusing to write to it", k.opts.Name, markerNamespace, markerName, markerLabel)
	}
	if lock != k.opts.Lock.Digest {
		k.notes = append(k.notes, "the reused cluster was built from a different testbed.lock")
	}
	return nil
}

// readMarker reads the marker ConfigMap as `kubectl get -o json` prints it.
// It counts only with boards' label: a ConfigMap of the same name that
// someone else made does not make a cluster boards'.
func readMarker(js string) (lock string, ok bool) {
	var cm struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Data map[string]string `json:"data"`
	}
	if json.Unmarshal([]byte(js), &cm) != nil {
		return "", false
	}
	name, value, _ := strings.Cut(markerLabel, "=")
	if cm.Metadata.Labels[name] != value || cm.Data["lock"] == "" {
		return "", false
	}
	return cm.Data["lock"], true
}

// provision installs the mesh testbed, in the order meshmedic's demo
// scripts used (D19).
func (k *Kind) provision(ctx context.Context) error {
	file := func(name string) string {
		p, err := k.opts.Lock.File(name)
		if err != nil {
			panic(err) // LoadLock checked the files; a missing name is a bug here
		}
		return p
	}
	steps := []struct {
		desc    string
		timeout time.Duration
		argv    []string
	}{
		// Ambient waypoints are Gateway API resources; kind ships no CRDs.
		// Server-side apply, because the CRDs are too large for the
		// last-applied annotation.
		{"Gateway API CRDs", 2 * time.Minute, []string{"kubectl", "apply", "--server-side", "-f", file("gatewayAPI")}},
		{"Istio " + k.opts.Lock.Istio.Profile, 10 * time.Minute, []string{"istioctl", "install", "--set", "profile=" + k.opts.Lock.Istio.Profile, "--skip-confirmation"}},
		{"Prometheus", 2 * time.Minute, []string{"kubectl", "apply", "-f", file("prometheus")}},
		{"", 5 * time.Minute, []string{"kubectl", "-n", "istio-system", "rollout", "status", "deploy/prometheus", "--timeout=300s"}},
		{"kube-state-metrics", 2 * time.Minute, []string{"kubectl", "apply", "-f", file("kubeStateMetrics")}},
		{"", 5 * time.Minute, []string{"kubectl", "-n", "istio-system", "rollout", "status", "deploy/kube-state-metrics", "--timeout=300s"}},
		{"payments and ledger", 2 * time.Minute, []string{"kubectl", "apply", "-f", file("payments"), "-f", file("ledger")}},
		{"", 6 * time.Minute, []string{"kubectl", "-n", "demo", "rollout", "status", "deploy/ledger", "deploy/payments-v1", "deploy/payments-v2", "deploy/loadgen", "--timeout=300s"}},
		{"waypoint", 5 * time.Minute, []string{"istioctl", "waypoint", "apply", "-n", "demo", "--enroll-namespace", "--wait"}},
		{"ingress", 2 * time.Minute, []string{"kubectl", "apply", "-f", file("ingress")}},
		{"", 6 * time.Minute, []string{"kubectl", "-n", "demo", "rollout", "status", "deploy/ingress-loadgen", "--timeout=300s"}},
		{"sidecar orders", 2 * time.Minute, []string{"kubectl", "apply", "-f", file("sidecarOrders")}},
		{"", 6 * time.Minute, []string{"kubectl", "-n", "demo-sidecar", "rollout", "status", "deploy/orders-v1", "deploy/orders-v2", "deploy/loadgen", "--timeout=300s"}},
	}
	for _, s := range steps {
		if s.desc != "" {
			k.opts.Log("testbed: installing %s", s.desc)
		}
		if _, err := k.cluster(ctx, s.timeout, "", s.argv...); err != nil {
			return fmt.Errorf("testbed: %s: %w", strings.Join(s.argv[:2], " "), err)
		}
	}
	return nil
}

// startPrometheus forwards a local port to the in-cluster Prometheus, keeps
// the forward alive (forwards drop), and waits until Prometheus sees mesh
// traffic in the demo namespace: a forward that answers but points at an
// empty Prometheus is the failure the old harness learned to check for.
func (k *Kind) startPrometheus(ctx context.Context) error {
	port, err := freePort()
	if err != nil {
		return err
	}
	k.promURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	pfCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	k.stopPF = func() { cancel(); <-done }
	go func() {
		defer close(done)
		for pfCtx.Err() == nil {
			cmd := exec.CommandContext(pfCtx, "kubectl", "-n", "istio-system", "port-forward", "svc/prometheus", fmt.Sprintf("%d:9090", port))
			cmd.Env = k.clusterEnv()
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			_ = cmd.Run()
			select {
			case <-pfCtx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}()

	k.eval = &oracle.Evaluator{Run: k.oracleRun, Prom: prom.New(k.promURL), Timeout: time.Minute}
	k.opts.Log("testbed: waiting for Prometheus to see mesh traffic")
	ready := casefile.Oracle{PromQL: &casefile.PromQLOracle{
		Query: `sum(rate(istio_requests_total{reporter="waypoint", destination_service_namespace="demo"}[1m]))`,
		Op:    ">", Value: 0,
	}}
	deadline := time.Now().Add(10 * time.Minute)
	for {
		st, err := k.eval.Eval(ctx, ready, k.opts.Dir)
		if err == nil && st == Holds {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("testbed: Prometheus at %s saw no mesh traffic in demo after 10m (last: %v %v)", k.promURL, st, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Info implements Testbed.
func (k *Kind) Info() Info {
	return Info{Name: "kind-mesh", Standard: len(k.notes) == 0, LockDigest: k.opts.Lock.Digest, Notes: k.notes}
}

// Env implements Testbed: what adapters and case steps get.
func (k *Kind) Env() []string {
	return append(k.clusterEnv(), "BOARDS_PROMETHEUS_URL="+k.promURL)
}

// clusterEnv points kubectl, istioctl and helm at the private kubeconfig
// and a scratch HOME, and inherits nothing else but PATH (architecture §7).
func (k *Kind) clusterEnv() []string {
	return append(execx.BaseEnv(k.home), "KUBECONFIG="+k.kubeconfig)
}

// hostEnv is for kind itself, which needs the user's Docker setup (found
// through HOME). The user's kubeconfig and Helm settings are dropped, and
// kind is always given --kubeconfig, so it never edits the user's own.
func hostEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "KUBECONFIG" || name == "HELM_NAMESPACE" ||
			strings.HasPrefix(name, "HELM_KUBE") || strings.HasPrefix(name, "HELM_DRIVER") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func (k *Kind) host(ctx context.Context, timeout time.Duration, argv ...string) (string, error) {
	return k.do(ctx, timeout, hostEnv(), "", "", argv)
}

func (k *Kind) cluster(ctx context.Context, timeout time.Duration, dir string, argv ...string) (string, error) {
	return k.do(ctx, timeout, k.clusterEnv(), dir, "", argv)
}

func (k *Kind) do(ctx context.Context, timeout time.Duration, env []string, dir, stdin string, argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := execx.Run(ctx, execx.Cmd{Argv: argv, Env: env, Dir: dir, Stdin: stdin})
	if err != nil {
		return "", fmt.Errorf("%s: %w", argv[0], err)
	}
	if res.TimedOut {
		return res.Stdout, fmt.Errorf("%s: timed out after %s", strings.Join(argv, " "), timeout)
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("%s: exit %d: %s", strings.Join(argv, " "), res.ExitCode, tail(res.Stderr))
	}
	return res.Stdout, nil
}

func (k *Kind) oracleRun(ctx context.Context, argv []string, dir string) (execx.Result, error) {
	return execx.Run(ctx, execx.Cmd{Argv: argv, Env: k.clusterEnv(), Dir: dir})
}

func (k *Kind) kubectlStdin(ctx context.Context, stdin string, args ...string) (string, string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res, err := execx.Run(ctx, execx.Cmd{Argv: append([]string{"kubectl"}, args...), Env: k.clusterEnv(), Stdin: stdin})
	if err == nil && res.TimedOut {
		err = errors.New("kubectl timed out")
	}
	return res.Stdout, res.Stderr, res.ExitCode, err
}

// tail keeps the end of a long stderr, where the reason usually is.
func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		s = "..." + s[len(s)-600:]
	}
	return s
}

// Quiesce implements Testbed.
func (k *Kind) Quiesce(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Inject implements Testbed.
func (k *Kind) Inject(ctx context.Context, c *casefile.Case, twin casefile.Twin) error {
	inject, _ := c.Steps(twin)
	return k.steps(ctx, c, inject)
}

// Reset implements Testbed. Every step runs even if one fails, so that a
// partial reset leaves as little behind as it can.
func (k *Kind) Reset(ctx context.Context, c *casefile.Case, twin casefile.Twin) error {
	_, reset := c.Steps(twin)
	var errs []error
	for _, s := range reset {
		if err := k.step(ctx, c, s); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (k *Kind) steps(ctx context.Context, c *casefile.Case, steps []casefile.Step) error {
	for _, s := range steps {
		if err := k.step(ctx, c, s); err != nil {
			return err
		}
	}
	return nil
}

// stepTimeout bounds one inject or reset step. The slowest are rollouts,
// which carry their own --timeout below this.
const stepTimeout = 5 * time.Minute

func (k *Kind) step(ctx context.Context, c *casefile.Case, s casefile.Step) error {
	_, err := k.do(ctx, stepTimeout, k.clusterEnv(), c.Dir, s.Stdin, s.Run)
	if err != nil && s.AllowFail {
		k.opts.Log("  step failed, allowed: %v", err)
		return nil
	}
	return err
}

// Defect implements Testbed.
func (k *Kind) Defect(ctx context.Context, c *casefile.Case) (State, error) {
	return k.eval.Eval(ctx, c.Oracle, c.Dir)
}

// Check implements Testbed.
func (k *Kind) Check(ctx context.Context, c *casefile.Case, o casefile.Oracle) (State, error) {
	return k.eval.Eval(ctx, o, c.Dir)
}

// ApplyPatch implements Testbed for the kubernetes-yaml format.
func (k *Kind) ApplyPatch(ctx context.Context, c *casefile.Case, p *verdict.Patch) error {
	if p.Format != "kubernetes-yaml" {
		return fmt.Errorf("patch format %q is not supported; boards applies kubernetes-yaml", p.Format)
	}
	objs, err := applyPatch(ctx, k.kubectlStdin, p.Content)
	k.mu.Lock()
	k.patches[p] = objs
	k.mu.Unlock()
	return err
}

// RemovePatch implements Testbed.
func (k *Kind) RemovePatch(ctx context.Context, c *casefile.Case, p *verdict.Patch) error {
	k.mu.Lock()
	objs := k.patches[p]
	delete(k.patches, p)
	k.mu.Unlock()
	return removePatch(ctx, k.kubectlStdin, objs)
}

// inventoryKinds are the objects whose creation or deletion by a tool is
// counted as a side effect of its investigation.
const inventoryKinds = "pods,services,configmaps,deployments,jobs,secrets,serviceaccounts,roles,rolebindings"

var inventoryLine = regexp.MustCompile(`^\S+$`)

// Inventory implements Testbed.
func (k *Kind) Inventory(ctx context.Context) ([]string, error) {
	out, err := k.cluster(ctx, time.Minute, "", "kubectl", "get", inventoryKinds, "-A",
		"-o", `jsonpath={range .items[*]}{.kind}/{.metadata.namespace}/{.metadata.name}{"\n"}{end}`)
	if err != nil {
		return nil, err
	}
	var objs []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" && inventoryLine.MatchString(l) {
			objs = append(objs, l)
		}
	}
	slices.Sort(objs)
	return objs, nil
}

// Close stops the port forward and deletes the cluster, unless it was
// reused or Keep is set.
func (k *Kind) Close(ctx context.Context) error {
	if k.stopPF != nil {
		k.stopPF()
	}
	if !k.created || k.opts.Keep {
		if k.created {
			k.opts.Log("testbed: keeping cluster %s (kind delete cluster --name %s)", k.opts.Name, k.opts.Name)
		}
		return nil
	}
	k.opts.Log("testbed: deleting cluster %s", k.opts.Name)
	_, err := k.host(context.WithoutCancel(ctx), 5*time.Minute, "kind", "delete", "cluster", "--name", k.opts.Name, "--kubeconfig", k.kubeconfig)
	return err
}
