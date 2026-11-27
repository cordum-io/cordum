# License-state behavior contract

Status: **state-model tests; capability decision cells pending**.
Reviewed source: `ee58be379d3927f3aec0cac13ce5e09cf605a67d`.
Tags: security-contract, licensing, PR2-06.
This table records requirements, not qualification of a release or deployment.

## Rule

A license change may remove features or administration. It never turns a policy
deny into an allow, and it never stops decision evidence from being written.

## States

| State | How it arises | Recorded status | Plan and entitlements | Break-glass state | Tests |
| --- | --- | --- | --- | --- | --- |
| valid | Enterprise license expires in 90 days | active | Enterprise, licensed merge | valid | `TestLicenseStateDerivation/valid` |
| warning | Enterprise license expires in 10 days | warning | Enterprise, licensed merge | valid | `TestLicenseStateDerivation/warning` |
| grace | Enterprise license expired 3 days ago; default 14-day grace | grace | Enterprise, licensed merge | grace | `TestLicenseStateDerivation/grace` |
| degraded | License expired 30 days ago, past grace | fallback | Community defaults | invalid | `TestLicenseStateDerivation/degraded` |
| invalid | Wrong signing key, future not_before, or load error | fallback | Community defaults | invalid | `TestLicenseStateDerivation/invalid` |
| missing | No license loaded | active | Community defaults | valid | `TestLicenseStateDerivation/missing` |

## When the state changes

State is computed only at start-up and on license reload; no timer re-evaluates
expiry. `TestLicenseStateChangesOnlyOnReload` pins the cached snapshot across
clock changes and the transition after reload. Past-grace verification returns
an expiry error, so the resolver records fallback, not a degraded snapshot.

## Behavior by capability class

Every decision cell below is **Required; not yet tested** end to end. The
administration cells also cite the narrower break-glass permission matrix;
that matrix alone does not establish route authorization or effect prevention.

