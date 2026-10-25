# boards architecture

Status: accepted; the core, the mesh testbed and the first adapters are built against it. Decisions are recorded in [`decisions/0001-boards.md`](decisions/0001-boards.md) and cited here as D1-D32. This document sets the boundaries the code is built inside. It changes when the code teaches us something.

## 1. What the system does

Given a track, a suite and a tool adapter, boards does the following:

1. **Builds the testbed** from `testbed.lock`.
   - `mesh`: a kind cluster with Istio ambient, Prometheus and the payments/ledger app.
   - `helm`: a bare kind cluster.
   - `code`: a checkout of a fixture repository at a pinned SHA.
2. **Runs every mesh and Helm case twice: as the defect twin, then as the clean twin.** A code case runs once against its fixture (D22). For each twin:
   1. Wait for residual signals to settle (quiesce).
   2. Inject the defect. The clean twin injects nothing, or installs the fixed version.
   3. Wait until the case's oracle confirms the defect is live (defect twin), or confirms it is absent (clean twin).
   4. Run the tool through its adapter for the case's window.
   5. Collect its verdict and record its cost: wall time, cluster objects created or deleted, and tokens.
   6. If the tool proposed a patch, apply it, wait for the oracle to clear, re-check the clean-twin checks, and remove the patch (D12).
   7. Reset.
3. **Scores each axis** per case (§5), and marks the case contaminated for any tool listed in its `contaminatedFor` (D13).
4. **Writes the report.** It writes one `report.json`, plus Markdown and a terminal view, and exits with a code CI can act on (§6).
5. **Tears the testbed down**, unless `--keep` was given. `--reuse <name>` accepts only a cluster labelled as created by boards, and the run is marked `standard: false` (D17).

In v1 it touches only clusters it created itself (§7).

### What it measures, and what it does not

It measures how well a tool finds a known defect, stays quiet on a clean twin, and, where it proposes one, applies a fix that actually heals. In the code track, it measures how close a tool's numbers come to hand-verified values (D22).

It does not rank tools across categories (D26). It does not publish a total (D16). It does not measure tools on clusters other than its own until the drill mode (D2, v2).

## 2. Principles

- **Measured or null.** Every axis value comes from something boards observed. An axis that does not apply is `null` with a machine-readable reason, never 0. The reasons are:
  - `not_applicable_track`: remediation in the code track.
  - `tool_proposes_no_fixes`: the adapter declares it.
  - `prose_unrated`: prose output that is waiting for its two raters.
  - `oracle_never_live`: the case is INCONCLUSIVE, and the tool is not blamed.
  - `testbed_failed`.
  - `not_llm`: tokens, for a tool without a model.

  A crash or a timeout is the tool's fault, so it is never `null`. Detection and diagnosis score fail, with the reason `tool_crashed` or `timeout` recorded next to the result. Otherwise a flaky tool would drop out of the denominator, and its rates would rise.
- **No tool is special.** Every tool enters through an adapter, MeshMedic included (D3, D10). boards never imports code from a tool it scores. The pieces of meshmedic-prove that boards needs (the Prometheus client, the observer wait loop, the floor collector) are copied into `internal/` with attribution (D4).
- **The measurement must not change what it measures.** This is Helmbulance's rule, borrowed.
  - Quiesce between cases.
  - Snapshot the cluster inventory before and after the tool runs, so a tool's own side effects are visible.
  - Give the tool nothing the case does not give every tool.
- **Cases are data.** A case is a `case.yaml` validated against `schema/case.v1.json`. Code is needed only for a new oracle kind or a new track.
- **Every result names its versions.** A result carries five things: the boards version, the suite (`2026.4`), the tool version, the `testbed.lock` digest, and a `standard` flag that is false for any override such as `--istio` (D18, D28).
- **Same contract as the rest of Git-Hospital.** Exit codes 0/1/2/3, a versioned JSON report, and stable case IDs.

## 3. Package layout

