# 0001: Turn mesh-incidents-bench into boards, the Git-Hospital benchmark

**Status:** accepted, 23 October 2026; v0.2.0 is being built against it. **Decided by:** the maintainer.

Each decision gives the option chosen, then the options considered and rejected, so the reasoning stays visible.

**Inputs:**
- an internal review of the four Git-Hospital tools (22-23 October 2026). Its findings on this repository are summarised under "Why".
- GitHealthCheck's [0001: Rebuild GitHealthCheck in Go](https://github.com/Git-Hospital/githealthcheck/blob/main/docs/decisions/0001-go-rewrite.md), whose process and "measured or null" rule this record follows.
- [`docs/architecture.md`](../architecture.md), which turns these decisions into boundaries for the code.
- Helmbulance's practices as the target: reviewed PRs, defect/fixed fixture pairs, a versioned report, exit codes 0/1/2/3, and a pinned corpus.

## Why

Today there are two benchmarks, and neither can be trusted on its own.

- **mesh-incidents-bench** (this repository, about 600 lines of Bash and Python) holds 11 Istio scenarios. Each is `inject.sh`, `check.sh`, `reset.sh` and a `ground-truth.md` rubric, and a human scores detection, diagnosis and remediation from 0 to 2.
- **meshmedic-prove** (in the MeshMedic repository, about 4,000 lines of Go) re-describes many of the same faults as `proof/*.yaml`: 16 proofs, plus 2 entries declared unprovable. Its four assertions are machine-checked: fired within the hold duration, named the culprit, kept declared neighbours quiet, resolved on reset. It can only measure MeshMedic.

The same faults are maintained twice by hand. One copy is public but scored by a person, the other is machine-scored but tied to one tool.

The review found these problems, each checked against the repository:

- **66/66 is a home game.** The author's own tool, on scenarios developed alongside it, scored by its author (`results/meshmedic.md`). The `pool-overflow` and `noise-only` scores break the repository's own rubric.
- **Remediations were never applied.** A fix scored 2 was never run against the testbed. One of MeshMedic's patches likely does not override the faulty DestinationRule.
- **There is one human scorer**, **and results are hand-written Markdown** with no machine-readable form.
- **The testbed is unpinned,** and nothing runs a cluster in CI. CI is shellcheck only.
- **Configuration is copied into every script.** `127.0.0.1:9090` is hard-coded in 10 of 11 `check.sh` files; the context and namespace are baked into `harness/run.sh` and every `inject.sh`.
- **The README contradicts the tree.** It says the HolmesGPT and k8sgpt comparison was removed, yet four adapters are tracked, and two of them read a personal path (`~/.holmes.env`).
- **The istioctl comparison covers 1 of 11 scenarios**.

The decision is to merge both into one benchmark for every Git-Hospital tool. Like GitHealthCheck, its rule is that every number it publishes is a measurement.

## Decisions

### Product

#### D1. Which tools does the benchmark measure?
**The tools of every Git-Hospital domain.** MeshMedic, Helmbulance and GitHealthCheck, each measured in its own domain (D5).

Considered and rejected:

- Only service-mesh incident tools (MeshMedic, istioctl, alert rules, k8sgpt, HolmesGPT).
- All of Kubernetes, including non-mesh incidents.

All three tools answer one question: does the doctor find the disease? A target (a live mesh, a chart, a repository) gets a known defect, and the tool is scored on finding it and on staying quiet on a clean twin. That holds for mesh and Helm; the code track measures accuracy instead (D22). Helmbulance's defect/fixed fixture pairs and this repository's `noise-only` scenario are already that method.

#### D2. What people use it for
**Both, in order.** v1 is the comparison mode, on a testbed boards builds itself. The case format is designed from the start not to depend on that testbed, so the drill mode (`--target-context`, D17) arrives in v2.

Considered and rejected:

- Only comparison on a fixed testbed.
- A fire drill on your own cluster first.

