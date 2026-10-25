# Investigation footprint

A diagnostic tool has a cost that is easy to miss: how much it changes the
cluster while it investigates. A tool that spawns pods, execs into containers,
or applies objects to test a hypothesis leaves a footprint, and doing that in a
production namespace during an incident is its own risk. This benchmark measures
that footprint so it is visible next to the diagnosis.

## The metric

For every run, the harness records an inventory diff of the cluster before and
after the tool ran: objects created and objects deleted while it investigated.
The footer of each `results/raw/*` file carries this count alongside the tool's
wall time. It is a property of the tool, not of the incident, so it is
comparable across runs and across tools.

## Read-only tools: zero by design

Some tools have a footprint of zero by construction. MeshMedic, scored here
and written by the same author, is one: it reads and never writes. Its detection is Prometheus queries; its
configuration and triage evidence is read-only `kubectl get` and `kubectl logs`.
It proposes remediation as a pull request against a config repository, which a
human merges, so nothing is applied to the cluster by the tool at all. The
footprint is therefore zero as a structural guarantee, not a per-run outcome:
there is no code path in which MeshMedic creates or mutates a cluster object.

The contrast worth stating generally: an investigator that probes connectivity
or state by launching a temporary pod, or by exec-ing into a running one, is
mutating the cluster to learn about it. That can be reasonable, but it is a cost
an operator should be able to see before paging the tool, and for a tool whose
footprint is not structurally zero it varies run to run. The metric exists so
that cost is measured rather than assumed.
