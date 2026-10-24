# client-wrong-port: ground truth

**Fault**: a bad client rollout repoints the `loadgen` caller at
`payments:9091`, a port the payments Service does not expose (it listens on
9090). The host resolves, ztunnel accepts the TCP connection, but the closed
backend port returns an empty reply, so every call fails and payments' L7
request rate drops to zero while payments itself is healthy.

**Root cause**: a client configured to call the wrong port. This is the
DNS-typo scenario's sibling with a different signature: the host resolves,
so there is no NXDOMAIN; the failure is `curl: (52) Empty reply from server`.

**Correct remediation**: none in the mesh; fix the client's target port back
to 9090. The correct output is a triage dossier: the absence of traffic, the
empty-reply signature in the client's own logs, and the rollout diff showing
the 9090 -> 9091 change.

**Why this scenario exists**: it proves the traffic-vanished triage layer
generalizes beyond the DNS typo to any wrong-target client deploy, on a
signature (empty reply) distinct from NXDOMAIN. Catalog-based tools that
only watch request metrics see nothing, because the traffic stopped.

## Scoring rubric

| points | detection | diagnosis | remediation |
| --- | --- | --- | --- |
| 0 | nothing flagged, or "payments looks fine" | no cause named, or a mesh cause invented for payments | a mesh patch to payments |
| 1 | notices payments traffic dropped, or loadgen failing | connection/port failure named, field not pinned | generic (restart loadgen, blind redeploy) |
| 2 | flags loadgen failing and no traffic reaching payments | pins the wrong target port in the loadgen command | set the loadgen target back to port 9090; explicitly not a mesh change |
