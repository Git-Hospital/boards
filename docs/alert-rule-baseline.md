# The alert-rule baseline

This records the answer to [architecture](architecture.md) section 9, question 4: where the plain-Prometheus alert-rule baseline comes from. The baseline is the adapter listed for v0.3.0 in [`decisions/0001-boards.md`](decisions/0001-boards.md). The adapter is not written yet. This page fixes its rule set and its definition of "alarmed" so the adapter cannot tune them (D10).

Everything below was read from the sources linked. Nothing has been run on the testbed. Claims about what a rule would do on the testbed are derived from reading and are marked so.

## Chosen rule set

The Istio group of [samber/awesome-prometheus-alerts](https://github.com/samber/awesome-prometheus-alerts), used unmodified, with all of its Istio rules, including the ones that cannot fire on this testbed.

| Item | Value |
| --- | --- |
| Repository | `samber/awesome-prometheus-alerts` |
| Commit | `e23e8777432ee8c0851c292ce6198e72586046cc` (2026-11-10) |
| File | `dist/rules/istio/embedded-exporter.yml` |
| URL at the commit | [`github.com/.../blob/e23e8777.../dist/rules/istio/embedded-exporter.yml`](https://github.com/samber/awesome-prometheus-alerts/blob/e23e8777432ee8c0851c292ce6198e72586046cc/dist/rules/istio/embedded-exporter.yml) |
| SHA-256 of the file | `7b5c1e3b658ac84a98c1567a565cca49e313e84853e5c2d8876bc1ae158c84d6` |
| Rules in the file | 11 |
| Licence | CC BY 4.0 for the rules and content (`_data/rules.yml`, `dist/rules/`, README); MIT for `site/`. See the [LICENSE at the commit](https://github.com/samber/awesome-prometheus-alerts/blob/e23e8777432ee8c0851c292ce6198e72586046cc/LICENSE). |

The file is already a Prometheus rule file (`groups:` with `alert:`, `expr:`, `for:`), so no conversion is needed. The project's own source is `_data/rules.yml`, a data format that is not a rule file; the `dist/` file is generated from it. The file's blob is byte-identical at the later commit `4b14ccb79cb8a9dc44f153766777031849e78c0e` (the head of `master` when checked, 2026-11-13); it was last changed by commit `549dd0175a45c53e3ff15b4a549f7d9358d2e9f3` (2026-09-07). The adapter downloads the file from the URL above and refuses to run if the SHA-256 differs. The file is not copied into this repository in this change.

Attribution, as CC BY 4.0 requires. The upstream LICENSE at the pinned commit names no copyright holder; it only states the dual licence. The notice is therefore: rules from samber/awesome-prometheus-alerts (https://github.com/samber/awesome-prometheus-alerts) at commit e23e8777432ee8c0851c292ce6198e72586046cc, licensed [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/), used without changes. A vendored copy of the file carries this notice next to it, and a report that uses this baseline repeats it.

The eleven rules, by `alert:` name and what each reads:

| Rule | Reads | `for` |
| --- | --- | --- |
| `IstioControlPlaneComponentDown` | `absent(istio_build{component="pilot"})` | 1m |
| `IstioPilotDuplicateEntry` | `pilot_duplicate_envoy_clusters` | 0m |
| `IstioKubernetesGatewayAvailabilityDrop` | `kube_deployment_status_replicas_available{deployment="istio-ingressgateway", namespace="istio-system"}` | 1m |
| `IstioPilotHighPushErrorRate` | `pilot_xds_push_errors`, `pilot_xds_pushes` | 1m |
| `IstioMixerPrometheusDispatchesLow` | `mixer_runtime_dispatches_total` (the file's own comment: Mixer was removed in Istio 1.8) | 1m |
| `IstioHighTotalRequestRate` | `istio_requests_total{reporter="destination"}`, over 1000 req/s, `[5m]` | 2m |
| `IstioLowTotalRequestRate` | `istio_requests_total{reporter="destination"}`, under 100 req/s, `[5m]` | 2m |
| `IstioHigh4xxErrorRate` | `istio_requests_total{reporter="destination"}`, 4xx share over 5%, `[5m]` | 1m |
| `IstioHigh5xxErrorRate` | `istio_requests_total{reporter="destination"}`, 5xx share over 5%, `[5m]` | 1m |
| `IstioHighRequestLatency` | `istio_request_duration_milliseconds_sum/_count{reporter="destination"}`, mean over 100 ms, `[1m]` | 1m |
| `IstioLatency99Percentile` | `istio_request_duration_milliseconds_bucket`, no `reporter` filter, by `destination_canonical_service`, `destination_workload_namespace`, over 1000 ms, `[1m]` | 1m |

## Why this set

- It was not written by us. Nobody on this project can tune it, which is the requirement in question 4.
- Of the sets we found, its request rules use `for:` of 1 to 2 minutes, which fits case windows of 5 to 8 minutes. It also ships as a ready Prometheus rule file under a licence that allows reuse with attribution. It is widely used; we did not record a star count.
- Pinning by commit and SHA-256 makes a later upstream edit visible: the adapter stops instead of silently running different rules.
- Using all eleven rules unmodified keeps the choice of rules out of our hands. Dropping the rules that cannot fire here would be a choice, and it would flatter or handicap the baseline.

## Considered and rejected

- **Istio-2 mixin from Grafana ([grafana/jsonnet-libs `istio-2-mixin`](https://github.com/grafana/jsonnet-libs/tree/cd5bb6f/istio-2-mixin), commit `cd5bb6fca4015294d4dfda1014a1386c447241b7`).** Apache-2.0 ([LICENSE](https://github.com/grafana/jsonnet-libs/blob/cd5bb6f/LICENSE)), seven alerts in [`prometheus_alerts.yaml`](https://github.com/grafana/jsonnet-libs/blob/cd5bb6f/istio-2-mixin/prometheus_rules_out/prometheus_alerts.yaml). Its request alerts (latency, HTTP errors, gRPC errors) filter `reporter="source"`, use `increase(...[5m])` and `for: 5m`. The testbed's waypoint reports `reporter="waypoint"` (every case oracle under `cases/mesh/` filters on it), so those alerts would not see waypoint traffic. The ingress gateway's Envoy may report `reporter="source"` (unverified), so only gateway traffic may reach them. A `5m` window plus `for: 5m` can take up to about 10 minutes to fire (a ratio over `increase(...[5m])` can cross its threshold sooner), while the cases give 5 to 8 minutes after a 90 s to 2 minute settle. Rejected because the request rules would most likely not see the waypoint traffic and are likely slow against these windows, not because they are worse rules.
- **An adapted rule set** (for example rewriting `reporter=` to `waypoint`, or shortening windows). Rejected because the adapted rules would be written by us, which is what question 4 forbids.
- **Rules published by Istio, Kiali, kube-prometheus or the prometheus-community Helm charts.** We found none. This was a quick search, not proof that none exist.

## How the adapter decides "alarmed"

The pinned file is loaded unmodified into a Prometheus that evaluates it against the testbed's metrics. The tool has alarmed when any rule of the set is `firing` inside the case window. The adapter reads this from the Prometheus [alerts API](https://prometheus.io/docs/prometheus/latest/querying/api/#alerts) (`/api/v1/alerts`) or the [rules API](https://prometheus.io/docs/prometheus/latest/querying/api/#rules) (`/api/v1/rules`). A rule that is only `pending` has not alarmed. The alert times become the `events` in the verdict, so time to detect is measured against boards' own clock.

Culprits: the workload named by a firing alert's own labels, when the rule keeps such labels, and none otherwise. Nothing is inferred (D10). In the pinned file the rules differ. `IstioLatency99Percentile` keeps `destination_canonical_service` and `destination_workload_namespace`. `IstioHighRequestLatency` has no aggregation, so it keeps the labels of the series it reads (including workload labels), but it only reads `reporter="destination"`. `IstioKubernetesGatewayAvailabilityDrop` uses `min(...) without (instance, pod)`, so it keeps the deployment's other labels. The remaining rules aggregate with `sum(...)` and keep none, or (`IstioControlPlaneComponentDown`, `absent(...)`) keep only the labels of the matcher. A canonical service name is not a Deployment name, so whether it counts as a named workload belongs to the adapter's `culprits` mapping, which is frozen per suite and offered to the rules' maintainers for review (D10).

Left to the adapter's own pull request:

- How the rule file reaches a Prometheus without changing what the other tools see. The vendored config lists `/etc/config/rules` and `/etc/config/alerts` under `rule_files` ([`istio-1.24.1-prometheus.yaml`](../testbed/mesh/vendor/istio-1.24.1-prometheus.yaml), lines 44-48), but putting the file into the testbed's Prometheus would make every tool's Prometheus evaluate it, and that file is pinned by [`testbed.lock`](../testbed/testbed.lock). A second Prometheus or a separate rule evaluator is an option. Not decided here.
- The `adapter.yaml` fields (`alarm`, `culprits`, `warmup`). The rules use `[5m]` windows, so `warmup` must cover at least that.
- Evaluation interval: the vendored config sets `evaluation_interval: 1m` (line 41). A rule with `for: 1m` can take up to about two evaluations to fire. Whether the baseline's Prometheus keeps 1m is the adapter's choice, and it changes time to detect.

## Which rule reads each fault on the testbed

The testbed runs Istio 1.24.1 in ambient mode. A waypoint is applied to namespace `demo` ([`internal/testbed/kind.go`](../internal/testbed/kind.go), step "waypoint": `istioctl waypoint apply -n demo --enroll-namespace`), and the case oracles read `reporter="waypoint"`. Namespace `demo-sidecar` ([`sidecar-orders.yaml`](../testbed/mesh/app/sidecar-orders.yaml)) runs sidecars and reports `reporter=destination`. No case injects a fault there. The north-south ingress gateway ([`ingress.yaml`](../testbed/mesh/app/ingress.yaml), Gateway `ingress`, driven by `ingress-loadgen`) is also an Envoy that emits request metrics. Its `reporter` value is unverified here. `IstioLatency99Percentile` has no `reporter` filter, so it reads those series too.

All entries below are derived from reading the rules and the case files. None is measured.

| Case | Oracle reads | Rule that could detect it (derived) |
| --- | --- | --- |
| `authz-deny-flood` | 403 at the waypoint | None. The 4xx rule filters `reporter="destination"`, so it does not see waypoint traffic. |
| `canary-latency` | p99 of v2 over 1000 ms, waypoint | `IstioLatency99Percentile`. It has no `reporter` filter and reads waypoint series. About 20% of requests go to the slowed v2 (the VirtualService split is 80/20), so the service-wide p99 should exceed 1000 ms. It names the service, not v2. |
| `client-dns-typo` | loadgen log, waypoint traffic below 0.1 req/s | None. The request never reaches the mesh, so no series is produced. No rule tests for absent request metrics. |
| `client-wrong-port` | loadgen log, waypoint traffic below 0.1 req/s | None, same reason. |
| `client-wrong-scheme` | loadgen log, waypoint traffic below 0.1 req/s | None, same reason. |
| `error-surge` | 5xx share over 15%, waypoint | None. The 5xx rule filters `reporter="destination"`. |
| `fault-injection-left` | `response_flags="FI"`, waypoint | None. The injected 503s are waypoint series; the 5xx rule does not see them. |
| `mtls-conflict` | `exists`/`exec` checks, no PromQL | None. The rejection happens at ztunnel layer 4 and never becomes a request metric (see [ambient-l4-denial-telemetry.md](ambient-l4-denial-telemetry.md)). The rules read no ztunnel TCP metric. |
| `noise-only` | control, nothing to detect | Any firing is a false alarm. See the next table. |
| `pool-overflow` | `response_flags="UO"`, no `reporter` filter | `IstioLatency99Percentile` only if queueing pushes the p99 over 1000 ms; reading cannot settle that. The 5xx rule does not see the waypoint's rejections. |
| `route-timeout-too-short` | `response_flags="UT"`, waypoint | None. The 504s are waypoint series, and the requests time out at 10 ms, so no latency rule fires. |

Summary of the derived reading: only `IstioLatency99Percentile` has an expression that can read a fault: `canary-latency`, and `pool-overflow` if queueing pushes the p99 over 1000 ms. The other eight fault cases have no rule that reads them. This says which rule's expression reflects each fault, not the scored outcome. Five of the eleven rules filter `reporter="destination"`, which the waypoint does not produce; whether that shows up in a live run is not measured.

The scored outcome can differ from the table. An alarm is any rule `firing` in the window, and an alarm before the defect is observable counts as immediate (architecture section 5). If `IstioLowTotalRequestRate` fires throughout, as expected (derived, unmeasured), every fault twin counts as alarmed with a time to detect of zero, every clean twin counts as a false alarm, the alarm never clears, and the firing rule names no culprit. The count of alarmed fault twins then says nothing about the rules. The same holds for `IstioControlPlaneComponentDown` if istiod is not scraped. The report therefore shows the baseline per rule as well as for the whole set, so one noisy rule is visible. The alarm definition stays: any rule firing.

Expected behaviour of the other rules on a testbed with no fault. Derived from reading, unmeasured:

| Rule | Expectation |
| --- | --- |
| `IstioLowTotalRequestRate` | Likely to fire in clean twins and in `noise-only`. Each loadgen runs `curl` in a loop with `sleep 0.2` ([`payments.yaml`](../testbed/mesh/app/payments.yaml), [`sidecar-orders.yaml`](../testbed/mesh/app/sidecar-orders.yaml)), so each sends at most about 5 req/s, far below 100. It needs `reporter="destination"` series to exist; `demo-sidecar` produces them. The rule's own comment says it may fire in low-traffic environments. If it fires in a clean twin it counts as a false alarm for the baseline. This is expected, not a defect of the adapter, and it is reported as is. |
| `IstioControlPlaneComponentDown` | Fires if istiod's metrics are not scraped. The vendored Prometheus has no static job for istiod; it scrapes by annotation (`prometheus.io/scrape`) in the `kubernetes-pods` and `kubernetes-service-endpoints` jobs. Whether `istioctl install` puts those annotations on istiod, and so whether `istio_build{component="pilot"}` exists, is not checked in this repository and is unverified here. |
| `IstioKubernetesGatewayAvailabilityDrop` | Probably silent. It needs a deployment named `istio-ingressgateway`; the ambient profile and [`ingress.yaml`](../testbed/mesh/app/ingress.yaml) use a Gateway API gateway named `ingress`. `kube-state-metrics` is deployed, so an empty result is the expected case. Unverified. |
| `IstioMixerPrometheusDispatchesLow` | Cannot fire: Mixer metrics do not exist in Istio 1.24.1 (per the rule's own comment). |
| `IstioHighTotalRequestRate`, `IstioHigh4xxErrorRate`, `IstioHigh5xxErrorRate`, `IstioHighRequestLatency` | Expected silent: no case drives 1000 req/s, and the only `reporter="destination"` traffic is healthy `demo-sidecar` traffic. Unmeasured. |
| `IstioPilotDuplicateEntry`, `IstioPilotHighPushErrorRate` | Control plane rules; no case injects a duplicate cluster or push error. Unmeasured. |

## What is still needed

A live run on the testbed is needed to confirm both tables. The result of a near-silent baseline is reported with the reason per rule: blind by `reporter` filter, no such metric, window too long, or no fault of that kind injected. A baseline that mostly stays silent, or has one rule that fires in clean twins, is reported as it is, with the reason per rule.
