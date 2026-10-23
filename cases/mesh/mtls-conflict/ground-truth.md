# mtls-conflict: ground truth

**Fault**: the `demo` namespace is switched to STRICT mTLS while a client
outside the mesh (no sidecar, not enrolled in ambient) keeps calling
`payments` in plaintext. The client's requests fail from that moment on.

**Root cause**: mTLS policy conflict: STRICT PeerAuthentication versus a
plaintext, out-of-mesh caller.

**Correct remediation**: enroll the client into the mesh (ambient label or
sidecar), or apply a scoped PERMISSIVE PeerAuthentication on the affected
workload as an explicit, temporary de-escalation while the client migrates.

**Known hard case**: in ambient mode the rejection happens at L4 (ztunnel),
so it never appears in L7 request metrics. Metric-only tools watching
request telemetry are expected to struggle here; that is the point of the
scenario. The signal does exist, one layer down, and is scraped by the
stock Istio Prometheus addon: ztunnel reports
`istio_tcp_connections_closed_total{reporter="destination",
response_flags="DENY", connection_security_policy="unknown"}` with full
source/destination labels (including `source_principal="unknown"` for the
identity-less client), and its access logs mark the same denials. Tools
are scored on finding it, not on which of those surfaces they use.

## Scoring rubric

| points | detection | diagnosis | remediation |
| --- | --- | --- | --- |
| 0 | nothing flagged | no cause named | none or harmful |
| 1 | client failures flagged | TLS suspected, policy missed | generic (restart, network debug) |
| 2 | rejected plaintext flagged | STRICT vs plaintext client named | enroll client or scoped PERMISSIVE |

## Finding the signal

A practical reference for detecting this in Prometheus (exact metric,
labels, PromQL, and the access-log alternative) is in
[docs/ambient-l4-denial-telemetry.md](../../../docs/ambient-l4-denial-telemetry.md).
