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

| id | asset | why it matters | where it lives (cite) | protected by |
| --- | --- | --- | --- | --- |
| A1 | Authority to cause a protected effect | A decision matters only if the effect obeys it. | Job dispatch: `core/controlplane/scheduler/engine.go`; Edge: `core/controlplane/gateway/handlers_edge_evaluate.go`; MCP: `core/mcp/policy_evaluate.go`; workflow: `core/workflow/engine.go`. | These decision paths, not universal resource control; Kubernetes protected-operation definition is M0-02 (planned), broker authority is M2 (planned). |
| A2 | Policy content and identity | Determines the rules operators intend to apply. | `core/controlplane/safetykernel/`, `core/controlplane/safetykernel/file_loader.go`, `core/controlplane/gateway/handlers_policy_bundles.go`, `core/infra/config/safety_policy.go`. | Loader signature modes and Gateway policy permissions; policy infrastructure remains trusted. |
| A3 | Approvals and approver identity | A human decision must not become unrelated authority. | `core/controlplane/gateway/handlers_approvals.go`, `core/controlplane/gateway/mcp_approvals.go`, `core/edge/approval_store.go`. | Gateway authentication/permissions and approval stores; no end-to-end qualification is claimed. |
| A4 | Credentials and key material | Possession can confer identity or signing authority. | API keys/JWT: `core/controlplane/gateway/auth/`; worker credentials: `core/controlplane/scheduler/credential_cache.go`; delegation keys: `cmd/cordumctl/delegation_keygen.go`; endpoint nonce/keychain: `core/edge/keychain/`; TLS keys: `core/controlplane/gateway/gateway.go`; licenses: `core/licensing/`. | Credential holders, OS secret storage, configured transport and signing checks; not protection against a compromised holder. |
| A5 | Audit and evidence | Operators need attributable records, not merely successful API responses. | `core/audit/`, `core/edge/export.go`. | Audit-chain/export mechanisms and trusted storage; source existence is not completeness proof. |
| A6 | Tenant data and isolation | One tenant must not exercise another tenant's authority or read its data. | `core/controlplane/gateway/auth/rbac.go`, `core/controlplane/gateway/gateway.go`, `core/edge/export.go`. | Gateway tenant context, permission checks and tenant-scoped store operations. |
| A7 | Secrets in tool inputs, outputs and context | Accidental disclosure can create lasting external authority. | `core/edge/redaction.go`, `core/controlplane/safetykernel/scanners.go`, `core/contextwindow/engine/`. | Redaction/scanning mechanisms and access controls; arbitrary-content secrecy is not certified. |
| A8 | Decision-path availability | Outages must have explicit safety and recovery semantics. | `core/edge/agentd/fail_modes.go`. | Mode-dependent handling; the complete partition/failure matrix is M0-06 (planned). |

## Adversaries

Adversaries distinguish untrusted workloads from trusted infrastructure and administrators outside the promise.

Capabilities and goals below are threat assumptions, not reports of successful attacks. Model output is data, never authority: this is a requirement, not a claim of universal enforcement.

| id | adversary | assumed capabilities | goal | inside the boundary? | where Cordum meets it today (cite) |
| --- | --- | --- | --- | --- | --- |
| D1 | Untrusted agents | Choose tool calls and submit jobs using their granted credentials. | Cause an unauthorized effect. | Yes, on declared governed paths. | Edge evaluation and MCP calls: `core/controlplane/gateway/handlers_edge_evaluate.go`, `core/mcp/policy_evaluate.go`. |
| D2 | Repository scripts | Code invoked by build, test or other tools runs with the agent's privileges (threat assumption). | Turn allowed execution into undesired effects. | Yes as untrusted input; arbitrary execution confinement is planned in M3. | Classification rather than an OS sandbox: `core/edge/classifier.go`; design scope: `docs/edge-074-policy-mechanism.md`. |
| D3 | MCP plugins | Supply upstream tool descriptions, behavior and results. | Mislead the caller or misuse a tool invocation. | Yes for Gateway-fronted calls, not every direct connection. | `core/controlplane/gateway/mcp_upstream_serving.go`, `core/controlplane/gateway/handlers_mcp_upstreams.go`. |
| D4 | Model output | Propose commands, arguments and purported approvals. | Convert persuasive text into authority. | Yes as untrusted data. | Tool-call approval claims are treated as data in `core/mcp/policy_evaluate.go:109-114`; classifier boundary: `core/edge/policy_mapper.go`. |
| D5 | Compromised policy service | Control a trusted policy source or publication principal. | Change what the system authorizes. | A trusted-component compromise, not a tolerated workload adversary. | `core/controlplane/safetykernel/file_loader.go`, `core/controlplane/gateway/handlers_policy_bundles.go`. |
| D6 | Malicious tenant | Send hostile requests using its own tenant identity. | Cross tenant or privilege boundaries. | Yes. | Gateway middleware/RBAC and tenant-scoped Edge export: `core/controlplane/gateway/gateway.go`, `core/controlplane/gateway/auth/rbac.go`, `core/edge/export.go`. |
| D7 | Network attacker | Observe, inject, alter or interrupt traffic on reachable links. | Steal authority, alter messages or deny service. | Yes subject to transport/deployment conditions; availability under all attacks is not promised. | TLS: `core/controlplane/gateway/gateway.go`, `core/controlplane/safetykernel/kernel.go`; admission: `core/infra/bus/raw_admission.go`. |
| D8 | Endpoint administrator | Control the OS, binary files and managed configuration (threat assumption). | Replace or disable endpoint controls. | No, outside the boundary; see non-promise 1. | Endpoint trust assumptions: `docs/security/binary-signing.md`, `docs/security/agentd-keychain.md`. |

