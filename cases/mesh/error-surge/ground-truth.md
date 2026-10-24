# error-surge: ground truth

**Fault**: the `v2` subset of `payments` answers 90% of its requests with
HTTP 500. The service-level 5xx ratio settles around 18% because `v2`
takes only 20% of traffic.

**Root cause**: failing endpoints scoped to the `v2` subset; `v1` is clean.

**Correct remediation**: eject the failing endpoints from load balancing
(DestinationRule outlier detection) or shift traffic off the subset /
roll `v2` back. Restarting pods does not help; the fault is in the build.

## Scoring rubric

| points | detection | diagnosis | remediation |
| --- | --- | --- | --- |
| 0 | nothing flagged | no cause named | none or harmful |
| 1 | 5xx on payments flagged | errors named, subset missed | generic (restart, scale) |
| 2 | error surge flagged with rate | 500s pinned to subset v2 | outlier ejection, shift, or rollback |