#### D3. MeshMedic and meshmedic-prove
**boards owns everything.** It takes over the scenarios, the payments/ledger testbed app, the runner, and meshmedic-prove's four machine assertions. MeshMedic enters as an ordinary candidate through an adapter (D10), with no special path in the harness. MeshMedic pins a boards release to test itself.

Considered and rejected:

- Separate repositories that share a case format.
- Move the benchmark into the MeshMedic repository.

#### D4. Language of the runner
**Go.** It matches MeshMedic, Helmbulance and GitHealthCheck: one static binary, `go install`, and the same CI and release pipeline. Inject steps stay plain commands (`kubectl ...`) inside the case file. Code shared with meshmedic-prove is copied into `internal/` with attribution, not imported, so the benchmark never depends on a tool it scores (architecture §2).

Considered and rejected:

- Python.
- Bash, as today.

#### D5. The shape of a multi-tool benchmark
**A shared core and one track per domain, with rivals in every track.**
- The core is the case format, runner, scoring, report and leaderboard.
- The tracks are `mesh`, `helm` and `code`. Each track measures the Git-Hospital tool next to that domain's established tools, on the same cases.

Considered and rejected:

- Only an internal regression gate for the three Git-Hospital tools.
- A general core, with only the mesh track in v1.

#### D6. Track order
**Mesh, then Helm, then code.**
- The mesh material exists: 11 scenarios, 16 proofs and raw outputs from August 2026.
- Helmbulance has a corpus and defect/fixed pairs.
- The code track waits for GitHealthCheck's Go rewrite, since a tool that does not exist cannot be measured.

Considered and rejected:

- A thin slice of all three at once.
- Mesh and code first, Helm later.

#### D7. Where Helmbulance's and GitHealthCheck's cases live
**Practice in the tool, exams in boards.** Each tool keeps its own fixture pairs as unit tests: the training set it is developed against. boards writes its own independent exam cases, which the tool's author did not develop against.

Considered and rejected:

- Move them all into boards, and the tools pin a boards release.
- Reference them by commit SHA, without copying.

#### D8. Name
**`boards`**, after the board exam a doctor sits for a specialty licence. The tools sit the exam. The repository is `Git-Hospital/boards`, the module is `github.com/Git-Hospital/boards`, and the binary is `boards` (`boards run mesh/error-surge`).

Considered and rejected:

- `bench`.
- `triage-bench`.
- Keep `mesh-incidents-bench`.

### Cases and scoring

#### D9. How a case is described
**A declarative `case.yaml`, with an escape hatch.** One file holds:
- the track and how the target is built;
- the inject steps, as plain commands;
- a built-in oracle kind (a PromQL threshold, an HTTP status, a file or AST condition) that decides whether the defect is live;
- the expected culprits and the expected remediation class;
- the clean twin;
- the provenance (D15).

When no built-in oracle fits, `script:` runs a check of the case's own. A JSON Schema validates every case in CI. The proof specs in `meshmedic/proof/*.yaml` are already close to this shape.

Considered and rejected:

- Keep the shell scripts, and add a small `meta.yaml`.
- Cases as Go code, like Helmbulance's `Scenario` interface.

#### D10. How boards gets a tool's answer
**One adapter per tool, in this repository** (`adapters/<tool>/`). An adapter runs the tool, reads its output, and writes the shared verdict JSON: did it alarm, which culprits it named, which patch it proposed. The tool itself does not change. This is the only way rival tools can be measured, and MeshMedic gets no special treatment.

The boards maintainers write every adapter, rivals' included, so an adapter could handicap a rival or favour MeshMedic. To prevent that:
  - each adapter declares in `adapter.yaml` what counts as an alarm and how its output maps to culprits;
  - that mapping is frozen for a suite, published, and offered to the rival tool's maintainers for review;
  - every report names the adapter's commit.

Considered and rejected:

- Tools must emit the boards JSON themselves.
- Plain text, read by an LLM judge.

