# Docs visibility policy

Cordum's `docs/` tree is split into PUBLIC (root and most subdirectories,
intended for users landing on the GitHub repo) and PRIVATE (`docs/internal/`,
intended for Cordum engineering only). This file is the canonical inventory
of every `*.md` under `docs/`, the classification rule that governs new
docs, and the verification gates that keep the public/private boundary
clean.

It is itself PUBLIC.

## Classification rules

A doc is **PUBLIC** when it is one of:

- User-facing reference (API, CLI, SDK, MCP, configuration, deployment).
- Quickstart / getting-started / tutorial / demo walkthrough.
- Public ADR overview (design rationale shipped alongside the code).
- Public security model summary or operator-facing security guidance
  (secret rotation, TLS setup, SSO, audit subsystem reference).
- Operator-facing runbook (audit operations, worker health, output policy).
- Architecture explainer that is referenced from public reference docs.
- Release notes for the published codebase.

A doc is **PRIVATE** (lives under `docs/internal/`) when it is one of:

- Threat-model details with code-path-level mitigations.
- RBAC route audit, enterprise entitlement matrix, or any audit artifact
  that catalogues call sites with grep counts and task IDs.
- Internal sweep / cleanup audit reports (legacy code removals, deprecated
  symbol catalogs, OpenAPI artifact pruning).
- Bug-hunt audits with code-path analysis.
- Internal decision log entries (rejected approaches, trade-off analyses
  citing internal task IDs).
- Internal issue drafts (e.g. SEC-* security tickets with severity ratings
  and operational mitigation notes).
- EDGE-031 / EDGE-032 closure / acceptance evidence.
- Internal verification artifacts for deferred features (e.g. vLLM config
  verification while LLM chat is parked).

## Reference graph rule

Public docs MUST cite only other public docs. Private docs MAY cite
public docs. The graph is one-way. Verification gates target markdown
link syntax `(...path...)`, not raw string mentions — this file is
itself a public inventory and legitimately mentions every internal
path in code spans and table cells:

```
# (a) no public→private markdown link (matches `](path)` syntax only)
grep -rEn '\]\(\.\./internal/|\]\(docs/internal/' docs/ | grep -v '^docs/internal/'

# (b) no top-level link into private subtree
grep -nE '\]\(docs/internal|\]\(\.\./docs/internal' README.md *.md
```

Both must return zero matches.

## Inventory (167 .md files)

Counts: PUBLIC = 139, PRIVATE = 28. Both totals exclude this file
(`docs/visibility-policy.md`) which is itself PUBLIC and brings the
grand total to 168.

### PUBLIC — `docs/` root level

Top-level user-facing reference docs. PUBLIC by default.

| Path | Rationale |
|---|---|
| `docs/AGENT_PROTOCOL.md` | Public CAP wire-format / agent protocol reference. |
| `docs/CORE.md` | Public core architecture reference. |
| `docs/DOCKER.md` | Public Docker setup reference. |
| `docs/LOCAL_E2E.md` | Public local E2E test runner guide. |
| `docs/README.md` | Public docs landing page. |
| `docs/SCHEDULER_POOL_SPEC.md` | Public scheduler pool wire spec. |
| `docs/api-reference.md` | Public REST API reference. |
| `docs/api.md` | Public API summary. |
| `docs/audit-chain.md` | Public audit chain architecture reference. |
| `docs/audit-operations.md` | Public audit chain operator runbook. |
| `docs/audit.md` | Public audit subsystem reference. |
| `docs/backend_capabilities.md` | Public backend feature reference. |
| `docs/backend_feature_matrix.md` | Public backend feature matrix. |
| `docs/cli-reference.md` | Public CLI reference. |
| `docs/configuration-reference.md` | Public configuration reference. |
| `docs/configuration.md` | Public configuration overview. |
| `docs/context-engine.md` | Public context engine reference. |
| `docs/cordumctl.md` | Public `cordumctl` CLI overview. |
| `docs/dashboard-guide.md` | Public dashboard usage guide. |
| `docs/demo-edge-claude-spike.md` | Public EDGE-000 spike walkthrough. |
| `docs/demo-edge-claude.md` | Public Edge Claude demo walkthrough. |
| `docs/demo-guardrails.md` | Public guardrails demo. |
| `docs/demo-mock-bank.md` | Public mock-bank demo. |
| `docs/edge-api.md` | Public Edge API summary. |
| `docs/edge-claude-code.md` | Public Edge + Claude Code reference. |
| `docs/edge-export.md` | Public Edge evidence export reference. |
| `docs/edge-observability.md` | Public Edge observability reference. |
| `docs/edge-policy.md` | Public Edge policy reference. |
| `docs/edge.md` | Public Edge product overview. |
| `docs/enterprise.md` | Public enterprise feature overview. |
| `docs/faq.md` | Public FAQ. |
| `docs/getting_started.md` | Public getting-started page. |
| `docs/grpc-services.md` | Public gRPC service reference. |
| `docs/helm.md` | Public Helm chart reference. |
| `docs/horizontal-scaling.md` | Public horizontal scaling guide. |
| `docs/install.md` | Public install guide. |
| `docs/k8s-deployment.md` | Public Kubernetes deployment guide. |
| `docs/mcp-integration.md` | Public MCP integration overview. |
| `docs/mcp-resources-reference.md` | Public MCP resources reference. |
| `docs/mcp-server.md` | Public MCP server reference. |
| `docs/mcp-tools-reference.md` | Public MCP tools reference. |
| `docs/output-policy.md` | Public output policy operator guide. |
| `docs/output-safety.md` | Public output safety reference. |
| `docs/pack.md` | Public pack format reference. |
| `docs/performance-tuning.md` | Public performance tuning guide. |
| `docs/production-gate.md` | Public production gate runner reference. |
| `docs/production.md` | Public production deployment guide. |
| `docs/quickstart-cold-test.md` | Public cold-test quickstart checklist. |
| `docs/quickstart-edge.md` | Public Edge quickstart (EDGE-033). |
| `docs/quickstart.md` | Public canonical quickstart. |
| `docs/redis-operations.md` | Public Redis operator runbook. |
| `docs/redis-security.md` | Public Redis security guide. |
| `docs/repo_split.md` | Public repo layout overview. |
| `docs/safety-kernel.md` | Public safety kernel reference. |
| `docs/scheduler-internals.md` | Public scheduler internals reference. |
| `docs/scheduler.md` | Public scheduler reference. |
| `docs/sdk-reference.md` | Public SDK reference. |
| `docs/secret-rotation.md` | Public secret rotation runbook. |
| `docs/system_overview.md` | Public system overview. |
| `docs/troubleshooting.md` | Public troubleshooting guide. |
| `docs/websocket-streaming.md` | Public WebSocket streaming reference. |
| `docs/workflow-step-types.md` | Public workflow step types reference. |
| `docs/visibility-policy.md` | This file (PUBLIC). |

