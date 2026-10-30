# Response-flag coverage map

The mesh failure vocabulary is public and finite: Envoy stamps every request
with a `response_flags` code, and Istio surfaces it on `istio_requests_total`.
Harvesting that vocabulary and mapping it against the catalog gives a grounded,
non-imaginary picture of what MeshMedic covers and where the gaps are.

Source: Envoy access-log RESPONSE_FLAGS reference (substitution formatter docs,
read 2026-07-18). The list below is the documented vocabulary; a flag becomes a
catalog entry only after it is injected on the testbed and the signal observed,
because a flag existing in the spec does not mean it appears on this testbed's
telemetry (some need infrastructure the single-service ambient testbed lacks).

## Covered (validated live)

| flag | meaning | catalog entry |
| --- | --- | --- |
| `DENY` | RBAC/authorization denial | mtls-policy-conflict-ambient, authz-deny-flood |
| `RL` | Rate limited | rate-limit-throttling (EnvoyFilter local rate limit on the payments waypoint) |
| `UAEX` | External authorization denied | external-authz-denial (ext_authz filter calling a deny-all service; distinct from the native-policy DENY above) |
| `UH` | No healthy upstream | upstream-host-ejection-flood (ejection or subset mismatch) |
| `UO` | Upstream overflow (circuit breaking) | connection-pool-overflow |
| `UT` | Upstream request timeout | route-timeout-too-short |
| `FI` | Fault injected | fault-injection-left-in-production |
| `NR` | No route found | no-route-blackhole (source-keyed: NR carries `destination_service_name=unknown`, so the signal keys on `source_workload_namespace`, confirmed live) |

## Gap, prioritized for validation

Ranked by incident frequency and whether the single-service ambient testbed can
inject them cleanly.

| flag | meaning | failure class | inject on testbed? | priority |
| --- | --- | --- | --- | --- |
| `NR` | No route found | ~~missing/broken VirtualService route~~ | **validated and merged** as `no-route-blackhole` (source-keyed) | done |
| `DF` | DNS resolution failed | proxy-side DNS failure to a destination | yes: point a ServiceEntry/host at an unresolvable name | high (also the mesh-native counterpart to the client-side `client-dns-typo`) |
| `UF` | Upstream connection failure | backend unreachable at the connection layer | yes: wrong port / backend down | high (overlaps client-wrong-port triage; UF is the telemetry-native signal) |
| `URX` | Upstream retry limit exceeded | retries exhausted against a flaky backend | **no on this testbed** (measured 2026-08-19): `envoy_cluster_upstream_rq_retry` exists with 5 series, all `cluster_name="xds-grpc"` (a proxy's own connection to istiod). There is no application-cluster retry counter to read, so `retry-storm-damping` cannot fire here regardless of injected fault | blocked until application-cluster retry counters are exposed |
| `UC` | Upstream connection termination | backend closed the connection mid-request | yes: crash/kill the backend under load | medium |
| `UR` | Upstream remote reset | backend sent a TCP reset | yes: backend closes abruptly | medium |
| `UPE` | Upstream protocol error | protocol mismatch (h2/h1, TLS) | partial: overlaps client-wrong-scheme | medium |
| `SI` | Stream idle timeout | idle stream cut by the proxy | yes-ish: slow/stalled backend | low |
| `DT` | Duration timeout | request exceeded max stream duration | yes-ish: max-duration policy + slow backend | low |

## Deferred - need infrastructure the testbed lacks

| flag | why deferred |
| --- | --- |
| `RLSE` | needs a ratelimit *service*; the local rate-limit filter behind `RL` is now on the testbed and validated |
| `OM`, `DO`, `UDO` | need the overload manager / load-shedding under real memory pressure |
| `LH` | needs active health checking configured on the cluster |
| `NC` | cluster-not-found is usually a config-load error, not a runtime traffic signal |
| `IH`, `DPE`, `DC`, `LR`, `DR` | downstream/client-side or header-level; low mesh-diagnosis value on this testbed |

## How this feeds the pipeline

`NR`, `DF`, and the `UF`/`UC`/`UR` connection-failure family are the high-value
gap: common real failures with a clean telemetry signal. They go through the
same gate as every entry - inject on the testbed, observe the real flag and
labels, only then a catalog entry or bench scenario. `DF` is worth doing first:
it is both a coverage gap and the mesh-native signal for the client-DNS failure
class, so validating it also enriches that lane.