## Trust boundaries

Boundaries identify where data or authority changes trust domains.

| id | sides and crossing | present protection and limitation | status |
| --- | --- | --- | --- |
| TB1 | Agent process → Edge hook → agentd: action metadata and decisions. | Developer launcher children share the launching OS user's trust domain (`core/edge/claude/launcher_process.go`, `core/edge/safeexec/safeexec.go`). Loopback validation and request nonce checks exist (`core/edge/agentd/config.go:225-248`, `core/edge/agentd/local_server.go:141-150`); keychain bootstrap and managed-settings generation exist (`core/edge/keychain/bootstrap.go`, `core/edge/claude/managed_settings.go`). These are not isolation from same-user interference or the endpoint administrator (`docs/security/agentd-keychain.md`, `docs/security/binary-signing.md`). | `supported` on a trusted endpoint. |
| TB2 | HTTP/gRPC clients → Gateway: identity, tenant, jobs and administrative requests. | HTTP logging/tracing, CORS, rate limiting, auth, read audit, tenant and body middleware (`core/controlplane/gateway/gateway.go:1236-1242`); gRPC auth/rate interceptors (`core/controlplane/gateway/gateway.go:990-1005`); RBAC (`core/controlplane/gateway/auth/rbac.go`). TLS conditions are in Deployment boundary. | `supported`. |
| TB3 | MCP clients → Gateway-hosted or standalone server; Gateway → upstream MCP servers: calls, metadata and results. | Gateway MCP authentication and opt-in action-gate wiring (`core/controlplane/gateway/handlers_mcp.go`, `core/controlplane/gateway/mcp_policy_wire.go`); upstream registration/use-time checks (`core/controlplane/gateway/handlers_mcp_upstreams.go`, `core/controlplane/gateway/mcp_upstream_serving.go`). Standalone server uses Gateway API clients and enables local scopes only with an agent id (`cmd/cordum-mcp/main.go:103-155`); it is not a separate authority boundary in an agent-controlled process. | `supported` for configured Gateway mechanisms; standalone boundary `unsupported`. |
| TB4 | Gateway/Scheduler/Workflow Engine → Safety Kernel gRPC: policy requests and decisions. | Server TLS is configured with certificate/key and required in production only (`core/controlplane/safetykernel/kernel.go:349-371`, `core/infra/env/env.go:33-40`). That TLS configuration has no client-certificate requirement, and the server construction adds no caller-auth interceptor (`core/controlplane/safetykernel/kernel.go:481-497`); trusted private reachability is required, not authenticated caller identity inferred from TLS. Workflow's output-safety client is wired in `cmd/cordum-workflow-engine/main.go`. | `supported` transport; caller-authentication boundary `unsupported` in this server wiring. |
| TB5 | Control plane → NATS → workers: CAP packets, credentials and results. | CAP handshake keys/mode checks (`cmd/cordum-scheduler/handshake_security_keys.go`, `cmd/cordum-scheduler/worker_security_modes.go`), worker attestation (`core/controlplane/scheduler/credential_cache.go`), raw admission (`core/infra/bus/raw_admission.go`) and shared replay storage (`core/infra/replay/redis_store.go`) exist. Deployment mode and broker access remain load-bearing. | `supported`. |
| TB6 | Services → Redis: policy, jobs, context, approvals and evidence. | Shared configuration reads one `REDIS_URL` (`core/infra/config/config.go:48-57`), loaded by Gateway, Safety Kernel, Scheduler, Workflow Engine and Context Engine (`cmd/cordum-api-gateway/main.go`, `cmd/cordum-safety-kernel/main.go`, `cmd/cordum-scheduler/main.go`, `cmd/cordum-workflow-engine/main.go`, `cmd/cordum-context-engine/main.go`). Per-service/per-key ACL separation in a deployed Redis is not established by this review. *(not verified)* | `supported` connection configuration; deployed privilege separation *(not verified)*. |
| TB7 | Tenant → tenant: identifiers, data and authority. | Tenant middleware and handler/store scoping are separate checks (`core/controlplane/gateway/gateway.go`, `core/controlplane/gateway/handlers_mcp_upstreams.go`, `core/edge/export.go`). Application checks do not substitute for trusted shared storage. | `supported`. |
| TB8 | Administrators → policy/configuration: publication, rules and keys. | Policy permissions and signature modes exist (`core/controlplane/gateway/handlers_policy_bundles.go`, `core/controlplane/gateway/auth/rbac.go`, `core/controlplane/safetykernel/file_loader.go`). A signature authenticates a trusted publisher; it does not make that publisher honest. | `supported`. |
| TB9 | Cordum → external systems: the actual protected effect. | Agents/workers are assumed to hold external credentials independently; no exhaustive credential-ownership inventory was measured. *(not verified)* Credential brokering is M2 (planned); universal resource mediation is outside the current claim. | `unsupported` universal mediation; broker `planned`. |