### PUBLIC — `docs/adr/`

10 ADRs. Public design rationale shipped alongside the code.

| Path | Rationale |
|---|---|
| `docs/adr/001-safety-before-dispatch.md` | Public ADR. |
| `docs/adr/002-context-pointers.md` | Public ADR. |
| `docs/adr/003-redis-nats-split.md` | Public ADR. |
| `docs/adr/004-inline-vs-dispatch-steps.md` | Public ADR. |
| `docs/adr/005-output-policy-architecture.md` | Public ADR. |
| `docs/adr/006-circuit-breaker-safety.md` | Public ADR. |
| `docs/adr/007-dashboard-state-management.md` | Public ADR. |
| `docs/adr/008-spa-auth-localstorage.md` | Public ADR. |
| `docs/adr/009-control-plane-boundary-hardening.md` | Public ADR. |
| `docs/adr/010-edge-p0-architecture-decisions.md` | Public Edge P0 ADR. |

### PUBLIC — `docs/api/` and `docs/api/openapi/`

Public API reference + OpenAPI artifacts.

| Path | Rationale |
|---|---|
| `docs/api/audit-verify.md` | Public audit-verify API reference. |
| `docs/api/governance-health.md` | Public governance-health API reference. |
| `docs/api/openapi/AUDIT_BASELINE.md` | Public OpenAPI audit baseline. |
| `docs/api/openapi/CHANGELOG.md` | Public OpenAPI changelog. |
| `docs/api/openapi/ERROR_CODE_AUDIT.md` | Public OpenAPI error-code audit. |
| `docs/api/openapi/README.md` | Public OpenAPI README. |
| `docs/api/openapi/SCHEMA_DRIFT.md` | Public OpenAPI schema-drift reference. |

### PUBLIC — single-file subdirs

| Path | Rationale |
|---|---|
| `docs/architecture/heartbeat-demotion.md` | Public phase-2 boundary-hardening architecture explainer (cited by sdk/handshake.md and operations/runbook-worker-health.md). |
| `docs/audit/mcp-events.md` | Public SIEM event catalog for MCP integrators. |
| `docs/auth/delegation.md` | Public delegation reference. |
| `docs/compliance/soc2_mapping.md` | Public SOC2 mapping. |
| `docs/dashboard/README.md` | Public dashboard README. |
| `docs/governance/decision-log.md` | Public Policy Decision Log API documentation. |
| `docs/operations/runbook-worker-health.md` | Public operator-facing worker health runbook. |
| `docs/packs/signing.md` | Public pack signing reference. |
| `docs/release-notes/unreleased.md` | Public unreleased changelog. |
| `docs/sdk/handshake.md` | Public SDK worker handshake reference. |
| `docs/sso/okta.md` | Public Okta SSO pointer. |
| `docs/topics/registry.md` | Public topic registry reference. |