```
cmd/boards/                 CLI: run, validate, list, plan, score, render, verify-holdout
internal/casefile/          case.yaml loader and schema validation
internal/testbed/           testbed.lock, kind lifecycle, per-track provisioners
  mesh/                       Istio ambient, Prometheus, payments/ledger, ingress
  helm/                       bare kind
  code/                       fixture checkout at a pinned SHA
internal/oracle/            promql, http, k8s (resource state), file, script
internal/runner/            case lifecycle, quiesce, inventory snapshot, watchdog
internal/adapter/           adapter protocol (exec), verdict parsing
internal/score/             per-track scorers, contamination, raters merge
internal/report/            report.json, Markdown, terminal
internal/execx/             exec with timeout, cancellation and an env allowlist
internal/prom/              Prometheus client, copied from MeshMedic with attribution
adapters/<tool>/            adapter.yaml plus a script or a small Go program
adapters/dummy/{oracle,silent,panic}/
cases/{mesh,helm,code}/<id>/case.yaml   plus manifests, patches, fixtures
testbed/                    testbed.lock, app manifests, kind configs
holdout/<suite>.sha256      published digests of unrevealed exam cases
results/<suite>/<tool>@<version>.json
results/raw/                raw tool output, unedited
results/legacy-2026-08/     the pre-boards raw outputs and pages, kept as they are
schema/                     case.v1.json, verdict.v1.json, report.v1.json
site/                       leaderboard generator (static, GitHub Pages)
```

## 4. Contracts

### case.yaml

```yaml
id: mesh/error-surge
track: mesh
title: One canary subset serves 5xx
provenance:
  kind: synthetic            # synthetic | issue | postmortem | cve; exam cases may not be synthetic (D15)
  url: null
author: kassvl
authorRelation: [meshmedic] # tools the author built; used for the conflict-of-interest column
contaminatedFor: [meshmedic] # D13
target:
  namespace: demo
  service: payments
inject:
  - run: [kubectl, -n, demo, set, env, deploy/payments-v2, ERROR_RATE=0.9, ERROR_CODE=500]
  - run: [kubectl, -n, demo, rollout, status, deploy/payments-v2, --timeout=180s]
reset:
  - run: [kubectl, -n, demo, set, env, deploy/payments-v2, ERROR_RATE-, ERROR_CODE-]
  - run: [kubectl, -n, demo, rollout, status, deploy/payments-v2, --timeout=180s]
cleanTwin:
  inject: []                 # healthy app, same window
  checks:                    # required when expect.remediation is set; re-run after a patch (D12)
    - promql: >
        sum(rate(istio_requests_total{reporter="waypoint",destination_service_name="payments",response_code!~"5.."}[1m]))
        / sum(rate(istio_requests_total{reporter="waypoint",destination_service_name="payments"}[1m]))
      op: ">"
      value: 0.95            # success rate with traffic present; no data fails the check
oracle:
  promql: >
    sum(rate(istio_requests_total{reporter="waypoint",destination_service_name="payments",response_code=~"5.."}[1m]))
    / sum(rate(istio_requests_total{reporter="waypoint",destination_service_name="payments"}[1m]))
  op: ">"
  value: 0.15
settle: 90s
window: 300s
expect:
  alarmWithin: 5m
  resolvesWithin: 5m         # after reset
  culprits:
    - {kind: Deployment, name: payments-v2, level: 2}  # full diagnosis
    - {kind: Service, name: payments, level: 1}        # partial: service named, subset missed
  quiet: [ledger]            # naming these as culprits is false blame
  remediation:
    class: [outlier-ejection, traffic-shift, rollback]
    healsWithin: 5m
rubric: ground-truth.md      # used by the human raters for prose output (D11)
```

Oracle results have three states: live, absent, or no data. "No data" never counts as cleared. The PromQL ratio of a deleted service is NaN, and it must not read as healed.

The schema rejects `--context`, `--kubeconfig` and `KUBECONFIG=` in any command. That check is only a lint. The enforcement is the kubeconfig the runner supplies, which contains nothing but the boards cluster (§7).

### Adapter protocol

An adapter is a directory with an `adapter.yaml` and an executable:

```yaml
name: meshmedic
category: mesh-specific      # mesh-specific | config-linter | general-ai-agent | helm-tester | static-linter | code-metrics
mode: watcher                # watcher (runs until killed) | one-shot
output: structured           # structured | prose
proposesFixes: true
needsLLM: false
warmup: 5m                   # baseline learning before the window; relative thresholds need it
secrets: []                  # names of environment variables the adapter needs, e.g. an API key
version: [meshmedic, version]
alarm: "an incident report with a firing scenario"     # what counts as an alarm, frozen per suite (D10)
culprits: "the workload labels in the report's evidence"
```

