# route-timeout-too-short: ground truth

**Fault**: the `payments` VirtualService has a route timeout (10ms) shorter
than the backend's real response time (~20ms). Requests are cut off and
returned as HTTP 504 with the UT (upstream request timeout) response flag,
while payments itself is healthy and would have answered.

**Root cause**: a VirtualService route timeout set below the backend's p99
latency.

**Correct remediation**: raise the route timeout on the named VirtualService
above the backend's p99, or remove it to fall back to the mesh default,
preserving the existing routes and weights. No safe automatic patch exists
without the operator's routing intent; the correct output pins the offending
VirtualService and its timeout next to the measured backend latency.

**Known distinction**: this is 504 with the UT flag, a route-level timeout,
not a 503 (upstream unavailable) or a backend 5xx. A tool that blames the
backend for a route-config fault scores 0 on diagnosis.

## Scoring rubric

| points | detection | diagnosis | remediation |
| --- | --- | --- | --- |
| 0 | nothing flagged | no cause named, or blamed on the backend | proposes a backend fix or an unrelated change |
| 1 | 504/timeout flagged | timeout suspected, VirtualService not named | generic ("increase timeout somewhere") |
| 2 | UT flood on payments flagged | route timeout named as the cause | name the VirtualService and its short timeout vs the backend p99 |