| Class | State | Required behavior | Tests |
| --- | --- | --- | --- |
| policy evaluation | valid | Required: policy denies remain denies. | `TestLicenseBehaviorKernel_BaseDeny/valid`, `TestLicenseBehaviorKernel_CustomBundleDeny/valid`, `TestLicenseBehaviorKernel_InvariantsDeny/valid`, `TestLicenseBehaviorKernel_VelocityDeny/valid` |
| policy evaluation | warning | Required: policy denies remain denies. | `TestLicenseBehaviorKernel_BaseDeny/warning`, `TestLicenseBehaviorKernel_CustomBundleDeny/warning`, `TestLicenseBehaviorKernel_InvariantsDeny/warning`, `TestLicenseBehaviorKernel_VelocityDeny/warning` |
| policy evaluation | grace | Required: policy denies remain denies. | `TestLicenseBehaviorKernel_BaseDeny/grace`, `TestLicenseBehaviorKernel_CustomBundleDeny/grace`, `TestLicenseBehaviorKernel_InvariantsDeny/grace`, `TestLicenseBehaviorKernel_VelocityDeny/grace` |
| policy evaluation | degraded | Required; not yet tested: policy denies remain denies. | `TestLicenseBehaviorKernel_BaseDeny/degraded`, open finding PR2-06-F4, open finding PR2-06-F5, open finding PR2-06-F6 |
| policy evaluation | invalid | Required; not yet tested: policy denies remain denies. | `TestLicenseBehaviorKernel_BaseDeny/invalid`, open finding PR2-06-F4, open finding PR2-06-F5, open finding PR2-06-F6 |
| policy evaluation | missing | Required; not yet tested: policy denies remain denies. | `TestLicenseBehaviorKernel_BaseDeny/missing`, open finding PR2-06-F4, open finding PR2-06-F5, open finding PR2-06-F6 |
| approvals | valid | Required; not yet tested: approval cannot override a deny; no prohibited mutation. | `TestLicenseBehaviorGateway_Approvals/valid`, `TestLicenseBehaviorGateway_RBACLoss/valid/jobs.approve`, pending PR2-06-RUNNER |
| approvals | warning | Required; not yet tested: approval cannot override a deny; no prohibited mutation. | `TestLicenseBehaviorGateway_Approvals/warning`, pending PR2-06-RUNNER |
| approvals | grace | Required; not yet tested: approval cannot override a deny; no prohibited mutation. | `TestLicenseBehaviorGateway_Approvals/grace`, `TestLicenseBehaviorGateway_RBACLoss/grace/jobs.approve`, pending PR2-06-RUNNER |
| approvals | degraded | Required; not yet tested: approval cannot override a deny; no prohibited mutation. | `TestLicenseBehaviorGateway_Approvals/degraded`, `TestLicenseBehaviorGateway_RBACLoss/degraded/jobs.approve`, pending PR2-06-RUNNER |
| approvals | invalid | Required; not yet tested: approval cannot override a deny; no prohibited mutation. | `TestLicenseBehaviorGateway_Approvals/invalid`, `TestLicenseBehaviorGateway_RBACLoss/invalid/jobs.approve`, pending PR2-06-RUNNER |
| approvals | missing | Required; not yet tested: approval cannot override a deny; no prohibited mutation. | `TestLicenseBehaviorGateway_Approvals/missing`, `TestLicenseBehaviorGateway_RBACLoss/missing/jobs.approve`, pending PR2-06-RUNNER |
| audit | valid | Required: decision evidence is still written. | `TestLicenseBehaviorGateway_Audit/valid`, `TestLicenseBehaviorKernel_DecisionRecord/valid` |
| audit | warning | Required: decision evidence is still written. | `TestLicenseBehaviorGateway_Audit/warning`, `TestLicenseBehaviorKernel_DecisionRecord/warning` |
| audit | grace | Required: decision evidence is still written. | `TestLicenseBehaviorGateway_Audit/grace`, `TestLicenseBehaviorKernel_DecisionRecord/grace` |
| audit | degraded | Required: decision evidence is still written. | `TestLicenseBehaviorGateway_Audit/degraded`, `TestLicenseBehaviorKernel_DecisionRecord/degraded` |
| audit | invalid | Required: decision evidence is still written. | `TestLicenseBehaviorGateway_Audit/invalid`, `TestLicenseBehaviorKernel_DecisionRecord/invalid` |
| audit | missing | Required: decision evidence is still written. | `TestLicenseBehaviorGateway_Audit/missing`, `TestLicenseBehaviorKernel_DecisionRecord/missing` |
| protected operations | valid | Required: denied requests cause no publication or protected mutation. | `TestLicenseBehaviorGateway_ProtectedOperations/valid`, `TestLicenseBehaviorGateway_RBACLoss/valid/jobs.write` |
| protected operations | warning | Required: denied requests cause no publication or protected mutation. | `TestLicenseBehaviorGateway_ProtectedOperations/warning` |
| protected operations | grace | Required: denied requests cause no publication or protected mutation. | `TestLicenseBehaviorGateway_ProtectedOperations/grace`, `TestLicenseBehaviorGateway_RBACLoss/grace/jobs.write` |
| protected operations | degraded | Required: denied requests cause no publication or protected mutation. | `TestLicenseBehaviorGateway_ProtectedOperations/degraded`, `TestLicenseBehaviorGateway_RBACLoss/degraded/jobs.write` |
| protected operations | invalid | Required: denied requests cause no publication or protected mutation. | `TestLicenseBehaviorGateway_ProtectedOperations/invalid`, `TestLicenseBehaviorGateway_RBACLoss/invalid/jobs.write` |
| protected operations | missing | Required: denied requests cause no publication or protected mutation. | `TestLicenseBehaviorGateway_ProtectedOperations/missing`, `TestLicenseBehaviorGateway_RBACLoss/missing/jobs.write` |
| runner | valid | Required; not yet tested: denied work is not dispatched or started. | pending PR2-06-RUNNER |
| runner | warning | Required; not yet tested: denied work is not dispatched or started. | pending PR2-06-RUNNER |
| runner | grace | Required; not yet tested: denied work is not dispatched or started. | pending PR2-06-RUNNER |
| runner | degraded | Required; not yet tested: denied work is not dispatched or started. | pending PR2-06-RUNNER |
| runner | invalid | Required; not yet tested: denied work is not dispatched or started. | pending PR2-06-RUNNER |
| runner | missing | Required; not yet tested: denied work is not dispatched or started. | pending PR2-06-RUNNER |
| administration | valid | Required: recovery permissions do not replace authorization. | `TestLicenseBreakGlassMatrix`, `TestLicenseBehaviorGateway_Administration/valid`, `TestLicenseBehaviorGateway_RBACLoss/valid/roles.write`, `TestLicenseBehaviorGateway_RBACLossConfigWrite/valid`, `TestLicenseBehaviorGateway_RBACLossCustomRoleRead/valid` |
| administration | warning | Required: recovery permissions do not replace authorization. | `TestLicenseBreakGlassMatrix`, `TestLicenseBehaviorGateway_Administration/warning` |
| administration | grace | Required: recovery permissions do not replace authorization. | `TestLicenseBreakGlassMatrix`, `TestLicenseBehaviorGateway_Administration/grace`, `TestLicenseBehaviorGateway_RBACLoss/grace/roles.write` |
| administration | degraded | Required: recovery permissions do not replace authorization. | `TestLicenseBreakGlassMatrix`, `TestLicenseBehaviorGateway_Administration/degraded`, `TestLicenseBehaviorGateway_RBACLoss/degraded/roles.write`, `TestLicenseBehaviorGateway_RBACLossConfigWrite/degraded`, `TestLicenseBehaviorGateway_CommunityFallback/degraded` |
| administration | invalid | Required: recovery permissions do not replace authorization. | `TestLicenseBreakGlassMatrix`, `TestLicenseBehaviorGateway_Administration/invalid`, `TestLicenseBehaviorGateway_RBACLoss/invalid/roles.write`, `TestLicenseBehaviorGateway_RBACLossConfigWrite/invalid`, `TestLicenseBehaviorGateway_CommunityFallback/invalid` |
| administration | missing | Required: recovery permissions do not replace authorization. | `TestLicenseBreakGlassMatrix`, `TestLicenseBehaviorGateway_Administration/missing`, `TestLicenseBehaviorGateway_RBACLoss/missing/roles.write`, `TestLicenseBehaviorGateway_RBACLossConfigWrite/missing`, `TestLicenseBehaviorGateway_RBACLossCustomRoleRead/missing`, `TestLicenseBehaviorGateway_CommunityFallback/missing`, `TestLicenseBehaviorGateway_MemoryRateLimit` |