The runner calls `run --work <dir> --window <duration>`. A real tool gets nothing that names the case: not the case directory (it holds the expected culprits, the reference fix and the rubric), not the case id (it names the defect) and not the twin (it says whether there is a defect). Only a dummy adapter also gets `--case <dir> --twin <twin>`, because answering from the case file is its job. The work directory is a fresh temporary directory named after nothing, and HOME is a fresh one beside it, so a tool cannot read its siblings or carry state from one twin to the next; the regular files it leaves are copied to the report afterwards. The runner provides the following, and nothing else from the user's environment:
- `KUBECONFIG`, set to the private kubeconfig, which contains only the cluster boards created;
- `HOME`, set to that run's own empty directory, so a bare `kubectl` cannot fall back to `~/.kube/config`;
- `BOARDS_PROMETHEUS`;
- the case's `target`, as `BOARDS_TARGET_*`. The target is the scope an operator would point the tool at (namespace and service, chart, repository). It never names a culprit, and validation rejects a case whose culprit is its target;
- the secrets the adapter declares.

The window starts only after the adapter's `warmup` has passed on the same cluster. The defect is injected after that. The adapter writes `verdict.json` into the work directory. Its raw tool output goes to `raw.txt` unedited.

### Verdict

```json
{
  "schemaVersion": "boards.verdict/v1",
  "tool": {"name": "meshmedic", "version": "1.1.0"},
  "alarmed": true,
  "firstAlarmAfterSeconds": 63,
  "culprits": [{"kind": "Deployment", "namespace": "demo", "name": "payments-v2"}],
  "ruleIds": ["error-surge-outlier-ejection"],
  "patch": {"format": "kubernetes-yaml", "content": "..."},
  "measurements": null,
  "prose": null,
  "tokens": null
}
```

The fields depend on the track and the output kind:
- The code track fills `measurements`, a map from metric ID to a number or `null`.
- A prose tool fills `prose`, and its adapter also lists any entities it can extract.
- `patch` is `null` when the tool proposed nothing.

## 5. Scoring

Each axis is scored per case: pass, partial or fail, or `null` with a reason. A summary shows, for each axis, the rate, `n`, the null count and, in the code track, the abstained count. There is no total (D16). Contaminated cases are summarised separately for the tools they are contaminated for.

**Mesh**
- *Detection:*
  - pass: the tool alarmed within `alarmWithin` on the defect twin;
  - partial: it alarmed later, inside the window;
  - fail: it did not alarm, or alarmed only after boards began the reset.
  - The clock starts when the defect is observable: after inject, the case's `settle` time and the oracle seeing the defect. An alarm before that counts as immediate, so a slow oracle is never charged to the tool.
- *Diagnosis:*
  - structured output is matched against `expect.culprits` by machine. The best level matched wins: level 2 is a pass, level 1 is partial.
  - naming a `quiet` entity as a culprit is a fail, and is counted as false blame.
  - prose output is scored by two raters against the rubric, and both scores go to `scores.csv`. If they differ by more than one level, a third rater decides (D11).
- *Resolves:* the tool's alarm is clear `resolvesWithin` after boards begins the reset. Clearing while the defect is still live is a fail. Clearing after the tool's own patch went in and healed the defect counts, even though boards then removes the patch and the defect returns until the reset; a tool that re-alarms then must resolve again. This is the fourth of meshmedic-prove's assertions. It is `null` (`tool_one_shot`) for one-shot tools, which never hold an alarm open.
- *Remediation:*
  - pass: four things held. The patch applied. The oracle cleared within `healsWithin` (with traffic present). The `cleanTwin.checks` still passed. The patch could be removed.
  - partial: the patch healed, but its class is not in `expect.remediation.class`. An example is a restart that happened to clear the signal when the expected fix was a rollback. The class comes from the adapter's mapping of the tool's patch.
  - fail: any of those did not happen. The report says which.
  - when `expect.remediation.class` is `none` (the fix is outside the mesh, as in `client-dns-typo`), proposing nothing is a pass, and a mesh patch is a fail.
- *False alarm:* any alarm on the clean twin.
- *Cost:* wall time, objects created or deleted, and tokens.

**Helm**
- *Detection:* a finding on the defect version.
- *False alarm:* a finding on the fixed version.
- *Diagnosis:* the finding names the expected resource or behaviour (for example the PVC, or the data-loss check).
- *Remediation:* `null`.

**Code** (D22)
- *For each metric:* the absolute and relative error against the hand-verified value, with a tolerance per metric. Counts must be exact; coverage within 0.1 percentage points.
- *Null honesty:*
  - expected `null` and reported `null` is a pass;
  - expected `null` and reported a number is *fabricated*, a fail;
  - expected a number and reported `null` is *abstained*. It is counted separately, as a coverage gap, not as dishonesty.

## 6. Report contract and exit codes

