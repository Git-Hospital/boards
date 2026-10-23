# authz-deny-flood: ground truth

**Fault**: an AuthorizationPolicy in the `demo` namespace denies the
`loadgen` caller's requests to `payments` at the waypoint. The caller has a
valid mesh identity (`spiffe://cluster.local/ns/demo/sa/default`), so this
is an authorization decision, not an mTLS handshake failure. loadgen's calls
return HTTP 403; payments itself is healthy.

**Root cause**: an authorization policy rejecting a legitimate caller, from
a DENY rule broader than intended or a default-deny namespace missing an
ALLOW rule.

**Correct remediation**: none applied automatically. Whether the denial is a
mistake or intended is an operator judgment; auto-loosening authorization
could open a security hole. The correct output is a dossier naming the
denied caller and listing the namespace's AuthorizationPolicies so the
operator can see which policy denies and decide. If the denial is a mistake,
the fix is a scoped ALLOW for the caller or a correction to the DENY rule.

**Known distinction**: this is HTTP 403 with a valid `source_principal`, not
the 503/DENY of an mTLS handshake failure. A tool that conflates the two
(proposing a PERMISSIVE mTLS fallback for an authorization denial) scores 0
on diagnosis.

## Scoring rubric

| points | detection | diagnosis | remediation |
| --- | --- | --- | --- |
| 0 | nothing flagged | no cause named, or blamed on mTLS/network | proposes a harmful auto-loosen, or an mTLS fix |
| 1 | 403s flagged | authorization suspected, policy not named | generic (restart, "check RBAC") |
| 2 | 403 flood on payments flagged | authorization denial named, denied caller identified | dossier of the denying policies; no blind auto-loosen |
