# canary-latency: ground truth

**Fault**: the canary subset (`payments` version `v2`, receiving 20% of
traffic through a VirtualService split) serves with p50 around 1200ms while
the stable subset stays around 20ms.

**Root cause**: a latency regression scoped to the `v2` subset. Not a
capacity, node, or dependency problem; `v1` on the same node stays fast.

**Correct remediation**: shift traffic back to the stable subset by editing
the VirtualService weights (100/0), then investigate the canary offline.
Rolling back the `payments-v2` deployment is also accepted.

## Scoring rubric

| points | detection | diagnosis | remediation |
| --- | --- | --- | --- |
| 0 | nothing flagged | no cause named | none or harmful |
| 1 | generic "payments unhealthy" | latency named, subset missed | generic (restart pods, scale up) |
| 2 | latency regression flagged | regression pinned to subset v2 | VirtualService shift or v2 rollback |