### Relationship to the Edge P0 threat model

The Edge document remains the historical Edge P0 closure checklist, not a current platform certification. This platform model uses the stricter vocabulary above. The table reconciles all eleven threat-table rows plus other differing/stale statements; an agreement or rejected candidate is labeled explicitly. Component status caps are listed separately below, by id only, rather than explaining findings here.

| Edge P0 statement (cite) | what this document says | why they differ (cite) |
| --- | --- | --- |
| E1: Local hook-config deletion; enterprise rollout deferred (`docs/internal/security/edge-p0-threat-model.md:262`, `docs/internal/security/edge-p0-threat-model.md:314`). | Managed-settings generation, export, verification and synthetic rollback tooling are `supported`; actual fleet rollout remains unmeasured. | Tooling now exists (`cmd/cordumctl/edge_managed_settings.go`, `docs/edge/managed-settings-deploy.md`); it is not an MDM controller or fleet proof. |
| E2: Raw agent launch; managed settings are the real control (`docs/internal/security/edge-p0-threat-model.md:263`). | Agreement on the boundary: wrapper-only universal enforcement is `unsupported`; managed mechanisms are `supported`, conditional on trusted deployment. | `cmd/cordum-claude/main.go` delegates to a launcher; `core/edge/claude/managed_settings.go` generates configuration, not a universal execution monitor. |
| E3: Binary replacement; signing and endpoint verification deferred (`docs/internal/security/edge-p0-threat-model.md:264`, `docs/internal/security/edge-p0-threat-model.md:316`). | Release-signing and endpoint install-time verification mechanisms are `supported`; continuous endpoint integrity and actual release provisioning are not certified. | Release workflow and install verifier now exist (`.github/workflows/release.yml`, `tools/scripts/install.sh`, `tools/scripts/install.ps1`, `tools/sign/verifier.go`, `docs/security/binary-signing.md`). Platform signing depends on configured secrets; no deployed result is inferred. |
| E4: Hook-token handling; environment tradeoff until keychain bootstrap (`docs/internal/security/edge-p0-threat-model.md:265`, `docs/internal/security/edge-p0-threat-model.md:317`). | Keychain bootstrap is `supported`, not wholly deferred; same-user/administrator trust still matters. | `core/edge/keychain/bootstrap.go`, `cmd/cordum-agentd/bootstrap.go` and `docs/security/agentd-keychain.md`; actual CLI default is dev unless strict is selected, so keychain code existence is not strict deployment proof. |
| E5: Secret reads marked Implemented (`docs/internal/security/edge-p0-threat-model.md:266`). | Classification/evaluation mechanisms are `supported`, not a universal secrecy guarantee. | Current mechanisms: `core/edge/classifier.go`, `core/controlplane/gateway/handlers_edge_evaluate.go`; this review has no pinned negative-effect qualification. |
| E6: Destructive shell marked Implemented (`docs/internal/security/edge-p0-threat-model.md:267`). | Governed-path classification is `supported`; arbitrary-code confinement is planned in M3. | `core/edge/classifier.go`, `docs/edge-074-policy-mechanism.md`; a classifier is not an OS sandbox. |
| E7: Direct shell/network versus MCP; full Gateway deferred (`docs/internal/security/edge-p0-threat-model.md:268`, `docs/internal/security/edge-p0-threat-model.md:319`). | Gateway-fronted MCP mechanisms are `supported`; universal external-effect mediation remains `unsupported`. | In-process policy wiring and upstream serving now exist (`core/controlplane/gateway/mcp_policy_wire.go`, `core/controlplane/gateway/mcp_upstream_serving.go`); an opt-in gate is not proof all traffic traverses it (`core/controlplane/gateway/handlers_mcp.go:41-50`). |
| E8: Gateway outage in strict mode marked Implemented (`docs/internal/security/edge-p0-threat-model.md:269`). | Mode-dependent decision handling is `supported`; full deployment/partition behavior is not qualified. | `core/edge/agentd/fail_modes.go`; M0-06 owns the planned failure matrix, and this document does not promote historical tests to current effect proof. |
| E9: Cross-tenant event access marked Implemented (`docs/internal/security/edge-p0-threat-model.md:270`). | Tenant checks are `supported`, not an exhaustive platform isolation certification. | Gateway tenant middleware and scoped export exist (`core/controlplane/gateway/gateway.go`, `core/edge/export.go`); deployed storage privilege separation is *(not verified)*. |
| E10: Prompt/tool-output leakage marked Implemented (`docs/internal/security/edge-p0-threat-model.md:271`). | Redaction and metadata export are `supported`, not proof of secrecy for every path/content. | `core/edge/redaction.go`, `core/edge/export.go`, `core/audit/`; current end-to-end disclosure resistance is not qualified here. |
| E11: No full Shadow scanner/store/dashboard (`docs/internal/security/edge-p0-threat-model.md:272`, `docs/internal/security/edge-p0-threat-model.md:318`). | The blanket absence is stale: scoped observe-mode scanning and finding storage exist; full universal runtime enforcement is not claimed. | `core/edge/shadow/scanner.go`, `core/edge/shadow/finding_store_redis.go`, `docs/edge/shadow-scanner.md` describe the narrower mechanism, not a universal detector. |
| E12: No Unix socket/named-pipe listener (`docs/internal/security/edge-p0-threat-model.md:169-175`). | Rejected contradiction candidate: loopback HTTP remains the configured local boundary. Listener handoff is not a Unix-domain/named-pipe feature. | `core/edge/agentd/config.go:176-183` rejects socket paths; `cmd/cordum-agentd/main.go:175-194` reconstructs an inherited listener. Platform handoff helpers are `core/edge/claude/listener_inheritance_unix.go` and `core/edge/claude/listener_inheritance_windows.go`. |
| E13: LLM proxy is only a template/placeholder (`docs/internal/security/edge-p0-threat-model.md:27-28`, `docs/internal/security/edge-p0-threat-model.md:320`). | LLM ingestion/evaluation integration exists; provider-token mediation and universal proxy coverage are not established here. | `core/controlplane/gateway/handlers_edge_llm_ingest.go` is disabled by default and produces advisory record/redact results; `docs/edge/llm-proxy-governance.md` describes integration, not deployed qualification. |
| E14: Global invariants called an uncrossable floor (`docs/internal/security/edge-p0-threat-model.md:49-77`). | Policy tiers and invariants are `supported` mechanisms, not an absolute platform promise. | `core/controlplane/safetykernel/`, `core/controlplane/gateway/`; distributed decisions and the inventory caps preclude a stronger status in this review. |
| E15: Gateway middleware anchors at former line numbers (`docs/internal/security/edge-p0-threat-model.md:83-90`). | Use anchors at the Reviewed commit, not historical line positions. | The chain is now at `core/controlplane/gateway/gateway.go:1236-1242`; old line 972 is in gRPC TLS setup (`core/controlplane/gateway/gateway.go:965-978`). |
| E16: Historical acceptance/evidence (`docs/internal/security/edge-p0-threat-model.md:146-150`, `docs/internal/security/edge-p0-threat-model.md:282-287`). | Those are historical runs, not this commit's negative-effect qualification; no current release-qualified status is claimed. | The Edge evidence table itself records a skipped local fake-hook integration invocation (`docs/internal/security/edge-p0-threat-model.md:286`); source review and old unit results are not deployed proof. |

