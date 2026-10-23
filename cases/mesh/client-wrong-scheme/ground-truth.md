# client-wrong-scheme: ground truth

**Fault**: a bad client rollout makes `loadgen` speak https to `payments`,
which serves plain HTTP. The TLS handshake receives an HTTP response and
fails (`curl: (35) TLS connect error`), every call fails, and payments' L7
request rate drops to zero while payments itself is healthy.

**Root cause**: a client configured with the wrong scheme (https for an
http-only backend). The DNS-typo and wrong-port scenarios' sibling with a
third signature: the host resolves and the port is open, but the transport
is wrong.

**Correct remediation**: none in the mesh; fix the client's scheme back to
http. The dossier is the deliverable: the absence of traffic, the TLS-error
signature in the client's logs, and the rollout diff.

## Scoring rubric

| points | detection | diagnosis | remediation |
| --- | --- | --- | --- |
| 0 | nothing flagged | no cause named, or a mesh cause invented | a mesh patch to payments |
| 1 | notices payments traffic dropped, or loadgen failing | TLS/transport failure named, field not pinned | generic (restart, blind redeploy) |
| 2 | flags loadgen failing and no traffic reaching payments | pins the wrong scheme (https) in the loadgen command | set the loadgen scheme back to http; not a mesh change |
