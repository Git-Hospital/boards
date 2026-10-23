# noise-only: ground truth

**Fault**: none. The payments service is healthy and stays healthy for the
whole window. What the cluster does contain is deliberate, realistic,
harmless noise:

- an empty ConfigMap (`legacy-feature-flags`) in the demo namespace
- a Service with no matching endpoints (`payments-legacy`), the classic
  leftover of a retired workload
- a completed one-off pod (`one-off-migration`, exit 0) and the benign
  events it produced

**Root cause**: there is no incident. The correct diagnosis is that the
service is healthy and the noise is harmless.

**Correct remediation**: none. Optionally, housekeeping suggestions clearly
labeled as non-urgent cleanup are acceptable, as long as no fix is proposed
for the payments service itself.

**Why this scenario exists**: every real cluster is full of things that look
vaguely wrong. An investigator that treats each of them as the incident
produces alert fatigue and, worse, "fixes" for problems that do not exist.
That is the failure mode this benchmark's other scenarios cannot see. This scenario
measures discipline: knowing when NOT to act. Threshold-and-hold detectors
pass it by construction only if their thresholds are honest; agentic
investigators pass it only if they can conclude "nothing is wrong" and stop.

## Scoring rubric

The detection axis is inverted here: silence is the correct answer.

| points | detection | diagnosis | remediation |
| --- | --- | --- | --- |
| 0 | reports an incident on payments | names a root cause for a non-problem | proposes or applies a fix to payments |
| 1 | flags the noise as a possible incident, hedged | inspects the noise, leaves its harmlessness unclear | generic advice presented as needed now |
| 2 | reports no incident (or stays quiet) | states the service is healthy and the noise is harmless | none, or cleanup explicitly labeled optional |
