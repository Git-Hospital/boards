# client-dns-typo: ground truth

**Fault**: a bad rollout of the `loadgen` Deployment changed its request loop
to call `http://payments-svc.demo:9090/` instead of `http://payments:9090/`.
No Service named `payments-svc` exists, so every request fails name resolution
(`NXDOMAIN`) before a socket is opened. User-facing calls to `payments` go to
100% failure and traffic through the mesh drops to zero. All pods stay
`Running`; `payments` v1/v2 are healthy and simply idle.

**Root cause**: client-side misconfiguration. The target host in the `loadgen`
container command (`spec.template.spec.containers[0].args`) points at a name
that does not resolve. Nothing is wrong with the `payments` workload, the mesh
routing, the connection pool, or the mTLS policy. The failure is entirely in
the caller and happens at `getaddrinfo`, one layer above the dataplane, so the
mesh never sees a request to observe.

**Correct remediation**: fix the caller's target back to the real Service name,
`http://payments:9090/` (Service `payments` in namespace `demo`), and roll the
`loadgen` Deployment. This is not a mesh fix: no VirtualService,
DestinationRule, or PeerAuthentication change helps, and editing mesh objects
for `payments` while the client is misconfigured is wasted or harmful effort.
Confirm the real name against `kubectl -n demo get svc`.

**How to find it**: the symptom is the *absence* of traffic, not bad traffic.
`kubectl -n demo logs deploy/loadgen` shows
`curl: (6) Could not resolve host: payments-svc.demo` on every iteration;
`kubectl -n demo get deploy loadgen -o yaml` (or `describe`) shows the typo in
the container args; `kubectl -n demo get svc` shows the real name is
`payments`. Prometheus corroborates negatively:
`rate(istio_requests_total{destination_service_name="payments"}[1m])` has
flat-lined to zero.

**Why this scenario exists**: every other scenario in this benchmark is a fault
that emits a pathological mesh signal: a 5xx surge, a UO overflow, a DENY at
ztunnel, a p99 blowout. MeshMedic's catalog is a set of threshold-and-hold
detectors over exactly those signals, and the v0.2 catalog was tuned against
these very scenarios, so it scores well on them: a structural home-field
advantage. This scenario is the honesty control for that bias. The fault here
is real and total (100% of user-facing calls fail), but it manifests as the
*disappearance* of telemetry, and no threshold over `istio_requests_total`,
latency histograms, retry counters, or TCP `DENY` flags can fire on an absence.
When this scenario was written it predicted **0 / 6 for MeshMedic by design**:
every catalog query is scoped to `payments`, all of them return no data once
traffic stops, and MeshMedic treated no-data as "quiet, not an incident", which
is correct for a mesh that genuinely has no traffic and wrong for a service
whose callers have all gone dark. MeshMedic scored 0 on the first run, as
predicted.

The prediction had a loophole, and the record of it is kept here rather than
edited away. An absence *is* detectable deterministically, with `or vector(0)`
plus a `max_over_time` baseline to prove traffic used to flow; and once a
detector knows traffic vanished, the caller's own logs and the most recent
rollout diff usually hold the root cause verbatim. MeshMedic's triage layer
does that, and it scores 6 / 6 here as of v1.0 (see
[results/meshmedic.md](../../../results/meshmedic.md)).

What the scenario measures is unchanged: breadth, meaning whether a tool can
diagnose a client-side fault that lives outside the mesh's own metrics, and
whether it resists inventing a mesh cause for a service that is healthy. The
0-point row still exists for exactly that failure.

## Scoring rubric

| points | detection | diagnosis | remediation |
| --- | --- | --- | --- |
| 0 | nothing flagged, or "payments looks fine" with no notice its traffic vanished | no cause named, or a mesh cause invented for payments (blames v1/v2, mTLS, a DestinationRule) | none, or a mesh patch to payments (VirtualService / DestinationRule / PeerAuthentication) |
| 1 | notices payments traffic dropped to zero, or that loadgen is failing | resolution / DNS failure named, misconfigured field not pinned | generic (restart loadgen, "check DNS", blind redeploy) |
| 2 | flags that loadgen's calls are failing and no traffic reaches payments | pins it to the wrong target host `payments-svc.demo` in the loadgen container command; payments itself healthy | set the loadgen target back to `http://payments:9090/` (Service `payments`) and roll it; explicitly not a mesh change |
