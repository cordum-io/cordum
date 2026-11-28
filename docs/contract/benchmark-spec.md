# Benchmark specification

Status: contract, roadmap M6. Tags: benchmark, contract.

This document defines measures and a result format, not benchmark results or a
comparison. Running the benchmark belongs to M6-06; choosing alternatives belongs
to M6-07. No system is qualified by this specification or its synthetic example.

## Measures

Each ID is required exactly once. Percentages use the denominator stated below;
record the trial count, observation window and evidence pins in the measure's
note. A zero denominator means `not measured`, not zero percent. Use the same
workload revision, inputs and observation rules for any systems being measured.

| ID | Dimension | Measure | Definition | Unit | Method |
| --- | --- | --- | --- | --- | --- |
| protection.unauthorized_effects | Protection | Unauthorized effects | Prohibited target effects observed in adversarial trials. | effects | Inspect target state before and after every corpus trial; count prohibited changes, not denial responses. |
| protection.mutation_rejection | Protection | Mutation rejection | Mutated authorized-action trials that produce no prohibited effect, divided by all mutation trials. | percent | Change one bound action field after authorization; inspect executor and target evidence for absence of the changed effect. |
| protection.replay_rejection | Protection | Replay rejection | Replay trials with no repeated protected effect, divided by all replay trials. | percent | Resubmit captured authorizations and deliveries; inspect target state for duplicate effects. |
| protection.cross_tenant_rejection | Protection | Cross-tenant rejection | Cross-tenant trials with no unauthorized access or effect, divided by all cross-tenant trials. | percent | Attempt access and execution using a different tenant; inspect resource state and captured data for unauthorized changes or disclosure. |
| protection.revocation_rejection | Protection | Revocation rejection | Post-bound revocation trials with no protected effect, divided by all post-bound trials. | percent | Revoke authority, wait the disclosed propagation bound, retry and inspect target state; record the bound and attempts inside it separately. |
| protection.constraint_enforcement | Protection | Constraint enforcement | Out-of-constraint trials with no prohibited effect, divided by all constraint-violation trials. | percent | Vary each declared restriction independently and inspect actual executor inputs and target outcomes. |
| protection.bypass_coverage | Protection | Bypass coverage | Enumerated bypass paths tested with no unauthorized protected effect, divided by all paths in the declared threat model. | percent | Enumerate alternate execution and resource-access paths, test each and inspect target effects; identify untested paths in the note. |
| protection.evidence_integrity | Protection | Evidence integrity | Evidence-tampering trials detected by an independent verifier, divided by all tampering trials. | percent | Alter, remove and reorder evidence artifacts; run the pinned verifier against pinned originals and record detections. |
| protection.recovery_correctness | Protection | Recovery correctness | Recovery trials with correct authority, target state and outcome classification, divided by all recovery trials. | percent | Inject failures at declared execution phases, recover, then reconcile receipts and target state; unknown must not count as success. |
| utility.legitimate_completion | Utility | Legitimate completion | Legitimate trials reaching their specified successful target outcome, divided by all legitimate trials. | percent | Run the pinned legitimate workload set and independently inspect its success postconditions. |
| utility.false_blocks | Utility | False blocks | Legitimate trials denied contrary to the workload's expected policy, divided by all legitimate trials. | percent | Compare decisions and observed outcomes with the pinned legitimate-case oracle; distinguish outages and approvals from policy denials. |
| utility.unnecessary_approvals | Utility | Unnecessary approvals | Legitimate trials requesting approval when the oracle requires none, divided by all legitimate trials. | percent | Compare approval requests with predeclared expected authority for the same workload inputs. |
| utility.reviewer_time | Utility | Reviewer time | Total active human time spent reviewing workload approval requests. | person-minutes | Time active review sessions over the disclosed workload window; exclude queue waiting and record request count. |
| utility.installation_effort | Utility | Installation effort | Active human effort from clean prerequisites to the declared working deployment boundary. | person-hours | Time a scripted installation including required application changes; disclose prerequisites, operator count and completion criteria. |
| utility.upgrade_effort | Utility | Upgrade effort | Active human effort to upgrade between pinned versions while restoring the declared boundary. | person-hours | Time the version-to-version procedure including migration and verification; record source and destination versions and downtime. |
| utility.sustained_enforced_usage | Utility | Sustained enforced usage | Eligible operations actually traversing the declared enforcing path, divided by all eligible operations over the observation window. | percent | Count eligible operations independently of hook telemetry and reconcile with enforcement evidence; disclose window and exclusions. |
| operations.latency | Operations | End-to-end latency | 95th percentile elapsed time from workload submission to its observed terminal outcome. | milliseconds | Timestamp submissions and terminal outcomes using the pinned workload harness; disclose sample count and treatment of timeouts. |
| operations.throughput | Operations | Throughput | Successful terminal workload outcomes per second over the measured steady-state window. | outcomes/second | Count independently confirmed completions at disclosed concurrency; disclose window, failures and saturation conditions. |
| operations.outage_behavior | Operations | Outage behavior | Injected-outage trials meeting predeclared safety and outcome-classification expectations, divided by all outage trials. | percent | Interrupt each declared dependency; inspect target state, decisions and uncertainty during the outage and after recovery. |
| operations.recovery_time | Operations | Recovery time | Elapsed time from dependency restoration to the first full workload window meeting predeclared recovery criteria. | seconds | Restore each failed dependency and probe the workload; disclose the recovery window, criteria and per-scenario times. |
| operations.unknown_outcome_reconciliation | Operations | Unknown-outcome reconciliation | Initially unknown outcomes resolved from independent target or executor evidence by the declared deadline, divided by all initially unknown outcomes. | percent | Inject lost receipts or interrupted execution; reconcile without assuming success, and disclose deadline and unresolved count. |
| operations.support_burden | Operations | Support burden | Active human support effort attributable to the deployment during the observation window. | person-hours | Log triage, incident and maintenance time using a declared attribution rule; disclose workload volume and window. |
| operations.deployment_cost | Operations | Deployment cost | Infrastructure, license and attributable operator cost for the measured deployment window. | USD | Sum itemized costs under disclosed utilization, pricing date, currency conversion and labor assumptions; disclose window and workload volume. |

