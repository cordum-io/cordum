# Cordum platform threat model

This M0 security contract under `docs/contract/` states who Cordum defends against, what it trusts and what it does not promise across the platform, not only Edge. It distinguishes source-reviewed mechanisms from qualified protection; roadmap capabilities remain planned.

Reviewed commit: `d027ae97401c411fb62037a11b783700d1501cfe` (cordum origin/main, 2026-11-11).

Findings R01-R15 come from the roadmap review at `ebc0dd6a`; none is recorded fixed there, and repro cases are planned in rows M0-08 to M0-12. *(not verified)*

Claim-status vocabulary:

- `planned` (not built)
- `supported` (the feature exists, but a workload could bypass it or it is untested against bypass)
- `enforced` (an enforcement point exists that the workload cannot skip inside the declared boundary)
- `verified` (enforced, with passing negative tests at a pinned release)
- `unsupported` (explicitly not provided)

Statuses are code tokens, for example `supported`. No row uses the release-qualified status: no release has been qualified with negative tests by this review. Existing mechanisms are source-reviewed only; these tokens do not certify roadmap work or deployment operation.

A current-behavior statement cites a repository path at the reviewed commit as `path`, `path:N` or `path:N-M`, with symbol names outside the path backticks. A statement without a citation ends with *(not verified)*. Open findings are named by id only; this document gives neither descriptions nor trigger instructions.

Related documents (their scope is not an endorsement of every claim they contain):

- [Edge P0 threat model](../internal/security/edge-p0-threat-model.md): the historical Edge closure checklist.
- [Delegation](../auth/delegation.md): delegated authority, token scope and key rotation.
- [Policy signing](../deployment/policy-signing.md): policy signing modes, key distribution and rollout.
- [Audit deployment](../deployment/audit-chain.md): audit-chain operation, retention and incident handling.
- [Audit architecture](../audit-chain.md): chain representation, hashing and authentication.
- [MCP outbound signing](../mcp/outbound-signing.md): signatures and cooperating-peer trust.
- [MCP approval gates](../mcp/per-tool-approval.md): tool approval configuration and operator flows.
- [Agentd keychain](../security/agentd-keychain.md): credential bootstrap and service integration.
- [Binary signing](../security/binary-signing.md): release integrity and endpoint verification scope.
- [Classifier boundary](../edge/classifier-trust-boundary.md): classifier-owned labels versus request data.
- [Policy mechanism roadmap](../edge-074-policy-mechanism.md): planned separation of classification and resource mediation.

## Assets

Assets identify authority, data and evidence whose integrity or confidentiality matters.

## Adversaries

Adversaries distinguish untrusted workloads from trusted infrastructure and administrators outside the promise.

## Trust boundaries

Boundaries identify where data or authority changes trust domains.

### Relationship to the Edge P0 threat model

The Edge closure checklist and this platform contract have different scopes and claim-status vocabularies.

## Trusted components

Classification follows whether a protection claim depends on the component deciding correctly or retaining a credential.

### Trusted-component inventory

The inventory records authority, compromise consequences and protection status.

### Explicit untrusted list

Clients and workload-controlled components require re-checks at trusted boundaries.

## Reference-monitor properties

These properties assess mediation, tamper resistance and analyzability without assuming deployment qualification.

## Non-promises

These exclusions bound the security promise rather than weakening an implicit universal guarantee.

## Deployment boundary

Deployment conditions limit which protection claims can eventually be qualified.
