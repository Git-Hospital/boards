# MeshMedic results

- Tool: [MeshMedic](https://github.com/Git-Hospital/meshmedic) v1.0.0, report-only mode
  (`meshmedic watch`, full catalog, no gitops)
- Date: 2026-08-19 (UTC), testbed: kind + Istio 1.24 ambient, single node
- Raw output: `raw/*-meshmedic-*.txt`, the 2026-08-19T21:00Z and later timestamps
- Calibration of the same target, on an idle cluster the next day: `calibration-2026-08-20.txt`

**Disclosure**: this benchmark and MeshMedic share an author, and the scenarios
overlap MeshMedic's remediation catalog. More than that: several catalog
entries were developed against these exact scenarios, including the ztunnel L4
signal behind `mtls-conflict`. Treat the table below as a home game played
after studying the tape. The scoring rubric is public, the raw outputs are
unedited, and outside scenario contributions remain the honest fix for the
bias.

## Scores

| scenario | detection | diagnosis | remediation | total |
| --- | --- | --- | --- | --- |
| canary-latency | 2 | 2 | 2 | 6 |
| error-surge | 2 | 2 | 2 | 6 |
| pool-overflow | 2 | 2 | 2 | 6 |
| mtls-conflict | 2 | 2 | 2 | 6 |
| noise-only | 2 | 2 | 2 | 6 (see the asterisk below) |
| client-dns-typo | 2 | 2 | 2 | 6 |
| client-wrong-port | 2 | 2 | 2 | 6 |
| client-wrong-scheme | 2 | 2 | 2 | 6 |
| authz-deny-flood | 2 | 2 | 2 | 6 |
| route-timeout-too-short | 2 | 2 | 2 | 6 |
| fault-injection-left | 2 | 2 | 2 | 6 |
| **total** | | | | **66 / 66** |

Harness-measured, per run: tool wall time 240s or 300s (the window given to it;
detection itself lands on the first 15s tick after the signal's hold duration),
and **0 cluster objects created or deleted** in every run.

Every scenario in the benchmark is scored here. The previous version of this
file scored six of eleven and left the other five with raw runs but no verdict,
which reads as coverage it did not have.

## The asterisk on noise-only, and why it matters more than the 66

`noise-only` inverts the rubric: an empty ConfigMap, an endpoint-less Service
and a completed one-off pod sit in a healthy namespace, and silence is the
correct answer. MeshMedic reported no incident for the whole window, which is
what the rubric scores, so the 2 stands.

It is not the whole truth, and the first version of this page got the
correction itself wrong. Both are worth writing down.

**What happened.** The per-cycle summary showed a breach for the last three
ticks of that run: `upstream-dependency-latency`, whose signal is the watched
service's outbound p99 to its dependencies, went over its 200ms threshold on a
cluster where nothing had been injected into that path. Reading the same
window afterwards, the breach began at 21:41:30Z and ran unbroken for **180
seconds**. The tool's window closed 25 seconds into it. The entry's hold
duration is 90 seconds, so had the run lasted two minutes longer, MeshMedic
would have published an incident about a dependency that was never slow, and
`noise-only` would have scored 0. The 2 is the scoring window's timing, not
the design's.

**What this page said the first time, and why it was wrong.** It quoted a
calibration run reporting a healthy peak of 222.6ms against the 200ms
threshold, and concluded the headroom was negative. That calibration was
started five minutes after a scenario reset. `meshmedic calibrate` prints
`the cluster must be healthy for this to mean anything: no injected faults, no
ongoing incident` before it takes a sample, and the condition was not met: the
previous fault was still decaying out of a two-minute rate window. The number
was a measurement of the tail, not of health, and two other entries were
called marginal on the same bad sample.

Re-run on a genuinely idle cluster over twelve minutes
(`calibration-2026-08-20.txt`):

| entry | first, contaminated | clean |
| --- | --- | --- |
| `upstream-dependency-latency` | 0.898x, negative | **1.18x, marginal** |
| `canary-latency-rollback` | 1.18x, marginal | **2.88x, calibrated** |
| `latency-regression-vs-baseline` | 1.54x, marginal | **2.49x, calibrated** |
| gate verdict | 3 marginal | **1 marginal**, 9 unmeasured |

Two entries were named as problems that are not. The gate still does not pass
on this target, because one marginal entry and nine that produce no readings
are both reasons to withhold a pass, but the specifics were wrong and are
corrected here rather than quietly edited.

**The sharper finding underneath.** The two measurements do not disagree; they
sampled different regimes. Idle, this signal peaks at 169ms. Across the full
benchmark suite, with pods cycling and load shifting and *other* entries'
faults live, it peaks at 433ms and spends 180 unbroken seconds over 200ms. The
injected fault this entry exists to catch reaches 493ms. A healthy spike and a
real fault are 1.14x apart, which no threshold can separate.

So the limitation is not this entry's number. It is the gate's sampling: it
measures the cluster it is pointed at, at the moment it is pointed, and an
idle moment is not the only healthy moment. A gate that only ever sees the
quiet regime will keep calling entries calibrated that an ordinary busy hour
fires.

The catalog's answer, made after these measurements: the entry now fires at
four times its own learned normal rather than at a fixed 200ms, and holds for
five minutes rather than ninety seconds. The hold is the part that does the
work, because the two regimes are separable by duration and not by height, and
five minutes clears the worst observed transient by 1.7x.

So the honest reading of this page is two sentences. MeshMedic recognises
every fault in the benchmark and names the offending field in each. Whether
its thresholds hold up on a cluster that is doing ordinary things is a
different question, one a benchmark made of faults cannot ask, and the one
place this suite did ask it, the answer was no.

## Notes per scenario

- **canary-latency**: `canary-latency-rollback` fired at p99 2482ms against a
  1000ms threshold, with the stable subset at 217ms for comparison, and
  proposed the VirtualService shift to v1. Configuration evidence read
  `TIMING_50_PERCENTILE=1200ms` off the canary Deployment, which is the
  injected root cause. New since v0.2: `latency-regression-vs-baseline` was
  also in breach and was suppressed, logged as `cascade symptom, not a second
  incident`, so one fault produced one report rather than two.
- **error-surge**: `error-surge-outlier-ejection` fired at a 0.2196 ratio
  against 0.15. Labelled evidence pinned the 500s to the v2 subset (0.79 rps
  from `payments-v2`, zero from `payments-v1`) and configuration evidence
  showed `ERROR_RATE=0.9 ERROR_CODE=500` on that Deployment. Proposed outlier
  detection in a DestinationRule.
- **pool-overflow**: `connection-pool-overflow` fired at 12.71 UO rps against
  1, and `error-surge-outlier-ejection` was suppressed as a cascade symptom.
  The diagnosis names pool exhaustion and separates raise-from-scale using
  `replicas: 1` and empty resource limits. **Gap worth recording**: the object
  evidence reads the Deployment, but the fault lives in the DestinationRule,
  whose `maxConnections: 1` never appears in the report. The operator still
  has to go look it up. Scored 2 because the cause is named and the proposed
  fix is right, but the evidence should point at the object that holds the
  fault.
- **mtls-conflict**: `mtls-policy-conflict-ambient` fired at 0.98 against 0.2,
  named the denied caller (`plain-client`, `source_principal="unknown"`, in
  the `default` namespace) and the policy denying it (`demo-strict`, mode
  STRICT), and proposed the scoped PERMISSIVE fallback while stating plainly
  that it is a temporary security downgrade. This run also settles a question
  the catalog raised: the sidecar-mode L7 variant of this entry was retired on
  2026-08-19 after measurement showed it could never fire, and the class is
  still fully covered here by the ambient entry.
- **noise-only**: no incident reported across the window. See the asterisk
  above for what the summary line showed underneath.
- **client-dns-typo**, **client-wrong-port**, **client-wrong-scheme**: the
  breadth-honesty controls, where the fault is in the caller and no mesh patch
  is the right answer. All three followed the same path:
  `traffic-vanished-triage` fired on the absence signal, the dossier named the
  former caller (`loadgen`), the log sweep caught the client's own error
  (`Could not resolve host: payments-svc.demo`, `Empty reply from server`,
  `TLS connect error: packet length too long` respectively), and the rollout
  diff showed the changed line verbatim: `http://payments:9090/` against
  `http://payments-svc.demo:9090/`, `:9091`, and `https://` in turn. No patch
  was proposed in any of the three, which is the correct answer, not an
  omission.
- **authz-deny-flood**: fired at 3.024 rps against 0.5, identified the denied
  caller by its full mesh identity
  (`spiffe://cluster.local/ns/demo/sa/default`, workload `loadgen`) and listed
  the denying policy with its action and rules
  (`payments-block-loadgen`, DENY). It refuses to propose a loosened policy
  and says why: whether a denial is a mistake or is working as intended is an
  operator's judgment, and auto-loosening authorization could open a real
  hole.
- **route-timeout-too-short**: fired at 3.206 UT rps against 0.5 and put the
  offending field next to the measurement that condemns it: `VirtualService
  demo/payments` with `timeout: 0.01s`, beside a measured backend p99 of
  165.9ms. It declines to regenerate the VirtualService because doing so would
  replace the operator's routing table, and shows the 80/20 weights it would
  have overwritten.
- **fault-injection-left**: fired at 1.619 FI rps against 0.5 and pinned the
  fault stanza itself
  (`{"abort":{"httpStatus":503,"percentage":{"value":50}}}`). The diagnosis
  explains why the usual 5xx response is wrong here: the endpoints are healthy
  and the failures are synthetic, so outlier ejection would evict good hosts.

## What changed since the previous measurement

The last table on this page was a v0.2 measurement from 2026-07-17. The binary
that produced it no longer exists. Between then and now MeshMedic gained the
four-state evaluation model with a per-target coverage probe, a hash-locked
catalog where unapproved entries do not run at all, persisted incident state,
an enforced apply-rate limit, the calibration gate quoted above, and the
cascade suppression visible in `canary-latency` and `pool-overflow`. One entry
was retired. Any of those could have moved a score, so the old numbers were
not evidence about this version of the tool.

## Two harness bugs found by re-running, both now fixed

Re-running found more in the harness than in the tool, which is worth writing
down rather than quietly fixing.

**No scenario pinned a kube context.** Twenty-eight scripts call `kubectl`
with no `--context`, deliberately, so that a scenario runs against whatever
cluster you point at. That portability made the harness the only place that
could catch being pointed somewhere else, and it was not checking. Being
pointed elsewhere here is not a stale read: the scenarios *write*, so a wrong
context injects a fault into a cluster nobody meant to touch and then scores
the tool on a cluster where nothing happened. `harness/run.sh` now refuses to
start unless the current context matches `BENCH_CONTEXT`.

**A live port-forward is not proof it points at this cluster.** Forwards
outlive the cluster they were opened against. The harness now also asks
Prometheus whether it can see the `demo` namespace before injecting, and
refuses if it cannot. That check fired for real during this run: the forward
died mid-suite, and six scenarios were refused rather than injected blind.