### PUBLIC — `docs/deployment/`

| Path | Rationale |
|---|---|
| `docs/deployment/README.md` | Public deployment README. |
| `docs/deployment/audit-chain-migration.md` | Public audit chain migration guide. |
| `docs/deployment/audit-chain.md` | Public deployment-side audit chain reference. |
| `docs/deployment/e2e-testing.md` | Public E2E testing guide. |
| `docs/deployment/ghcr-public-access.md` | Public GHCR access guide. |
| `docs/deployment/images.md` | Public deployment images reference. |
| `docs/deployment/policy-signing.md` | Public policy signing guide. |
| `docs/deployment/quickstart-env-contract.md` | Public quickstart env contract. |
| `docs/deployment/verification-checklist.md` | Public deployment verification checklist. |

### PUBLIC — `docs/edge/` (subset; `p0-acceptance-evidence.md` is PRIVATE)

| Path | Rationale |
|---|---|
| `docs/edge/README.md` | Public Edge docs index (EDGE-029). |
| `docs/edge/api.md` | Public Edge API reference. |
| `docs/edge/claude-hook-mapper.md` | Public Claude hook mapper reference. |
| `docs/edge/cli.md` | Public Edge CLI reference. |
| `docs/edge/configuration.md` | Public Edge configuration reference. |
| `docs/edge/cordum-agentd.md` | Public agentd reference. |
| `docs/edge/cordum-hook.md` | Public cordum-hook reference. |
| `docs/edge/cordumctl-edge-claude.md` | Public `cordumctl edge claude` reference. |
| `docs/edge/cordumctl-edge-doctor.md` | Public `cordumctl edge doctor` reference. |
| `docs/edge/demo.md` | Public Edge demo walkthrough. |
| `docs/edge/managed-settings-template.md` | Public managed settings template reference. |
| `docs/edge/runbook.md` | Public Edge operator runbook. |

### PUBLIC — `docs/evals/`

| Path | Rationale |
|---|---|
| `docs/evals/datasets.md` | Public evals datasets reference. |
| `docs/evals/extraction.md` | Public evals extraction guide. |
| `docs/evals/runner.md` | Public evals runner guide. |

### PUBLIC — `docs/getting-started/`

| Path | Rationale |
|---|---|
| `docs/getting-started/mcp-with-claude-code.md` | Public Claude Code MCP onboarding. |
| `docs/getting-started/mcp-with-cursor.md` | Public Cursor MCP onboarding. |
| `docs/getting-started/mcp-with-vscode.md` | Public VS Code MCP onboarding. |

### PUBLIC — `docs/guides/`

| Path | Rationale |
|---|---|
| `docs/guides/production-deployment.md` | Public production deployment guide. |
| `docs/guides/tls-setup.md` | Public TLS setup guide. |

### PUBLIC — `docs/mcp/`

| Path | Rationale |
|---|---|
| `docs/mcp/_prereqs.md` | Public MCP prerequisites. |
| `docs/mcp/mcp-onboarding-cold-test.md` | Public MCP cold-test checklist. |
| `docs/mcp/mutating-tools.md` | Public MCP mutating-tools reference. |
| `docs/mcp/outbound-signing.md` | Public MCP outbound signing reference. |
| `docs/mcp/per-tool-approval.md` | Public per-tool approval reference. |
| `docs/mcp/prompts.md` | Public MCP prompts reference. |
| `docs/mcp/quickstart-claude-code.md` | Public Claude Code MCP quickstart. |
| `docs/mcp/quickstart-cursor.md` | Public Cursor MCP quickstart. |
| `docs/mcp/quickstart-vscode.md` | Public VS Code MCP quickstart. |
| `docs/mcp/resources.md` | Public MCP resources reference. |
| `docs/mcp/scope-filtering.md` | Public MCP scope filtering reference. |
| `docs/mcp/scope-preapproval.md` | Public MCP scope preapproval reference. |
| `docs/mcp/tools.md` | Public MCP tools reference. |

### PUBLIC — `docs/static/img/*/`

Screenshot capture instructions for getting-started flows.

| Path | Rationale |
|---|---|
| `docs/static/img/mcp-claude-code/README.md` | Public screenshot capture instructions. |
| `docs/static/img/mcp-cursor/README.md` | Public screenshot capture instructions. |
| `docs/static/img/mcp-vscode/README.md` | Public screenshot capture instructions. |

### PUBLIC — `docs/troubleshooting/` and `docs/tutorials/`

| Path | Rationale |
|---|---|
| `docs/troubleshooting/install.md` | Public install troubleshooting. |
| `docs/tutorials/connect-datadog.md` | Public Datadog tutorial. |
| `docs/tutorials/langchain-guard.md` | Public LangChain guard tutorial. |