## Trusted components

Findings R01-R15 are open per the roadmap review at `ebc0dd6a`; repro cases are planned in M0-08 to M0-12, and the findings have not been re-checked at the reviewed commit. *(not verified)*

A component is trusted when a protection claim depends on it deciding correctly or retaining a credential/secret. A client in the agent's trust domain is untrusted even if it performs useful checks; the trusted service must re-check its requests. Status measures protection of the decision or component, not mere installation. All positive statuses below are `supported`, not bypass-resistance or release qualification.

Roadmap status caps (ids only; provenance is the review above):

- Safety Kernel: R05, R06, R07, R08.
- API Gateway: R01, R03, R04, R06, R09, R12, R13, R14.
- Scheduler: R01, R08, R10.
- Edge agentd: R01, R09, R14.
- Edge hook: R02, R03.
- NATS admission / Redis replay store: R10.
- Audit chain: R11, R12.
- Policy source and publication: R06, R07, R13.
- Every entry point: R15.

### Trusted-component inventory

The inventory records authority, compromise consequences and protection status.

| Component | Code | What it decides | What a compromise allows | Current enforcement status |
| --- | --- | --- | --- | --- |
| Safety Kernel | `cmd/cordum-safety-kernel/`; `core/controlplane/safetykernel/` | Policy evaluation and input/output decisions. | Corrupt policy responses used by callers. | `supported`: policy evaluation and configurable transport/signature mechanisms; trusted service, not independently qualified. |
| API Gateway | `cmd/cordum-api-gateway/`; `core/controlplane/gateway/` | Auth, RBAC, tenant checks; HTTP/gRPC submit, Edge evaluation, approvals, policy bundles; in-process MCP action gates (`core/controlplane/gateway/mcp_policy_wire.go`, `core/mcp/policy_evaluate.go`). The MCP dispatcher runs the local pipeline, not a kernel RPC. | Impersonate API decisions, alter publication/approvals and use service credentials. | `supported`: middleware and decision mechanisms, not universal mediation. |
| Scheduler | `cmd/cordum-scheduler/`; `core/controlplane/scheduler/` | Safety before dispatch, approval fast path with request/snapshot checks, constraints, worker attestation and saga compensation (`core/controlplane/scheduler/engine.go`, `core/controlplane/scheduler/saga.go`, `core/controlplane/scheduler/credential_cache.go`). | Dispatch unauthorized jobs or corrupt their state. | `supported`: dispatch controls depend on trusted scheduler/store and selected modes. |
| Workflow Engine | `cmd/cordum-workflow-engine/`; `core/workflow/` | Advances runs; executes inline condition/delay/approval/notify/transform/storage/switch operations; submits worker jobs (`core/workflow/engine.go`, `docs/adr/004-inline-vs-dispatch-steps.md`). | Mutate run data or initiate jobs outside the intended workflow. | `supported`: separate inline and dispatch paths; no claim every step traverses the scheduler. |
| Context Engine | `cmd/cordum-context-engine/`; `core/contextwindow/engine/` | Assembles context and reads/writes stored memory; receives `REDIS_URL` (`cmd/cordum-context-engine/main.go:84`). | Disclose or corrupt memory and exercise its store credentials. | `supported`: service/connection configuration; deployed Redis least privilege *(not verified)*. |
| Edge agentd | `cmd/cordum-agentd/`; `core/edge/agentd/`; `core/edge/keychain/` | Holds endpoint session credentials and translates Gateway outcomes under configured fail modes. | Falsify local decisions/evidence or misuse retained credentials. | `supported` only on a trusted endpoint: nonce/keychain and managed deployment; not same-user isolation (`docs/security/agentd-keychain.md`). |
| Edge hook | `cmd/cordum-hook/`; `core/edge/claude/` | Maps agent hook input and agentd responses to the agent hook contract. | Falsify the answer presented to the agent. | `supported` only on a trusted endpoint: managed settings and release-integrity mechanisms (`core/edge/claude/managed_settings.go`, `docs/security/binary-signing.md`), not an OS security boundary. |
| Operator CLI | `cmd/cordumctl/`; `cmd/cordumctl/delegation_keygen.go`, `cmd/cordumctl/certs.go`, `cmd/cordumctl/audit.go` | Generates keys/certificates and renders audit verification results. API subcommands and the `edge claude` launcher (`cmd/cordumctl/edge_claude.go`) are clients the Gateway authorizes. | Exfiltrate generated/operator credentials or misrepresent evidence to the operator. | `supported` operator trust; client-side checks are not an independent enforcement point. |
| Governance helper | `cmd/cordumctl-governance-helper/`; `cmd/cordumctl-governance-helper/governance.go` | Backfills `gov:dec:` records and tails `sys.audit.export` with direct Redis/NATS URLs. | Write misleading decision records and exercise its infrastructure credentials. | `supported` tooling; isolation of those credentials *(not verified)*. |
| Redis | `core/infra/config/config.go`, `core/configsvc/`, `core/infra/replay/redis_store.go` | Stores state used as decision authority; not itself the business-policy evaluator. | Corrupt stored authority, approvals, replay state or evidence within granted permissions. | `supported` connection configuration; per-key protection in the deployment *(not verified)*. |
| NATS | `core/infra/bus/`, `cmd/cordum-scheduler/handshake_security_keys.go` | Routes workload/control packets; admission and worker-identity mechanisms depend on configured modes. | Disrupt delivery or misuse broker privileges; application admission remains a separate check. | `supported`: transport/admission mechanisms, not qualified effect semantics. |
| Policy source and publication | `core/controlplane/safetykernel/file_loader.go`, `core/controlplane/gateway/handlers_policy_bundles.go` | Supplies file/URL policies, bundles, signing keys and effective rules. | Change authorization through trusted publishing authority. | `supported`: signature modes and API permissions; publisher honesty remains trusted. |
| Audit chain and exporters | `core/audit/` | Records, chains and exports event evidence. | Omit or misrepresent events, or misuse exporter/signing credentials. | `supported`: evidence mechanisms, not proof that every event was recorded or truthful. |
| License/entitlement resolver | `core/licensing/resolver.go`, `core/licensing/license.go` | Resolves signed license claims into enabled features and limits. | Alter enabled rights/limits and the operator's view of entitlement. | `supported`: license verification is not workload authorization. |
| Deployment configuration | `cordum-helm/`, `deploy/k8s/` | Selects service reachability, identities, modes and storage. | Replace services or relax every control that depends on those settings. | `supported` configuration artifacts; M0-02 reference-deployment qualification is `planned`. |

