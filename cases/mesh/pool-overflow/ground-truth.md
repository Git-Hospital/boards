# pool-overflow: ground truth

**Fault**: the `payments` DestinationRule clamps the connection pool to one
connection and one pending request while load and per-request latency rise.
Envoy's circuit breaker sheds the excess with 503s flagged `UO`
(upstream overflow).

**Root cause**: connection pool limits undersized for current traffic;
the workload itself has headroom.

**Correct remediation**: raise or remove the connection pool limits in the
DestinationRule (after confirming the destination has CPU headroom).
Restarting or scaling `payments` does not stop the shedding; the limit is
enforced at the mesh layer.

## Scoring rubric

| points | detection | diagnosis | remediation |
| --- | --- | --- | --- |
| 0 | nothing flagged | no cause named | none or harmful |
| 1 | 503s flagged | overload named, limit missed | generic (restart, scale) |
| 2 | UO shedding flagged | DR pool limit identified | resize the pool in the DestinationRule |