### PRIVATE — `docs/internal/`

Internal subtree introduced by EDGE-036. Banner at
`docs/internal/INTERNAL.md`. Not linked from public docs.

| Path | Rationale |
|---|---|
| `docs/internal/INTERNAL.md` | Internal subtree banner. |
| `docs/internal/_audit/quickstart-sync-20260420.md` | Internal quickstart drift audit. |
| `docs/internal/bug-hunts/scheduler-lifecycle-2026-03-04.md` | Internal scheduler lifecycle bug-hunt. |
| `docs/internal/cleanup/README.md` | Internal cleanup journal index. |
| `docs/internal/cleanup/auth-license-compat-audit.md` | Internal compat-shim deletion audit. |
| `docs/internal/cleanup/backward-legacy-sweep-20260420.md` | Internal residual-legacy sweep audit. |
| `docs/internal/cleanup/deprecated-symbols-audit.md` | Internal deprecated-symbols audit. |
| `docs/internal/cleanup/openapi-legacy-audit.md` | Internal OpenAPI legacy audit. |
| `docs/internal/cleanup/versioned-docs-audit.md` | Internal versioned-docs audit. |
| `docs/internal/decisions/2026-04-atomic-store-and-hash.md` | Internal rejected-approach evaluation. |
| `docs/internal/edge/p0-acceptance-evidence.md` | EDGE-032 acceptance evidence (operational sensitive). |
| `docs/internal/heartbeat-demotion-audit.md` | Internal heartbeat-demotion call-site audit. |
| `docs/internal/issue-drafts/security/SEC-001-rbac-oss.md` | Internal RBAC issue draft. |
| `docs/internal/issue-drafts/security/SEC-002-jwt-validation.md` | Internal JWT validation issue draft. |
| `docs/internal/issue-drafts/security/SEC-003-sso-saml-enterprise.md` | Internal SSO/SAML enterprise issue draft. |
| `docs/internal/issue-drafts/security/SEC-004-production-tls-hardening.md` | Internal TLS hardening issue draft. |
| `docs/internal/issue-drafts/security/SEC-005-audit-log-tamper-evident.md` | Internal audit-log tamper-evidence issue draft. |
| `docs/internal/issue-drafts/security/SEC-006-encryption-at-rest.md` | Internal encryption-at-rest issue draft. |
| `docs/internal/issue-drafts/security/SEC-007-secrets-manager-integration.md` | Internal secrets-manager issue draft. |
| `docs/internal/issue-drafts/security/SEC-008-cap-signature-verification.md` | Internal CAP signature issue draft. |
| `docs/internal/issue-drafts/security/SEC-009-api-key-rotation.md` | Internal API-key rotation issue draft. |
| `docs/internal/issue-drafts/security/SEC-010-distroless-image.md` | Internal distroless-image issue draft. |
| `docs/internal/issue-drafts/security/SEC-011-dependabot-snyk.md` | Internal Dependabot/Snyk issue draft. |
| `docs/internal/llmchat/vllm-config-verification.md` | Internal vLLM verification (LLM chat epic deferred). |
| `docs/internal/security/README.md` | Internal security audit-trail index. |
| `docs/internal/security/edge-p0-threat-model.md` | EDGE-031 Edge P0 threat model with code paths. |
| `docs/internal/security/enterprise-entitlement-matrix.md` | Internal enterprise entitlement matrix. |
| `docs/internal/security/rbac-route-audit-20260420.md` | Internal RBAC route audit. |

## Process for new docs

1. Decide PUBLIC or PRIVATE per the classification rules above.
2. PUBLIC docs land at `docs/<path>.md`. PRIVATE docs land at
   `docs/internal/<path>.md` (preserving any existing subdirectory
   layout — e.g. `docs/internal/security/foo.md`, not
   `docs/internal/foo.md`).
3. PUBLIC docs cite only PUBLIC docs. PRIVATE docs may cite either.
4. Re-run the verification gates above before opening the PR.
5. If a doc has both public-safe summary and private operational
   detail, split it — public summary at the public path, private
   detail at the internal path, cross-link from internal to public.

## Known gaps

- The reorganization affects HEAD only. Historical commits still
  contain the pre-move paths under the public tree, by design (task
  rail #2 forbids history scrub).
- External link aggregators (e.g. cordum.io marketing pages, third-
  party blog posts) may continue to reference pre-move paths. Site
  owners must update those separately; this repo cannot enforce them.
- Inside `docs/internal/`, some audits cite each other using the
  pre-move public paths (e.g. `docs/cleanup/foo.md` instead of
  `docs/internal/cleanup/foo.md`). Those are archival audit snapshots
  that referenced the path at audit time; they are intentionally not
  rewritten per task rail #5 (no content rewrite of internal audits).