### Explicit untrusted list

Clients and workload-controlled components require re-checks at trusted boundaries.

- `cmd/cordum-claude/`: thin alias invoking the CLI launcher, developer convenience rather than authority; Gateway checks still apply (`cmd/cordum-claude/main.go:35-55`, `core/controlplane/gateway/gateway.go`).
- `cmd/cordum-mcp/`: bridge calling Gateway REST with its configured API key; stdio lives in the client's trust domain and its optional local scope check is advisory relative to that domain. Gateway authorization must be authoritative (`cmd/cordum-mcp/main.go:103-155`, `core/controlplane/gateway/gateway.go`).
- Agents and tool calls: workload-supplied requests, re-checked by the configured Edge/MCP/submit paths (`core/controlplane/gateway/handlers_edge_evaluate.go`, `core/mcp/policy_evaluate.go`).
- Repository scripts: agent-domain executable inputs; classifier inspection is not confinement (`core/edge/classifier.go`, `docs/edge-074-policy-mechanism.md`). Arbitrary code confinement is M3 (planned).
- MCP plugins: upstream descriptions and results are untrusted inputs, with registry/use-time and invocation checks at the Gateway (`core/controlplane/gateway/mcp_upstream_serving.go`, `core/mcp/policy_evaluate.go`).
- Model output: proposals and text must be checked against backend authority, not accepted as grants (`core/mcp/policy_evaluate.go:109-114`, `core/policy/actiongates/`).
- External workers and packs: CAP participants are not the policy authority; configured admission and Scheduler checks mediate dispatch (`core/infra/bus/raw_admission.go`, `core/controlplane/scheduler/engine.go`). They must still be trusted for honest execution when holding external credentials; that execution property is not certified here.
- Dashboard SPA: an API client, not a trusted enforcement surface; Gateway middleware is the re-check (`core/controlplane/gateway/gateway.go`).
- Every other API client: possession of client code or a client-side allow does not replace server authorization (`core/controlplane/gateway/gateway.go`, `core/controlplane/gateway/auth/rbac.go`).