`report.json` (`boards.report/v1`) holds:
- the run: boards version, suite, `testbed.lock` digest, `standard`, start and end;
- the tool: name, version, category;
- `cases[]`: ID, twin, provenance, contamination, axes (status, value, reason), cost, and paths to `verdict.json` and `raw.txt`;
- `summary`: for each axis, the rate, `n` and the null count.

Exit codes follow Helmbulance:

| code | `boards run` | `boards run --baseline <results.json>` (the Action) |
|---|---|---|
| 0 PASS | every case was scored | and no axis of any case is worse than the baseline |
| 1 FAIL | not used | at least one case got worse than the baseline |
| 2 | usage or setup error | same |
| 3 INCONCLUSIVE | at least one case could not be scored (testbed failed, oracle never live) | same |

## 7. Safety

- **v1 writes only to clusters it created.**
  - Clusters are named `boards-<run-id>`, and their kubeconfig goes to a private temporary file.
  - Before anything runs, the user's `KUBECONFIG`, `HELM_KUBE*`, `HELM_DRIVER*` and `HELM_NAMESPACE` are dropped, as in Helmbulance's Action.
  - Every inject, reset, patch and adapter command runs through `execx`. `KUBECONFIG` points at the private file, `HOME` at a scratch directory, and the environment is an allowlist. The private kubeconfig is the guarantee. The schema lint on commands is a second layer, and it cannot see inside `script:`.
- **Charts in the Helm track are untrusted code.** They get the protections Helmbulance documents: a fresh VM in CI, a token with no permissions, no secrets.
- **The holdout.**
  - Exam cases live in the private repository `Git-Hospital/boards-holdout`.
  - The digest is the SHA-256 of a canonical tar of the case directory, with sorted entries and zeroed timestamps. It is committed to `holdout/<suite>.sha256` the day the case is written.
  - `boards verify-holdout` recomputes the digests after the cases are revealed.
- **Drill mode (v2).**
  - `--target-context` prints every command and every object it will touch, and requires `--yes-inject-faults`.
  - It refuses a context whose name matches a production deny list.
  - It runs `reset` on every exit path, as `harness/run.sh` does today.

## 8. Testing

- **Dummy tools on every PR (D32).** In dry-run mode, the oracle, silent and panic tools must get exactly their expected scores on every case. The runner then uses a fake testbed that answers the oracle from the case file.
- **Schema tests.**
  - Every case validates.
  - Every mesh and Helm exam case has a non-synthetic `provenance` with a URL.
  - Every code exam case names its verification method and its second verifier.
  - No command carries a context.
- **Isolation test.** A dummy adapter runs a bare `kubectl get ns` and a bare `helm list -A`. Both must see only the boards cluster.
- **Scorer fixtures.** Hand-written verdicts paired with their expected axis results, including false blame, fabricated and abstained.
- **Golden reports.** `report.json` and the Markdown for a fixed dry run are compared byte for byte.
- **Weekly kind job (D20).** Three mesh cases with MeshMedic and istioctl: a fault, a clean twin, and a remediation apply.
- **Parity with meshmedic-prove (D25).**
  - A tracking issue lists the 16 proofs. Each is ticked when the boards case, run with the MeshMedic adapter on the testbed, passes detection, diagnosis (culprit named, quiet neighbours untouched) and resolves.
  - The silence run and the winnability check are listed too. Each is ticked when ported, or when a recorded decision drops it. Winnability maps to a schema rule: `expect.alarmWithin` must exceed the hold duration that the adapter declares for the tool.

## 9. Open questions

1. **Does a free GitHub runner fit the mesh testbed?** It has to hold kind, Istio ambient, Prometheus and the app, and finish three cases within a reasonable job time. Spike before v0.3.0.
2. **How does the MeshMedic adapter get the patch?** Settled: MeshMedic gained JSON events (`watch --events`), and [`adapters/meshmedic`](../adapters/meshmedic) reads them.
3. **How is a patch applied faithfully?** A DestinationRule patch from MeshMedic likely does not override the faulty one. Patch formats must say whether they create a resource, replace one, or merge into a named target. Applying must follow that, not guess.
4. **Where does the alert-rule baseline come from?** It must not be written by us, or we would tune it. Candidate: a published community rule set for Istio, used unmodified and pinned.
5. **What happens when a code-track tool uses a different definition?** Some metrics have more than one definition: radon and gocyclo count cyclomatic complexity slightly differently. Two ways to score: against the definition the tool states it implements, or against one documented definition, with the difference reported as error. Decide before v0.6.0.
6. **What happens to links when the repository is renamed?** Settled: it was renamed from mesh-incidents-bench before any leaderboard existed, and GitHub redirects the old URL.