## Allowed values

Every measure contains a `value`: a JSON number, `unsupported`, or `not measured`.
A blank value, an omitted value, null, an unknown measure or a missing measure is
invalid. `unsupported` means the capability cannot be provided in the disclosed
boundary; explain the limitation in `note`. `not measured` means no qualifying
measurement is available; explain the gap. Neither is a numeric zero or a win.

## Deployment boundary

Every result requires a non-whitespace `deployment_boundary` statement. Disclose
which components and protected resources are inside enforcement, integration and
platform versions, topology, tenant model, authority configuration, trust
assumptions, exclusions, and the observation window. Distinguish a voluntary hook
from an independently enforced resource boundary. Qualification is specific to
that combination and must not be reused for another platform or adapter.

Pin source SHAs, artifact digests, policy and authority versions, executor and
platform versions, deployment configuration, corpus revision, results and open
exceptions in the boundary statement or measure notes. Performance budgets come
from workload-specific measured baselines, not this specification. No comparative
statement may be published without human approval of that exact statement; run
alternatives only within their licenses and terms.

## Workloads

The adversarial corpus is **M6-02 (planned)**. It does not exist yet; measures
requiring that corpus remain `not measured` until it and qualifying evidence
exist. This is a roadmap reference, not a claim of available adversarial coverage.

Named performance workloads (paths relative to this document):

- [Job submission](../../tools/perfharness/workloads/job-submit.yaml)
- [Job submission with approval](../../tools/perfharness/workloads/job-submit-approval.yaml)
- [Edge hook evaluation](../../tools/perfharness/workloads/edge-hook-evaluate.yaml)
- [MCP tool call](../../tools/perfharness/workloads/mcp-tool-call.yaml)

Use the same pinned workloads and disclose deployment boundaries for every system.
These performance workloads do not substitute for the planned adversarial corpus.

## Result format

[benchmark-result.schema.json](benchmark-result.schema.json) defines schema
version 1. `system` identifies the system name and pinned version; `measures` maps
every table ID to an object containing `value` and an optional string `note`.

The following is a **synthetic format example only**. Its numeric and unsupported
entries illustrate types, not observations or capability assessments of any system.

```json
{
  "schema_version": 1,
  "system": {"name": "synthetic-format-example", "version": "not-a-release"},
  "deployment_boundary": "Illustrative only; no deployed system, workload run or qualification.",
  "measures": {
    "protection.unauthorized_effects": {"value": "not measured"},
    "protection.mutation_rejection": {"value": "not measured"},
    "protection.replay_rejection": {"value": "not measured"},
    "protection.cross_tenant_rejection": {"value": "not measured"},
    "protection.revocation_rejection": {"value": "not measured"},
    "protection.constraint_enforcement": {"value": "not measured"},
    "protection.bypass_coverage": {"value": "not measured"},
    "protection.evidence_integrity": {"value": "not measured"},
    "protection.recovery_correctness": {"value": "not measured"},
    "utility.legitimate_completion": {"value": "not measured"},
    "utility.false_blocks": {"value": "not measured"},
    "utility.unnecessary_approvals": {"value": "not measured"},
    "utility.reviewer_time": {"value": "not measured"},
    "utility.installation_effort": {"value": "not measured"},
    "utility.upgrade_effort": {"value": "not measured"},
    "utility.sustained_enforced_usage": {"value": "unsupported", "note": "Synthetic type illustration, not an assessment."},
    "operations.latency": {"value": 0, "note": "Synthetic number, not a measured latency."},
    "operations.throughput": {"value": "not measured"},
    "operations.outage_behavior": {"value": "not measured"},
    "operations.recovery_time": {"value": "not measured"},
    "operations.unknown_outcome_reconciliation": {"value": "not measured"},
    "operations.support_burden": {"value": "not measured"},
    "operations.deployment_cost": {"value": "not measured"}
  }
}
```