## Reference-monitor properties

These properties assess mediation, tamper resistance and analyzability without assuming deployment qualification.

The roadmap invariants I01-I10 are targets, not achieved certifications: authority before effects; restriction fidelity; hard-prohibition precedence; policy identity; authorized-input fidelity; delivery versus completion; bounded revocation; honest unknown outcomes; required evidence; and non-weakening execution paths. M0-03/M0-04/M0-13 are planned contract/entry-point checks.

### Always mediated

Requirement: every protected effect must pass an authorization point that its workload cannot skip.

There are distinct mediation mechanisms: Gateway Edge calls the kernel (`core/controlplane/gateway/handlers_edge_evaluate.go:433-439`), the Scheduler checks before dispatch (`core/controlplane/scheduler/engine.go`), and Gateway MCP runs an in-process action-gate dispatcher without a kernel RPC (`core/controlplane/gateway/mcp_policy_wire.go:75-88`, `core/mcp/policy_evaluate.go`). The Scheduler also has a stored-approval fast path, with request-hash and policy-snapshot checks (`core/controlplane/scheduler/engine.go:2276-2340`). These are separate decision paths, not evidence of one universal monitor.

Inline workflow steps execute inside the engine, without job dispatch (`core/workflow/engine.go:1163-1195`, `core/workflow/engine.go:2306-2340`, `docs/adr/004-inline-vs-dispatch-steps.md`). Edge observe-mode failure handling can return allow with degraded evidence (`core/edge/agentd/fail_modes.go:88-160`). The Scheduler can also convert an unavailable safety decision to allow when its effective input fail mode is open (`core/controlplane/scheduler/engine.go:1709-1729`); selectors include `POLICY_CHECK_FAIL_MODE` and configured `input_fail_mode` values (`cmd/cordum-scheduler/main.go:497-504`, `core/controlplane/scheduler/failmode.go:75-142`). Production-identity enforcement forces both input and async-output fail-mode helpers closed (`core/controlplane/scheduler/engine.go:557-580`), but that guard is not a claim that every deployment selects it. TB9's independently held external credentials remain a deployment assumption, not an exhaustively measured boundary. *(not verified)*

Current status: `supported` for the listed paths; universal effect mediation is `unsupported`.

### Tamper resistant

Requirement: the workload must not be able to alter the monitor, its policy, credentials or decision evidence.

Policy signature modes exist for file/URL policy loading (`core/controlplane/safetykernel/file_loader.go`); Gateway bundle publication is a privileged path (`core/controlplane/gateway/handlers_policy_bundles.go`). Redis policy/configuration writes use the connection's permissions (`core/configsvc/service.go:53-70`, `core/configsvc/service.go:84-126`): anyone with the corresponding write-capable `REDIS_URL` credentials can exercise those permissions. A URL alone is not proof of unrestricted Redis rights; deployed ACL separation is *(not verified)*.

Kernel gRPC server TLS is mandatory only when production mode is selected, and this wiring does not require client certificates (`core/controlplane/safetykernel/kernel.go:349-371`). Endpoint keychain/managed settings and install-time signature/hash verification exist (`core/edge/keychain/bootstrap.go`, `core/edge/claude/managed_settings.go`, `tools/scripts/install.sh`, `tools/scripts/install.ps1`). Actual signing-key provisioning and installed-artifact integrity on any endpoint were not measured; no runtime enclave or hostile-administrator protection is claimed (`docs/security/binary-signing.md`).

Current status: `supported` mechanisms under trusted administration; resistance to a hostile endpoint administrator is `unsupported`.