## Protections outside the Community tier

Inventory of Enterprise defaults that differ from Community. These are
requirements, not results; owners must test the effect-side boundary.

| Entitlement | Required behavior after falling back to Community | Owner |
| --- | --- | --- |
| `approval_mode` | Required approvals remain binding, or the operation fails closed. | PR2-06-RUNNER |
| `max_workers` | Work beyond the limit is rejected without dispatch. | PR2-06-RUNNER |
| `requests_per_second` | Requests beyond the limit cause no protected mutation. | PR2-06-GATEWAY |
| `max_concurrent_jobs` | Excess work is held or rejected without dispatch. | PR2-06-RUNNER |
| `max_policy_bundles` | Rules from custom bundles, including secops/invariants, keep denying, or the kernel fails closed. | PR2-06-KERNEL |
| `audit_retention_days` | Decision evidence is still written; retention remains explicit. | PR2-06-GATEWAY |
| `sso` | Unavailable authentication is reported as unsupported, never a bypass. | PR2-06-GATEWAY |
| `saml` | Unavailable authentication is reported as unsupported, never a bypass. | PR2-06-GATEWAY |
| `scim` | Unavailable provisioning is reported as unsupported; authorization remains binding. | PR2-06-GATEWAY |
| `rbac` | Permission restrictions remain binding, or the request fails closed. | PR2-06-GATEWAY |
| `audit_export` | Unavailable export is reported as unsupported; decision evidence is still written. | PR2-06-GATEWAY |
| `siem_export` | Unavailable export is reported as unsupported; decision evidence is still written. | PR2-06-GATEWAY |
| `legal_hold` | Held evidence is retained, or the incompatible operation fails closed. | PR2-06-GATEWAY |
| `velocity_rules` | Required velocity restrictions remain binding, or the operation fails closed. | PR2-06-KERNEL |
| `agent_identity` | Required identity restrictions remain binding, or dispatch fails closed. | PR2-06-RUNNER |

## Cell grammar for maintainers

Tests cells use comma-separated backticked test names (optionally with a
`/subtest` suffix), `pending PR2-06-GATEWAY`, `pending PR2-06-KERNEL`, or
`pending PR2-06-RUNNER`. The closing row empties the checker's allowed-pending
set. States must cite tests, never pending work. After a governor ruling only,
`open finding PR2-06-F<n>` may name a finding by id. Never put finding mechanism
text in a cell. `TestLicenseBehaviorContract` checks table coverage, references
and inventory; it does not execute every capability decision cell.
