# boards

[![CI](https://github.com/Git-Hospital/boards/actions/workflows/ci.yml/badge.svg)](https://github.com/Git-Hospital/boards/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/Git-Hospital/boards)](LICENSE)

A benchmark for tools that diagnose and fix infrastructure. Each case is a
known defect injected into a real cluster, with a clean twin that must stay
quiet, and boards scores what a tool finds, what it blames and whether its
fix works once applied.

Today it has one track: eleven service-mesh incidents on Istio ambient. Helm
and code-health tracks are planned (see [Design](#design)). The repository was
called mesh-incidents-bench until October 2026.

## Try it without a cluster

```console
$ go build -o boards ./cmd/boards
$ ./boards run --adapter adapters/dummy/oracle --dry-run
...
CASE                          DETECTION  DIAGNOSIS  REMEDIATION  RESOLVES  CLEANTWIN
mesh/authz-deny-flood         pass       pass       pass         null      pass
mesh/canary-latency           pass       pass       pass         null      pass
...
mesh/noise-only               -          -          pass         -         pass
...
testbed dry-run is not standard: this result never enters the official table
```

`--dry-run` replaces the cluster with a fake testbed, so it checks boards
itself, never a tool. `adapters/dummy/` holds three stand-in tools that CI
runs on every pull request: one that always answers right (`oracle`), one
that says nothing (`silent`) and one that alarms on everything (`panic`).

## A real run

```console
$ ./boards run --adapter adapters/dummy/oracle --track mesh --case mesh/error-surge
```

This builds a kind cluster from [`testbed/testbed.lock`](testbed/testbed.lock)
(Istio ambient, Prometheus, and a payments service with two versions and a
downstream ledger), injects each case's fault, runs the tool with the
incident live, scores it, resets, runs the clean twin, and deletes the
cluster. It needs Docker, `kind`, `kubectl` and `istioctl` at the locked
versions.

boards never touches your own clusters. Every command gets a private
kubeconfig and an empty `HOME`, and `--reuse` accepts only a cluster boards
built itself (a reused run is marked not standard). `--keep` leaves the
cluster up for inspection.

To score a real tool, pass its adapter instead: [`adapters/meshmedic`](adapters/meshmedic)
needs a `meshmedic` build after v1.0.0 (the first with `watch --events`) and
python3 on `PATH`.

## What is scored

| axis | question |
| --- | --- |
| detection | Did the tool raise an alarm while the fault was live, within the case's time limit? |
| diagnosis | Did it blame the right object? Naming the service but not the subset is partial; naming a healthy neighbour is false blame. |
| remediation | Applied to the cluster, does the tool's patch heal the fault within the case's time limit, keep the clean twin's checks passing, and come off again? A patch that heals but is not one of the case's accepted classes is partial. |
| resolves | Did the tool's alarm clear after the fault was removed? `null` for one-shot tools, which never hold an alarm open. |
| clean twin | On the same app without the fault, did the tool stay quiet? |

A result that could not be measured is `null` with a reason, never a pass.
Each case's [`case.yaml`](cases/mesh/error-surge/case.yaml) holds the
injection, the PromQL oracle that confirms the fault is live, the expected
culprits and the accepted and forbidden fixes; its `ground-truth.md` explains
the root cause for a human reader.

## Cases

| case | fault | correct fix |
| --- | --- | --- |
| `authz-deny-flood` | a DENY AuthorizationPolicy rejects a legitimate caller (403) | none automatic: name the policy, let an operator decide |
| `canary-latency` | the canary subset's p99 latency regresses | shift traffic back to stable |
| `client-dns-typo` | a client rollout targets a host that does not resolve | none in the mesh: fix the client's host |
| `client-wrong-port` | a client rollout targets a port the service does not expose | none in the mesh: fix the client's port |
| `client-wrong-scheme` | a client rollout speaks https to a plain-HTTP service | none in the mesh: fix the client's scheme |
| `error-surge` | one canary subset serves 5xx | shift traffic away, eject the subset, or roll back |
| `fault-injection-left` | a fault-injection stanza is left on a production VirtualService | remove the stanza, keep the routes |
| `mtls-conflict` | STRICT mTLS rejects a plaintext client outside the mesh | enroll the client, or a scoped PERMISSIVE policy |
| `noise-only` | none: a healthy service with harmless noise | none; silence is the answer |
| `pool-overflow` | the circuit breaker sheds load from an undersized connection pool | resize the pool |
| `route-timeout-too-short` | a route timeout is shorter than the backend's latency (504) | none automatic: raising the timeout heals, but only an operator knows the intent |

The client cases are outages that show up only as missing traffic, one layer
above the mesh; `noise-only` measures false alarms. `boards list` prints the
cases with the tools each one is contaminated for.

## Conflict of interest

boards and [MeshMedic](https://github.com/Git-Hospital/meshmedic) share an
author, and every case so far was written alongside MeshMedic. Each case
records that (`contaminatedFor: [meshmedic]`), and a contaminated result is
reported as such. Cases written by people who did not build the tool being
scored, drawn from real incidents, postmortems or Istio issues, are the fix;
see [CONTRIBUTING.md](CONTRIBUTING.md).

## Results so far

These predate the Go core. They were produced with the earlier shell harness
(below) and scored by hand against each `ground-truth.md`.

- [results/meshmedic.md](results/meshmedic.md): MeshMedic on all eleven
  scenarios. A home game, for the reason above.
- [results/istioctl-analyze.md](results/istioctl-analyze.md): Istio's own
  configuration analyzer, the same-domain reference. It is a config-time
  linter, so it catches invalid references that MeshMedic does not, and does
  not report a runtime 5xx surge or a rate limit throttling traffic (it only
  warns about EnvoyFilter hygiene).

An earlier version also scored HolmesGPT and k8sgpt and headlined that
MeshMedic beat them. That comparison was withdrawn: those tools target
Kubernetes in general, and HolmesGPT's own evaluation corpus had no Istio
scenario (audited 2026-07-19). They are planned to return as ordinary
adapters, compared within their own category.

## Design

- [docs/decisions/0001-boards.md](docs/decisions/0001-boards.md): the
  decisions behind boards, with the options rejected.
- [docs/architecture.md](docs/architecture.md): the case and adapter
  contracts, scoring, the report, and the safety rules.
- [docs/ambient-l4-denial-telemetry.md](docs/ambient-l4-denial-telemetry.md):
  how to see ambient strict-mTLS denials in ztunnel's L4 telemetry, verified
  on Istio 1.24.1.
- [docs/investigation-footprint.md](docs/investigation-footprint.md): how
  much a diagnostic tool changes the cluster while it investigates, and how
  it is measured.
- [docs/taxonomy/response-flags-coverage.md](docs/taxonomy/response-flags-coverage.md):
  Envoy's response flags, mapped against the MeshMedic catalog: what it
  covers and where the gaps are.

## The earlier harness

`harness/run.sh` and `scenarios/<id>/` (`inject.sh`, `check.sh`,
`reset.sh`) are the shell harness the results above were made with. It runs
against the MeshMedic demo environment and records the tool's wall time and
the cluster objects it created or deleted. It stays until every adapter runs
under `boards run`. [CONTRIBUTING.md](CONTRIBUTING.md) has the invocation, the
testbed setup and the 0-2 scale those results use.

## License

Apache-2.0
