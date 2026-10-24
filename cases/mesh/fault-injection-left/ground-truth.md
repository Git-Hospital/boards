# fault-injection-left: ground truth

**Fault**: the `payments` VirtualService injects a 50% abort (503) into live
traffic, a leftover chaos-test or staging config. Requests fail with the FI
(fault injected) response flag while the endpoints are healthy.

**Root cause**: a fault-injection stanza left on a production VirtualService.

**Correct remediation**: remove the fault stanza from the named VirtualService,
preserving the routes. The failures are synthetic, so outlier ejection or a
backend fix would both be wrong.

**Known distinction**: the FI flag distinguishes this from a genuine 5xx
surge. A tool that proposes outlier ejection (treating the 503s as bad
endpoints) or a backend fix scores 0 on remediation.

## Scoring rubric

| points | detection | diagnosis | remediation |
| --- | --- | --- | --- |
| 0 | nothing flagged | genuine backend/endpoint failure assumed | outlier ejection or a backend change |
| 1 | 503s flagged | fault injection suspected, VirtualService not named | generic ("check the config") |
| 2 | FI flood flagged | fault-injection config named as the cause | name the VirtualService and its fault stanza to remove |
