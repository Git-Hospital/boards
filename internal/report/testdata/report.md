# boards report: dummy-tool 1.2.3

- Suite fixed, boards 0.0.0-test, testbed dry-run (**not standard**: this result never enters the official table)
- Tool category: detector; adapter: `adapters/dummy/tool`
- Ran 2026-10-30T08:00:00Z to 2026-10-30T08:01:30Z

There is no total: each axis is reported on its own, with the cases it could not score counted beside it.

## Summary

| axis | rate | pass | partial | fail | null |
|---|---|---|---|---|---|
| detection | 75% | 3 | 0 | 1 | 0 |
| diagnosis | 33% | 1 | 1 | 1 | 0 |
| remediation | 100% | 1 | 0 | 0 | 1 |
| resolves | 100% | 1 | 0 | 0 | 0 |
| cleanTwin | 67% | 2 | 0 | 1 | 0 |
| nullHonesty | 100% | 1 | 0 | 0 | 0 |

## Summary, contaminated cases (the tool was developed against these; they carry no standing)

| axis | rate | pass | partial | fail | null |
|---|---|---|---|---|---|
| detection | 100% | 1 | 0 | 0 | 0 |
| diagnosis | 0% | 0 | 0 | 1 | 0 |

1 case(s) were inconclusive: the testbed could not put the tool in front of the defect, so nothing was scored.

## Cases

| case | set | detection | diagnosis | remediation | resolves | cleanTwin | nullHonesty |
|---|---|---|---|---|---|---|---|
| `mesh-all-pass` | public | pass | pass | pass | pass | pass |  |
| `mesh-partial` | public | pass | partial (culprit named \| quiet neighbour blamed) |  |  | pass |  |
| `mesh-fail` | holdout | fail (no alarm within 5m0s) | fail (no alarm) |  |  | fail (alarm on the clean twin) |  |
| `mesh-null-axis` | public | pass |  | null (tool proposes no fixes) |  |  | pass |
| `mesh-contaminated` | public, contaminated | pass | fail (wrong culprit) |  |  |  |  |
| `mesh-inconclusive` | public |  |  |  |  |  |  |
