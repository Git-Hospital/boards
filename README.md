# boards (formerly mesh-incidents-bench)

> **Moving to boards.** This repository is becoming boards, the Git-Hospital
> benchmark: one core, three tracks (mesh, Helm, code), and every tool
> measured through an adapter next to its domain's rivals. The decisions are
> in [docs/decisions/0001-boards.md](docs/decisions/0001-boards.md) and the
> design in [docs/architecture.md](docs/architecture.md).
>
> What exists today: the Go core (`cmd/boards`), the 11 scenarios below as
> `cases/mesh/<id>/case.yaml`, three dummy tools that test the scorer on
> every PR, and the mesh testbed. `boards run` builds a kind cluster from
> [`testbed/testbed.lock`](testbed/testbed.lock) (Istio ambient,
> Prometheus and the payments/ledger app), runs the cases against one tool,
> and deletes the cluster:
>
> ```console
> $ go build -o boards ./cmd/boards
> $ ./boards run --adapter adapters/dummy/oracle --track mesh --case mesh/error-surge --keep
> $ ./boards run --adapter adapters/dummy/oracle --track mesh --reuse boards-<name>   # not standard
> $ ./boards run --adapter adapters/dummy/oracle --dry-run                           # no cluster
> ```
>
> It needs Docker, `kind`, `kubectl` and `istioctl` at the locked versions.
> It never touches your own clusters. Every command gets a private kubeconfig
> and an empty HOME, and `--reuse` accepts only a cluster boards built
> itself. Real tool adapters come next. Until then, tool runs still use
> `harness/run.sh` and the scripts in `scenarios/`, as described below. Each
> scenario's ground truth now lives next to its case, in
> `cases/mesh/<id>/ground-truth.md`.

Reproducible service mesh failure scenarios, with ground truth, for scoring
how well diagnostic and remediation tools handle mesh-layer incidents.

Plenty of tools claim to troubleshoot Kubernetes. Almost none of them speak
the mesh's language: traffic splits, outlier ejection, connection pools,
mTLS modes. This benchmark makes that measurable. Every scenario is a real
fault injected into a real Istio ambient mesh, with a documented root cause
and the remediation an experienced mesh operator would apply.

## Scenarios

Eleven scenarios today. The six below were the original set; five more
(`authz-deny-flood`, `client-wrong-port`, `client-wrong-scheme`,
`fault-injection-left`, `route-timeout-too-short`) have been added since. Each
is a real fault injected into a live mesh, with a documented root cause, the
remediation an operator would apply, and a scoring rubric in its
`ground-truth.md`.

| id | fault | mesh-native remediation |
| --- | --- | --- |
| `canary-latency` | canary subset p99 regression | shift traffic back to stable |
| `error-surge` | one subset throwing 5xx | outlier ejection or rollback |
| `pool-overflow` | circuit breaker shedding load (UO) | right-size the connection pool |
| `mtls-conflict` | plaintext client vs STRICT mTLS | enroll client or scoped PERMISSIVE |
| `noise-only` | none: healthy service plus harmless noise | none; silence is the correct answer |
| `client-dns-typo` | client targets a non-resolving host, payments traffic drops to zero | none in the mesh; fix the client's target host |

`noise-only` inverts the detection axis: it measures false-positive
discipline, the alert-fatigue failure mode the fault scenarios cannot see.

`client-dns-typo` is the breadth-honesty control. Every fault scenario above
emits a pathological mesh signal that a catalog of threshold detectors can
match; this one is a real, total outage (100% of user-facing calls fail) that
shows up only as the *absence* of telemetry, one layer above the mesh in a
client's own config. When first run, every tool scored 0 here - including
MeshMedic, as the scenario predicted for catalog-based detectors. That
prediction had a loophole: absence *is* detectable deterministically
(`or vector(0)` plus a `max_over_time` baseline), and once a detector
knows traffic vanished, the client's own logs and the most recent rollout
diff usually contain the root cause verbatim. MeshMedic's v0.3 triage
layer does exactly that, and the scenario now measures which tools can see
a disappearance as well as an appearance.

Each scenario directory contains `inject.sh`, `check.sh` (exits 0 while the
incident is live) and `reset.sh`. The root cause, the expected remediation
and the scoring rubric are in `cases/mesh/<id>/ground-truth.md`, next to the
scenario's `case.yaml`.

## Scoring

Three questions per scenario, 0 to 2 points each:

1. **Detection**: did the tool notice something is wrong with the workload?
2. **Diagnosis**: did it name the actual root cause?
3. **Remediation**: did it propose the mesh-native fix an operator would take?

Scores are assigned by a human against the rubric in each `ground-truth.md`.
Runs are honest: misses are published, including the benchmark author's own
tool. See `results/`.