#### D11. Scoring diagnosis
**Machine match, with two human raters for prose.**
- When a tool returns structured output, the expected entities (resource names, subsets, rule IDs) are matched by machine.
- When a tool writes prose, its adapter extracts candidate entities, and two independent people score it.
- `scores.csv` records both scores and who gave them.
- If the two raters differ by more than one level, a third rater decides. Both original scores stay in `scores.csv`.

Considered and rejected:

- An LLM judge, with a human audit of a 20% sample.
- Human only, with two raters.

#### D12. Scoring remediation
**Apply the fix and see whether it heals.** boards applies the proposed patch to the testbed and checks three things:
1. The oracle reports the defect gone.
2. The clean twin's checks still pass, so the patch broke nothing else.
3. The patch can be removed again.

Two rules close the obvious cheat, a patch that deletes or scales down the service:
  - "Cleared" requires traffic. When the oracle returns no data, the defect does not count as cleared.
  - Every case with a remediation expectation declares clean-twin checks, including a success rate measured with traffic present (architecture §4).

The axis applies only to tools and tracks that propose fixes. Elsewhere it is `null`, not 0. Helmbulance and GitHealthCheck propose none, so the Helm and code tracks have no remediation axis for them.

Considered and rejected:

- A person scores the patch text against the rubric.
- No remediation axis in v1.

### Neutrality

#### D13. The 11 existing scenarios and MeshMedic's proofs
**An open set, labelled contaminated.** They move into boards, and anyone can run them. MeshMedic was developed against them, so its scores on them are shown in a separate column marked contaminated. Standing comes from exam cases (D14). For every other tool these cases are not contaminated, and their scores count fully.

Considered and rejected:

- Return them to MeshMedic as training fixtures, and start boards empty.
- Count them unlabelled.

#### D14. A holdout that stays verifiable
**A rotating holdout with a hash commitment.**
1. Exam cases are written in a private repository.
2. When each case is written, its SHA-256 digest is published. This proves the case existed before a given tool release, without revealing it.
3. Every period (D31), each tool's current release runs on the holdout.
4. The cases are then opened with their results, so anyone can re-run and check them. They join the open set, and a new holdout is written.

The report shows each tool release's date relative to the digest date. A hash proves that a case existed; it does not prove that a tool's author had not read it. D28 adds the rule for that.

Considered and rejected:

- Everything public, with pre-registration only.
- A permanent secret set. *(Rejected: a result only the maintainers can reproduce cannot be falsified, which is what this project argues against.)*

#### D15. Who writes exam cases
**A real source, and a different author where possible.**
- Every mesh and Helm exam case comes from a public, real event: an Istio or chart GitHub issue, a published postmortem, or a CVE. Its link is a required field in `case.yaml`.
- Code exam cases have no such event (D22). They require documented hand verification instead: the method used, and a second person who verified the values.
- Where possible, the author did not build the Git-Hospital tool for that track. For example, a Helmbulance maintainer writes mesh cases.
- Each case records the author's relationship to the tools it scores.

Considered and rejected:

- Outside contributors only.
- An agent mines issue trackers for candidates, and a person verifies them.

#### D16. How results are summarised
**No total, a table of axes.** The table shows:
- the rates for each axis;
- the false alarm rate on clean twins;
- the cost: wall time, and cluster objects created or deleted during the run;
- `null` with a reason for anything that was not measured.

A single total is how "66/66" happened.

Considered and rejected:

- One weighted score from 0 to 100.
- The table, with a secondary summary score.

### Testbed

#### D17. Who provides the cluster
**boards builds it.**
- Each run creates its own kind cluster and deletes it afterwards.
- `--keep` leaves the cluster in place, since building kind with Istio takes minutes.
- `--reuse <name>` accepts only a cluster labelled as created by boards. A run on a reused cluster carries state from earlier runs, so its report has `standard: false`.
- Running against someone else's cluster (`--target-context`) is the v2 drill mode (D2). It must print every command it will run and require an explicit acknowledgement, as `meshmedic-prove --yes-inject-faults` does today.

Considered and rejected:

- The user's cluster, with a context guard (today's `harness/run.sh`).
- Both in v1.