### Small enough to analyze

Requirement: the entire authorization trusted computing base must be small and explicit enough for a reviewer to analyze as one boundary.

The decision surfaces are distributed: kernel job/input/output decisions (`core/controlplane/safetykernel/kernel.go`, `core/controlplane/safetykernel/input_policy.go`, `core/controlplane/safetykernel/output_policy.go`); rule evaluation (`core/infra/config/safety_policy.go`); Gateway HTTP/gRPC authorization, Edge outcomes and human/MCP approvals (`core/controlplane/gateway/`, `core/controlplane/gateway/auth/`); MCP dispatch and approval handling (`core/mcp/`); action-gate decisions (`core/policy/actiongates/`); Scheduler safety/approval, worker dispatch and compensation (`core/controlplane/scheduler/`); workflow inline approval/control decisions (`core/workflow/engine.go`); endpoint classification, hook response and fail-mode decisions (`core/edge/`). This is a package-level review map, not a claim that supporting auth/storage dependencies are excluded from trust.

Measured at the reviewed commit, recursive tracked non-test Go lines (including comments and blank lines), per package tree and combined:

| package tree | lines |
| --- | ---: |
| `core/controlplane/safetykernel/` | 8,049 |
| `core/controlplane/gateway/` | 60,158 |
| `core/controlplane/scheduler/` | 13,793 |
| `core/workflow/` | 8,978 |
| `core/edge/` | 36,263 |
| `core/mcp/` | 14,741 |
| `core/policy/actiongates/` | 2,997 |
| `core/infra/config/` | 3,299 |
| Total | 148,278 |

Reproduce at the reviewed commit with `git ls-files -- <dirs> | grep -E '[.]go$' | grep -vE '_test[.]go$' | xargs cat | wc -l`, substituting each package tree above and then all eight together. This count is a size proxy, not a complexity metric or proof of correctness. A 148,278-line distributed decision surface does not substantiate a small single reference monitor.

Current status: `unsupported` as a small, comprehensively analyzed platform monitor; reducing/qualifying the boundary remains planned.

## Non-promises

These exclusions bound the security promise rather than weakening an implicit universal guarantee.

1. Protection against a hostile endpoint administrator on a trusted endpoint. Why: the administrator controls the OS, binaries and managed settings that the Edge path relies on (`docs/security/binary-signing.md`, `docs/security/agentd-keychain.md`).
2. Universal understanding of arbitrary program behavior. Why: classifiers inspect declared actions rather than all consequences of arbitrary execution, so such code needs confinement rather than presumed understanding (`core/edge/classifier.go`, `docs/edge-074-policy-mechanism.md`).
3. Automatic reversal of committed external changes. Why: undo depends on the external system; Cordum's compensation jobs are best effort, not transactional reversal (`core/controlplane/scheduler/saga.go:360-417`), and broader reconciliation is planned.
4. Generic exactly-once external effects. Why: even single-use authorization cannot remove uncertainty after a remote commit but before its response is saved; the roadmap lifecycle calls for reconciliation rather than reporting that uncertainty as success, and is not a delivered lifecycle claim.
5. That a signed receipt proves the signer was honest. Why: a signature associates a key with bytes, not the truth of the signer's statement; key ownership and honest event production remain separate assumptions (`docs/audit-chain.md`, `docs/mcp/outbound-signing.md`).

## Deployment boundary

The intended promise is enforceable authorization within a declared, tested deployment boundary, with no claim outside it. The following are necessary conditions for qualification, not evidence that a deployment has met them.

### Inside the boundary