The benchmark and MeshMedic share an author. A new scenario, especially one
the author's tool does poorly on, is the best fix for that bias; see
[CONTRIBUTING.md](CONTRIBUTING.md).

## Comparison: MeshMedic vs istioctl analyze

An earlier version of this benchmark scored general Kubernetes tools (HolmesGPT,
k8sgpt) on these mesh scenarios. That was a category error and has been removed:
those tools do not target the service mesh. HolmesGPT's own 266-fixture
evaluation corpus contains zero Istio scenarios (audited 2026-07-19; the only
"traffic" fixtures are CNI NetworkPolicy), so testing it on mesh incidents
measured a domain mismatch, not tool quality, and "MeshMedic beats Holmes" was
never a well-formed claim - they are tools for different layers.

The fair, same-domain reference is [`istioctl
analyze`](results/istioctl-analyze.md), Istio's own configuration analyzer. Both
it and MeshMedic read the mesh; the honest question is what each one sees. Run
against the scenarios on the live testbed:

| scenario / fault | istioctl analyze | MeshMedic |
| --- | --- | --- |
| Config error: a VirtualService references a subset no DestinationRule defines | catches it: `Error [IST0101] ... payments+v3` | sees it only once traffic fails at runtime |
| Runtime 5xx surge (`error-surge`, ERROR_RATE 0.9) | `No validation issues found` | detects it and names the failing subset |
| Rate limit rejecting live traffic (429/RL) | a generic EnvoyFilter hygiene warning, not "traffic is being throttled" | detects the 429/RL and names the throttled caller |

The split is complementary, not a contest. `istioctl analyze` is a config-time
linter: it answers "is my Istio configuration valid and safe?" before traffic,
and catches invalid references and mode conflicts, which MeshMedic does not do.
A valid but wrong config - a timeout shorter than the backend, a rate limit set
too low - passes istioctl analyze and only surfaces once traffic hits it, which
is MeshMedic's job. MeshMedic is a runtime incident detector, reading the
Prometheus and ztunnel telemetry istioctl analyze never looks at. Run istioctl
analyze in CI to keep the config valid; run MeshMedic against live telemetry to
catch what a valid config does under real traffic.

MeshMedic's own diagnosis quality against each scenario's rubric, disclosed as a
home game (same author as this benchmark, developed against these exact
scenarios), is in [results/meshmedic.md](results/meshmedic.md). The standing fix
for that bias is scenarios authored independently; see
[CONTRIBUTING.md](CONTRIBUTING.md).

## Testbed

Scenarios run against the [MeshMedic demo environment](https://github.com/kassvl/meshmedic)
(kind + Istio ambient + a two-version payments service that calls a downstream
ledger dependency, fronted by a north-south ingress Gateway + Prometheus):
bring it up with `demo/scripts/00..02`, then:

```console
$ ./harness/run.sh scenarios/canary-latency 300 -- <your tool command>
```

The harness injects the fault, waits until `check.sh` confirms the incident
is observable, runs your tool with the incident live, saves its output under
`results/raw/`, and resets the testbed.

Each raw output ends with a footer the harness measures itself: the tool's
wall time and an inventory diff of cluster objects created or deleted while
the tool ran. Investigation side effects are part of a tool's cost; a
diagnostic run that spawns test pods in a production namespace mid-incident
is worth knowing about before you page it.

## Reference docs

- [docs/ambient-l4-denial-telemetry.md](docs/ambient-l4-denial-telemetry.md):
  how to detect ambient strict-mTLS denials from ztunnel's L4 telemetry, the
  signal every request-metric tool misses on `mtls-conflict`. Metric names
  and label sets verified live on Istio 1.24.1.
- [docs/investigation-footprint.md](docs/investigation-footprint.md): the
  cost axis of how much a diagnostic tool mutates the cluster while it
  investigates, how the harness measures it, and why MeshMedic's zero is a
  design guarantee while an agent's is per-run.
- [docs/taxonomy/response-flags-coverage.md](docs/taxonomy/response-flags-coverage.md):
  Envoy's full response-flag vocabulary mapped against what the tool covers,
  so the failure taxonomy is grounded in the mesh's own signals rather than
  imagination. The `docs/taxonomy/` directory holds the candidate failure
  classes and the validation queue that feed new scenarios.

## Status and goals

The honest limit of this benchmark is that its author also wrote MeshMedic, so
MeshMedic's scores are a home game. The `istioctl analyze` comparison is fair
(same domain, independent tool) but narrow, because istioctl is a config linter,
not an incident detector. The real credibility fix is scenarios authored
independently of MeshMedic: real Istio incidents from public postmortems and
issues, contributed by mesh operators who did not build the tool being scored.
That is the point of the repository, not an afterthought; see
[CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache-2.0