#### D18. Version pinning
**`testbed.lock`, with one version.** It locks, by digest:
- the kind node image;
- the Istio version and mode (ambient);
- Prometheus;
- the app images.

Another version can be run (`--istio 1.26`), but the report marks it non-standard and it never enters the official table.

Considered and rejected:

- A matrix from the start (two Istio versions, ambient and sidecar).
- No pinning.

#### D19. The patient app
**payments and ledger move into boards** from `meshmedic/demo/`. MeshMedic's demo then references boards. The app's fault knobs (`ERROR_RATE`, latency) exist, and the 11 cases are written against them. The small home advantage this gives MeshMedic is accepted; the larger bias was in the cases, and D13 to D15 address it.

Considered and rejected:

- Google's Online Boutique.
- Both: payments for the open set, Online Boutique for the holdout.

#### D20. What runs in CI
**Tiers.**
- Every PR: schema validation of every case, adapter unit tests, and a dry run with the dummy tools (D32).
- Weekly and on every release: one real kind job that runs three cases (a fault, a clean twin, and a remediation apply) with MeshMedic and istioctl.
- The full suite runs before each release, locally or on a worker machine.

Considered and rejected:

- Every case on every PR.
- No cluster in CI, static checks only.

### Tracks, output and distribution

#### D21. A Helm case
**Real defect/fix pairs.**
- A public chart's version with a known day-2 bug is the defect twin. Example: an upgrade that recreates a PVC and loses its data.
- The version that fixed it is the clean twin.
- The source is the chart's issue and the fixing commit. Helmbulance's `corpus.yml` follows this pattern, but it stays Helmbulance's training set (D7), and boards finds new pairs.
- Rival tools: `ct install --upgrade` (chart-testing), kube-score and Polaris.

Considered and rejected:

- Small synthetic charts with one defect each.
- Synthetic in the open set, real pairs in the holdout.

Helmbulance's maintainer is told before any Helmbulance result is published.

#### D22. What a correct answer is in the code track
**Measurement accuracy.** The correct values are computed by hand on small fixture repositories and verified: "this function's cyclomatic complexity is 7", "this repository's truck factor is 2", "line coverage is 64.3%". Tools are scored on how close they come.
- The code track's axes are therefore the error for each metric (absolute and relative), not detection, diagnosis and remediation.
- Some cases have `null` as the correct answer, for example a repository with no coverage report. A tool that prints a number there loses points. This turns GitHealthCheck's "measured or null" rule into an exam question.

Considered and rejected:

- Planted-defect detection.
- Both.

#### D23. Output and exit codes
**The Helmbulance standard, with a generated leaderboard.**
- Reports: a versioned `report.json` (`schemaVersion`), a Markdown report and a coloured terminal view.
- Exit codes follow Helmbulance: `0` PASS, `1` FAIL, `2` usage or setup error, `3` INCONCLUSIVE. INCONCLUSIVE covers cases where the testbed could not be built or the oracle never saw the defect go live; the tool is not blamed for those. The meaning of PASS and FAIL for a benchmark is defined in architecture §6.
- Official results are committed as `results/<suite>/<tool>@<version>.json`. A GitHub Pages leaderboard is generated from those files, with no hand-written tables.

Considered and rejected:

- JSON and Markdown, no page.
- The standard, plus SARIF. *(Rejected: SARIF describes findings in code; a boards report is a tool's score sheet.)*

#### D24. Distribution
**A binary, an image and an Action.**
- `go install` and release archives, built with goreleaser.
- An image, `ghcr.io/git-hospital/boards`, that carries pinned kind, kubectl, helm and istioctl. It needs the host's Docker socket, and the README says plainly what that grants.
- A GitHub Action, so a tool's author gets "did my tool regress on boards?" on every PR. MeshMedic, Helmbulance and GitHealthCheck use it themselves.

Considered and rejected:

- Binary only.
- Container only.

### Migration and process

#### D25. Migration
**Rename, then move step by step.**
- Rename this repository to `boards`. History, the raw outputs of August 2026 and links are kept, and GitHub redirects the old URL.
- Move content in order: first the Go core with the testbed app and `testbed.lock`, since every case runs on them; then the 11 scenarios as `case.yaml`; then the remaining proofs.
- meshmedic-prove is deleted from MeshMedic only when boards meets two conditions:
  1. It performs the four assertions on all 16 proofs.
  2. It has equivalents of meshmedic-prove's other two checks, or there is a recorded decision to drop them. The two checks are the silence run (a quiet cluster watched after a baseline warm-up, `pkg/proof/silence.go`) and the winnability check, which rejects a proof whose deadline is shorter than its entry's hold duration (`pkg/proof/winnable.go`).

  Until then both exist, so there is never a moment without end-to-end evidence.

Considered and rejected:

- A new `boards` repository, with this one archived.
- Rename, and delete meshmedic-prove at once.

#### D26. The HolmesGPT and k8sgpt adapters
**They return as ordinary adapters, grouped by category.** The original mistake was not measuring them; it was the sentence "MeshMedic beats Holmes".
- The leaderboard groups tools into categories: mesh-specific, config linter and general AI agent. Comparisons are made within a group. Across groups, tools are shown side by side, with no "who won" sentence.
- For an LLM tool, the model and the token cost are part of the report.
- Personal paths such as `~/.holmes.env` are removed.
- Official runs of LLM tools wait for a sponsor (D29).

Considered and rejected:

- Delete them.
- Archive them under `legacy/` with their old results.

#### D27. When boards is tagged v1.0
**When all three tracks are complete.** The exit criteria and the milestones before them are at the end of this record. v1.0 therefore depends on GitHealthCheck's Go rewrite.

Considered and rejected:

- When the mesh track is complete, with Helm and code in v1.x.
- No criteria; tag it when it feels ready.

#### D28. Process and version numbers
**The GitHealthCheck process, and two version numbers.**
- Every change is a PR, with an automated review, merged by the maintainer.
- Helmbulance's maintainer co-maintains the Helm track, if they agree.
- The holdout repository is private, and only the boards maintainer and Helmbulance's maintainer can read it. Both of them author tools that boards scores, so reading the holdout comes with a rule. Between a holdout being sealed and opened, a reader does not merge behaviour changes to their own tool in that track. Any commit to that tool made after the reader saw the cases is listed in the report.
- Two things are versioned separately:
  - the tool, with semver (`boards v0.2.0`);
  - the case set, by calendar (`suite 2026.4`).

  Every result carries both, for example "MeshMedic 1.1, on suite 2026.4, measured with boards 0.3". Scores from different suites are never compared by accident.

Considered and rejected:

- Commit directly to main until v0.1.
- The GitHealthCheck process, plus a required approval from Helmbulance's maintainer on every PR.

### Open points settled in the last round

#### D29. Paying for LLM tokens
**No official LLM runs until there is a sponsor.**
- Adapters for LLM tools are still written and tested with the dry run (D32).
- LLM tools can appear on the leaderboard only through submissions (D30).
- k8sgpt enters official runs in its analyzer mode (`k8sgpt analyze` without `--explain`), which needs no AI backend.
- The v1.0 criteria contain no LLM tool.

Considered and rejected:

- Official runs with a cheap model, while authors submit their own runs on expensive models.
- Only results submitted from outside.

#### D30. Results submitted by others
**Accepted, with two badges.**
- *Self-reported:* the author ran boards and attached the raw outputs and `report.json`.
- *Verified:* maintainers re-ran three randomly chosen cases with the same versions, and the results matched. "Matched" means the same detection status and the same culprit level on each re-run case, with wall times within a stated tolerance.

The leaderboard shows the two badges apart. Submissions cover the open set only, since the holdout is private.

Considered and rejected:

- Only maintainer runs.
- Accepted, without a distinction.

#### D31. How often the holdout rotates
**Every three months** (`suite 2026.4`, `2027.1`, ...), with three to five new exam cases per period in each active track.

Considered and rejected:

- On every boards release.
- Every six months.

#### D32. Who tests the benchmark itself
**Three dummy tools, on every PR.** None of them needs a cluster; they run in dry-run mode, in seconds. Unless all three get exactly their expected scores, the PR is red.
- *Oracle:* cheats by reading the answer from `case.yaml`. It must score full marks everywhere; otherwise the scorer is broken.
- *Silent:* says nothing. It must score 0 on detection for defect twins and full marks on clean twins.
- *Panic:* alarms on everything. Its false alarm rate must be 100%.

Considered and rejected:

- Unit tests of the scorer only.
- Not tested separately.

## Consequences

- **This repository changed identity.** It was renamed from mesh-incidents-bench, and README, CONTRIBUTING and the results pages were rewritten to match.
- **MeshMedic loses a binary.** `cmd/meshmedic-prove`, `pkg/proof` and `proof/` leave MeshMedic in the PR that follows boards reaching the D25 condition. MeshMedic's CI then runs the boards Action.
- **Helmbulance is not changed by this record.** The adapter lives in boards and calls the public Helmbulance CLI. Helmbulance's maintainer is told before any result is published (D21), and is asked about co-maintaining the Helm track (D28).
- **The code track waits for GitHealthCheck.** Its fixture repositories and hand-verified values can be written before, and rival tools (gocyclo, radon, jscpd, scc) can be measured before GitHealthCheck v0.2.0 exists.

### Dependency on MeshMedic's direction

MeshMedic's [ADR 0001](https://github.com/Git-Hospital/meshmedic/blob/main/docs/decisions/0001-compat-engine.md) (23 October 2026) proposed turning it toward compatibility testing: "does this chart or manifest break when it enters an Istio mesh?". Its pilot narrowed that to a compatibility atlas. This record measures incident detection, MeshMedic's `watch` mode. If a compatibility mode is built, the mesh track keeps its incident cases, and a compatibility sub-track is considered after v1.0. Nothing in this record blocks either direction.

## Milestones and the v1.0 exit criteria

| Release | Content |
|---|---|
| v0.2.0 | Go core: the `case.yaml` schema, the runner, `report.json`, exit codes and the three dummy tools in CI. The 11 scenarios converted to `case.yaml`, as an open set labelled contaminated for MeshMedic. |
| v0.3.0 | Adapters for MeshMedic, istioctl analyze, a plain Prometheus alert-rule baseline, and k8sgpt in analyzer mode. Remediation applied and verified (D12). The weekly kind CI job. All 16 proofs reproduced, then meshmedic-prove deleted from MeshMedic. |
| v0.4.0 | The first mesh holdout, with hashes published. The generated leaderboard. Release pipeline: archives, image and Action. Submissions with badges (D30). The HolmesGPT adapter, with personal paths removed, tested by dry run only (D26, D29). |
| v0.5.0 | The Helm track: real defect/fix pairs, and adapters for Helmbulance, ct and kube-score. |
| v0.6.0 | The code track: hand-verified fixture repositories, including `null` cases. Adapters for GitHealthCheck (Go) and its rivals. |
| v1.0.0 | Every criterion below is met. |

boards v1.0.0 is tagged only when all of the following are true:

1. **Mesh:**
   - the 11 open cases are in `case.yaml`;
   - at least 5 holdout cases come from real sources, with their hashes published before the run;
   - the four adapters of v0.3.0 exist;
   - remediation is applied, not read;
   - the weekly kind job is green;
   - meshmedic-prove is gone.
2. **Helm:**
   - at least 5 real defect/fix pairs are in the open set and at least 3 in the holdout;
   - the adapters for Helmbulance, ct and kube-score exist.
3. **Code:**
   - at least 3 fixture repositories have hand-verified values for every metric family GitHealthCheck v1 reports, including `null` cases;
   - adapters exist for GitHealthCheck and at least two rival tools.
4. **Everywhere:**
   - the three dummy tools are green on every PR;
   - one holdout period has completed: its cases are written, run and opened;
   - the leaderboard is generated only from `results/`;
   - the binary, image and Action are released.
