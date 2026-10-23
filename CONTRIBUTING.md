# Contributing

The most valuable contribution to this benchmark is a new scenario,
especially one whose fault is not already covered and, ideally, one whose
remediation is not in MeshMedic's catalog. The benchmark and MeshMedic share
an author; that is a bias, disclosed on every results page. Outside
scenarios are the honest fix for it. A scenario that MeshMedic scores poorly
on is a good scenario.

The most valuable contribution is a scenario drawn from a real Istio incident
you hit, a public postmortem, or an Istio GitHub issue, authored by someone who
did not build the tool being scored. That is what breaks the home game.

## What a scenario is

> **During the move to boards** (see the README), a new scenario is written
> as a case: `cases/mesh/<id>/case.yaml` plus its `ground-truth.md`. Copy
> `cases/mesh/error-surge/`, run `go run ./cmd/boards validate`, and see
> [docs/architecture.md](docs/architecture.md) §4 for every field. The
> four-file shape below is what the 11 existing scenarios still use for live
> runs until the kind testbed lands, and the rules further down apply to
> both.

A scenario is a directory under `scenarios/<id>/` with three scripts, and its
ground truth lives in `cases/mesh/<id>/ground-truth.md`. Each one
is small and does one job. Read an existing scenario such as
`scenarios/error-surge/` before writing your own; the shapes below mirror it.

### `inject.sh`

Injects the fault. Idempotent where possible, and it should wait until the
fault is actually in effect (roll out the change, do not just apply it).

```bash
#!/usr/bin/env bash
set -euo pipefail
kubectl -n demo set env deploy/payments-v2 ERROR_RATE=0.9 ERROR_CODE=500
kubectl -n demo rollout status deploy/payments-v2 --timeout=120s
```

### `check.sh`

Exits 0 while the incident is objectively live, non-zero otherwise. The
harness polls it after injection and only runs the tool once it passes, so
the tool always sees the incident. Base it on an observable signal (a
Prometheus query, a pod state, a log line), not a fixed sleep.

```bash
#!/usr/bin/env bash
set -euo pipefail
v=$(curl -sf "http://127.0.0.1:9090/api/v1/query" --data-urlencode \
  'query=<your ratio or rate>' \
  | python3 -c 'import json,sys; r=json.load(sys.stdin)["data"]["result"]; print(r[0]["value"][1] if r else "nan")')
python3 -c "import math; v=float('$v'); exit(0 if not math.isnan(v) and v > 0.15 else 1)"
```

### `reset.sh`

Restores the testbed exactly. The harness runs it on exit, including after a
tool is killed, so it must be safe to run at any time and must leave no
residue that would bleed into the next scenario.

```bash
#!/usr/bin/env bash
set -euo pipefail
kubectl -n demo set env deploy/payments-v2 ERROR_RATE- ERROR_CODE-
kubectl -n demo rollout status deploy/payments-v2 --timeout=120s
```

### `ground-truth.md`

The fault, the root cause, the correct remediation, and a scoring rubric.
The rubric is three axes, 0 to 2 each: detection (did the tool notice
something is wrong), diagnosis (did it name the actual cause), remediation
(did it propose the fix an operator would take). Scores are assigned by a
human against this rubric, so write it to be gradeable by someone who is not
you. A scenario may invert an axis (see `noise-only`, where silence is the
correct detection answer) as long as the rubric says so explicitly.

## Rules that keep the benchmark honest

- **The fault must be real and observable.** `check.sh` has to pass against
  a live signal. If you cannot write a `check.sh`, the scenario is not
  reproducible enough to include.
- **Reset must be complete.** Residual telemetry from one scenario decays
  over the PromQL rate windows and can make the next tool fire on the wrong
  incident. The harness enforces a quiesce period, but your `reset.sh` must
  still restore state fully.
- **Disclose overlap.** If the scenario happens to match a tool's catalog or
  training bias, say so in the ground truth. Transparency about bias is the
  point of the whole project.
- **No hints in the prompt.** Tools are run with the same fault-free prompt
  (see `harness/`); a scenario must not require a tool-specific hint to be
  solvable.

## Running it

```console
$ ./harness/run.sh scenarios/<your-id> 300 -- <tool command>
```

The harness injects the fault, waits for `check.sh`, runs the tool with the
incident live, saves the output under `results/raw/`, records a footer with
the tool's wall time and the cluster objects it created or deleted, and
resets the testbed. Put your scored result under `results/` following the
format of the existing files, and add a row to the README leaderboard.

## Testbed

Scenarios run against the
[MeshMedic demo environment](https://github.com/kassvl/meshmedic) (kind +
Istio ambient + a two-version payments service + Prometheus). Bring it up
with `demo/scripts/00..02` from that repo, then port-forward Prometheus to
`127.0.0.1:9090`.