| component/condition | setting and source default | qualification requirement |
| --- | --- | --- |
| Gateway transport | `GATEWAY_HTTP_TLS_CERT` / `GATEWAY_HTTP_TLS_KEY` default empty; so do `GRPC_TLS_CERT` / `GRPC_TLS_KEY`. Production rejects missing TLS (`core/controlplane/gateway/gateway.go:963-988`, `core/controlplane/gateway/gateway.go:1274-1283`). | Configure authenticated clients, server TLS and trusted keys; HTTP/gRPC authorization remains required. |
| Production mode and kernel transport | `CORDUM_PRODUCTION` defaults false; `CORDUM_ENV` defaults empty, with prod/production selecting production. Kernel certificate/key default empty and are required in production (`core/infra/env/env.go:19-40`, `core/controlplane/safetykernel/kernel.go:349-371`). | Select production explicitly; configure `SAFETY_KERNEL_TLS_CERT` / `SAFETY_KERNEL_TLS_KEY`, and restrict private kernel access rather than treating server TLS as caller authentication. |
| Redis and NATS | `REDIS_URL` defaults to local Redis on port 6379; `NATS_URL` to local NATS on 4222, without embedded credentials. Production requires explicit values (`core/infra/config/config.go:11-14`, `core/infra/config/config.go:48-57`, `core/infra/env/env.go:81-100`). TLS options apply only to `rediss://` and `tls://` respectively (`core/infra/redisutil/redisutil.go:117-131`, `core/infra/bus/nats.go:119-168`). | Private network, credentialed connections, constrained identities and TLS with peer verification on both links. Use those TLS URL schemes, trusted CAs (`REDIS_TLS_CA`, `NATS_TLS_CA`) and matching peer names; keep `REDIS_TLS_INSECURE` and `NATS_TLS_INSECURE` false (`core/infra/redisutil/redisutil.go:163-213`, `core/infra/bus/nats.go:1005-1044`). Private networking or credentials alone do not satisfy D7; actual ACL/TLS deployment is *(not verified)*. |
| Scheduler input safety failure | `POLICY_CHECK_FAIL_MODE` is unset by default, giving closed behavior (`cmd/cordum-scheduler/main.go:497-499`, `core/controlplane/scheduler/engine.go:515-520`). Tenant `input_fail_mode` inherits the system value when unset; an unconfigured resolver is also closed (`core/controlplane/scheduler/failmode.go:44-83`, `core/controlplane/scheduler/failmode.go:105-142`). | Require effective `closed` for protected jobs, including system and tenant configuration; defaults alone are not qualification. Config reload can set the system value (`cmd/cordum-scheduler/config_overlay.go:318-336`). Closed handling requeues unavailable checks (`core/controlplane/scheduler/engine.go:1737-1741`). |
| Scheduler async-output safety failure | `OUTPUT_POLICY_FAIL_MODE` is unset by default, giving closed behavior when output checks are enabled (`cmd/cordum-scheduler/main.go:491-496`, `core/controlplane/scheduler/engine.go:502-506`). Tenant `output_fail_mode` inherits the system value when unset (`core/controlplane/scheduler/failmode.go:86-94`, `core/controlplane/scheduler/failmode.go:105-157`). | Require effective `closed` for protected jobs at system and tenant scope; select and qualify the output-check path, not just its fail-mode flag. Config reload also selects this mode (`cmd/cordum-scheduler/config_overlay.go:318-336`); async errors quarantine in closed mode rather than return as allowed (`core/controlplane/scheduler/engine.go:2819-2839`). |
| Worker identity and readiness | `WORKER_ATTESTATION` defaults `off` (`core/controlplane/scheduler/credential_cache.go:39-49`); `WORKER_READINESS_REQUIRED` defaults false and readiness TTL to 60s (`core/controlplane/scheduler/readiness.go:14-46`). `CORDUM_SDK_HANDSHAKE` has no accepted empty default (`core/infra/handshakemode/mode.go`). | Select and qualify a worker-authentication mode and readiness policy explicitly. Active legacy attestation and active session-handshake modes cannot be combined (`cmd/cordum-scheduler/worker_security_modes.go:11-29`); do not blindly enable both. |
| Trusted Edge endpoint | `CORDUM_EDGE_POLICY_MODE` defaults `observe`; `CORDUM_AGENTD_FAIL_CLOSED` defaults false (`core/edge/agentd/config.go:50-78`). Bootstrap defaults dev unless `CORDUM_AGENTD_STRICT` or enterprise-strict mode selects strict (`cmd/cordum-agentd/bootstrap.go:81-94`). | Enterprise-strict, strict keychain bootstrap, deployed managed settings and controlled binaries; generated managed settings select strict mode (`core/edge/claude/managed_settings.go:56-83`), but deployment and integrity require measurement. |
| Policy signatures | File/URL `CORDUM_POLICY_STRICT` defaults to `warn` outside production and `enforce` in production; legacy `SAFETY_POLICY_SIGNATURE_REQUIRED` selects enforce (`core/controlplane/safetykernel/file_loader.go:86-114`). | Configure trusted keys and enforce-mode publication/loading; qualify bundle policy separately, not just a file-policy flag (`core/controlplane/safetykernel/policy_signing.go`). |
| Gateway MCP policy path | `mcp.policy_gate_enabled` defaults false (`core/controlplane/gateway/handlers_mcp.go:41-50`). | Explicitly configure and measure the intended MCP gate path; existence of a server is not evidence it mediates all calls. |

### Outside the boundary

Excluded are the hostile endpoint administrator, unmanaged developer machines, independently agent-held external credentials, external systems' own authorization/commit semantics, and any configuration that weakens the conditions above. Observe mode is explicitly non-strict in the local failure policy (`core/edge/agentd/fail_modes.go:150-160`); Scheduler input/async-output fail-open configurations are also outside the protected-job boundary (`core/controlplane/scheduler/engine.go:502-520`, `core/controlplane/scheduler/engine.go:1709-1729`, `core/controlplane/scheduler/engine.go:2819-2839`). Redis/NATS links without peer-verified TLS do not meet the transport conditions above. No protected-effect claim extends to an unmeasured alternative route.

### Reference deployment

M0-02 defines the reference deployment (planned), using the existing `cordum-helm/` and `deploy/k8s/` artifacts. No deployment is qualified by this document: not measured. Qualification must pin source/artifact/policy/authority versions, executor/platform and deployment settings, test corpus, negative-effect evidence and open exceptions; source review or a successful documentation verifier is not deployed security evidence.
