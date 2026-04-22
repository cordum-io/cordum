# RBAC route audit — 2026-04-20

Durable checklist for the remaining enterprise RBAC/license hardening sweep. This file captures **every current `requireRole(` / `requireStoreAndRole(` handler callsite** in `core/controlplane/gateway/` as of 2026-04-20, plus the dashboard entitlement matrix the backend changes must stay aligned with.

## Method

- Enumerated gateway callsites with `rg -n "requireRole\(|requireStoreAndRole\(" core/controlplane/gateway --glob "!**/*_test.go"`.
- Ignored helper definitions in `helpers.go` and the `requireRole` method definition itself in `handlers_auth.go`.
- Mapped registered handlers to `gateway.go`; for handlers that are only described in comments/tests today, the route column uses the documented route and the note explicitly says the route was **not found in `gateway.go` registration**.
- Reused existing permissions from `core/controlplane/gateway/auth/rbac.go` where they already fit; proposed new `Perm...` constants only when no current permission namespace matched the surface cleanly.
- Cross-checked dashboard entitlement surfaces in `dashboard/src/pages/**`, `dashboard/src/hooks/useLicense.ts`, and the Settings hub cards so backend RBAC work does not drift from the UI truth surface.

## Inventory summary

- **67** handler callsites across **27** gateway files are in scope.
- Recommended disposition: **59 `MIGRATE`** + **8 `KEEP_ROLE_WITH_JUSTIFICATION`**.
- **16** rows are comment/test-defined routes that are not currently visible in `gateway.go` route registration; they are still included because the task asked for every handler callsite in `core/controlplane/gateway/`, not only wired routes.
- **Step-2 migration status (repo HEAD `93cb19f`)**: all **59** audited `MIGRATE` callsites now use permission-aware guards; the remaining raw-role callsites are the **8** intentional `KEEP_ROLE_WITH_JUSTIFICATION` entries below. During implementation I also found an extra MCP approval approve/reject shim outside the original 67-row audit and migrated it to `PermJobsApprove` so the live gateway raw-role count is still **8**.

## Existing permission namespaces to reuse

- `PermWorkflowsWrite` (`workflows.write`)
- `PermJobsApprove` (`jobs.approve`)
- `PermAgentsDelegate` (`agents.delegate`)
- `PermConfigRead` (`config.read`)
- `PermPolicyRead` (`policy.read`)
- `PermPolicyWrite` (`policy.write`)
- `PermPacksInstall` (`packs.install`)
- `PermAdminAll` (`admin.*`)

## New permission namespaces this audit recommends adding

- `PermAuditExport` / `PermAuditVerify`
- `PermAPIKeysRead` / `PermAPIKeysWrite`
- `PermDLQRead` / `PermDLQWrite`
- `PermMemoryRead`
- `PermLegalHoldRead` / `PermLegalHoldWrite`
- `PermLicenseRead`
- `PermLocksRead`
- `PermMCPRead` / `PermMCPVerify`
- `PermPacksRead` / `PermPacksVerify`
- `PermPoolsWrite`
- `PermTelemetryRead` / `PermTelemetryWrite` / `PermTelemetryExport`
- `PermTopicsRead` / `PermTopicsWrite`
- `PermWorkerCredentialsRead` / `PermWorkerCredentialsWrite`
- `PermWorkersWrite`

## Route-by-route callsite checklist

### Audit, identity, workflow, and settings

| file:line | route | current guard | intended permission | ownership | recommended action | notes |
|---|---|---|---|---|---|---|
| `core/controlplane/gateway/handlers_audit_compliance.go:61` | `GET /api/v1/audit/export` | admin + configSvc | `PermAuditExport` (`audit.export`) | Compliance / audit export | `MIGRATE` | Keep existing entitlement gate; route is documented in the handler but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_audit_verify.go:51` | `GET /api/v1/audit/verify` | admin + redis client | `PermAuditVerify` (`audit.verify`) | Compliance / audit integrity | `MIGRATE` | Integrity reporting should not require blanket admin; route is documented in the handler but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_auth.go:962` | `GET /api/v1/auth/keys` | admin + keyStore | `PermAPIKeysRead` (`apiKeys.read`) | Identity / access | `MIGRATE` | Settings API Keys is a real dashboard surface. |
| `core/controlplane/gateway/handlers_auth.go:989` | `POST /api/v1/auth/keys` | admin + keyStore | `PermAPIKeysWrite` (`apiKeys.write`) | Identity / access | `MIGRATE` | Create is the mutating half of the API-key surface. |
| `core/controlplane/gateway/handlers_auth.go:1070` | `DELETE /api/v1/auth/keys/{id}` | admin + keyStore | `PermAPIKeysWrite` (`apiKeys.write`) | Identity / access | `MIGRATE` | Revoke belongs with the same API-key management permission as create. |
| `core/controlplane/gateway/handlers_chat.go:192` | `POST /api/v1/workflow-runs/{id}/chat` | admin, operator | `PermWorkflowsWrite` (`workflows.write`) | Workflow runtime | `MIGRATE` | Posting chat mutates run history; reuse the existing workflow write permission. |
| `core/controlplane/gateway/handlers_config.go:111` | `GET /api/v1/config` | store-only preflight (nil roles) + configSvc | `PermConfigRead` (`config.read`) | Config service | `KEEP_ROLE_WITH_JUSTIFICATION` | This callsite is not a legacy role decision; the real permission split already happens lower in the handler. |
| `core/controlplane/gateway/handlers_delegation.go:222` | `POST /api/v1/agents/revoke-delegation` | admin | `PermAgentsDelegate` (`agents.delegate`) | Agent delegation / trust | `MIGRATE` | Revoking a delegation token belongs with the delegation-issue surface, not the generic agent-profile write scope. |

### Jobs, licensing, locks, and compliance

| file:line | route | current guard | intended permission | ownership | recommended action | notes |
|---|---|---|---|---|---|---|
| `core/controlplane/gateway/handler_admin_locks.go:106` | `GET /api/v1/admin/locks` | admin | `PermAdminAll` (`admin.*`) | Platform / break-glass | `KEEP_ROLE_WITH_JUSTIFICATION` | Cross-system emergency lock inspection should stay raw-admin. |
| `core/controlplane/gateway/handlers_dlq.go:18` | `GET /api/v1/dlq` | admin + dlqStore | `PermDLQRead` (`dlq.read`) | Jobs / DLQ | `MIGRATE` | Read-only incident triage should not require blanket admin. |
| `core/controlplane/gateway/handlers_dlq.go:44` | `GET /api/v1/dlq/page` | admin + dlqStore | `PermDLQRead` (`dlq.read`) | Jobs / DLQ | `MIGRATE` | Pagination variant should match the same read permission as the list route. |
| `core/controlplane/gateway/handlers_dlq.go:95` | `DELETE /api/v1/dlq/{job_id}` | admin + dlqStore | `PermDLQWrite` (`dlq.write`) | Jobs / DLQ | `MIGRATE` | Deleting a DLQ record is a mutating incident-response action. |
| `core/controlplane/gateway/handlers_dlq.go:125` | `POST /api/v1/dlq/{job_id}/retry` | admin | `PermDLQWrite` (`dlq.write`) | Jobs / DLQ | `MIGRATE` | Retry replays a failed job and should share the same mutating DLQ permission. |
| `core/controlplane/gateway/handlers_jobs.go:175` | `GET /api/v1/status` | inline admin redaction (`requireRole(...) == nil`) | `PermAdminAll` (`admin.*`) | Observability / support | `KEEP_ROLE_WITH_JUSTIFICATION` | Not a route gate; it only controls whether sensitive diagnostics are included in the response. |
| `core/controlplane/gateway/handlers_jobs.go:720` | `GET /api/v1/memory` | admin + memStore | `PermMemoryRead` (`memory.read`) | Observability / support | `MIGRATE` | Raw memory-pointer inspection is sensitive, but it is still a distinct debug surface. |
| `core/controlplane/gateway/handlers_legal_hold.go:49` | `POST /api/v1/audit/legal-hold` | admin + legalHold entitlement | `PermLegalHoldWrite` (`legalHold.write`) | Compliance / audit retention | `MIGRATE` | Keep the existing feature entitlement check; swap the raw role for a write permission. |
| `core/controlplane/gateway/handlers_legal_hold.go:101` | `GET /api/v1/audit/legal-holds` | admin + legalHold entitlement | `PermLegalHoldRead` (`legalHold.read`) | Compliance / audit retention | `MIGRATE` | Compliance reviewers often need visibility without release powers. |
| `core/controlplane/gateway/handlers_legal_hold.go:126` | `DELETE /api/v1/audit/legal-hold/{id}` | admin + legalHold entitlement | `PermLegalHoldWrite` (`legalHold.write`) | Compliance / audit retention | `MIGRATE` | Release is the mutating half of the legal-hold surface. |
| `core/controlplane/gateway/handlers_license.go:9` | `GET /api/v1/license` | admin | `PermLicenseRead` (`license.read`) | Licensing / settings | `MIGRATE` | `/settings/license` depends on this read route. |
| `core/controlplane/gateway/handlers_license.go:30` | `POST /api/v1/license/reload` | admin | `PermAdminAll` (`admin.*`) | Platform / break-glass | `KEEP_ROLE_WITH_JUSTIFICATION` | Manual signed-license reload is a recovery path; later pair it with expiry-aware break-glass rules. |
| `core/controlplane/gateway/handlers_license.go:54` | `GET /api/v1/license/usage` | admin | `PermLicenseRead` (`license.read`) | Licensing / settings | `MIGRATE` | Usage is read-only data for the same dashboard license surface. |
| `core/controlplane/gateway/handlers_locks.go:35` | `GET /api/v1/locks` | admin, operator, viewer | `PermLocksRead` (`locks.read`) | Platform / distributed locks | `MIGRATE` | Read-only lock inspection should use a named permission instead of the legacy role triad. |
| `core/controlplane/gateway/handlers_locks.go:95` | `POST /api/v1/locks/acquire` | admin + lockStore | `PermAdminAll` (`admin.*`) | Platform / break-glass | `KEEP_ROLE_WITH_JUSTIFICATION` | Manual lock mutation is emergency-only operator tooling. |
| `core/controlplane/gateway/handlers_locks.go:129` | `POST /api/v1/locks/release` | admin + lockStore | `PermAdminAll` (`admin.*`) | Platform / break-glass | `KEEP_ROLE_WITH_JUSTIFICATION` | Same emergency-only manual lock surface as acquire. |
| `core/controlplane/gateway/handlers_locks.go:164` | `POST /api/v1/locks/renew` | admin + lockStore | `PermAdminAll` (`admin.*`) | Platform / break-glass | `KEEP_ROLE_WITH_JUSTIFICATION` | Same emergency-only manual lock surface as acquire/release. |

### MCP governance and policy shadow surfaces

| file:line | route | current guard | intended permission | ownership | recommended action | notes |
|---|---|---|---|---|---|---|
| `core/controlplane/gateway/handlers_mcp_outbound.go:75` | `GET /api/v1/mcp/outbound` | admin + redis client | `PermMCPRead` (`mcp.read`) | MCP governance | `MIGRATE` | Route is documented in the handler but not found in `gateway.go` registration. |
| `core/controlplane/gateway/handlers_mcp_prompts.go:23` | `GET /api/v1/mcp/prompts` | admin | `PermMCPRead` (`mcp.read`) | MCP governance | `MIGRATE` | Prompt catalogue is read-only MCP governance data; route is documented in the handler but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_mcp_tools.go:77` | `GET /api/v1/mcp/tools` | admin | `PermMCPRead` (`mcp.read`) | MCP governance | `MIGRATE` | MCP tool catalogue is a read-only governance surface; route is documented in the handler but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_mcp_tools.go:103` | `GET /api/v1/agents/{id}/tools` | admin | `PermMCPRead` (`mcp.read`) | MCP governance / agent identity | `MIGRATE` | Per-agent tool visibility is read-only governance data; route is documented in the handler but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_mcp_tools.go:158` | `GET /api/v1/agents/{id}/denied-events` | admin | `PermMCPRead` (`mcp.read`) | MCP governance / agent identity | `MIGRATE` | Denied-event timeline is read-only governance data; route is documented in the handler but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_mcp_usage.go:83` | `GET /api/v1/mcp/usage` | admin + redis client | `PermMCPRead` (`mcp.read`) | MCP governance | `MIGRATE` | Heatmap/usage aggregation is read-only MCP governance data; route is documented in the handler but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_mcp_verify.go:86` | `POST /api/v1/mcp/verify-signature` | admin | `PermMCPVerify` (`mcp.verify`) | MCP governance / trust | `MIGRATE` | Signature verification is a distinct least-privilege action; route is documented in the handler but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_policy_shadow.go:29` | `POST /api/v1/policy/bundles/{id}/shadow` | admin + configSvc | `PermPolicyWrite` (`policy.write`) | Policy shadowing | `MIGRATE` | Shadow activation is a policy write surface; route is documented in comments/tests but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_policy_shadow.go:95` | `GET /api/v1/policy/bundles/{id}/shadow` | admin + configSvc | `PermPolicyRead` (`policy.read`) | Policy shadowing | `MIGRATE` | Shadow inspection is read-only policy data; route is documented in comments/tests but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_policy_shadow.go:128` | `DELETE /api/v1/policy/bundles/{id}/shadow` | admin + configSvc | `PermPolicyWrite` (`policy.write`) | Policy shadowing | `MIGRATE` | Shadow deactivation is the mutating half of the shadow-policy surface; route is documented in comments/tests but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_shadow_results.go:258` | `GET /api/v1/policy/bundles/{id}/shadow/results/summary` | admin + redis client | `PermPolicyRead` (`policy.read`) | Policy shadowing | `MIGRATE` | Read-only shadow-eval summary; route is documented in comments/tests but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_shadow_results.go:403` | `GET /api/v1/policy/bundles/{id}/shadow/results/comparisons` | admin + redis client | `PermPolicyRead` (`policy.read`) | Policy shadowing | `MIGRATE` | Read-only shadow-eval comparison feed; route is documented in comments/tests but not found in `gateway.go`. |
| `core/controlplane/gateway/handlers_shadow_results.go:608` | `GET /api/v1/policy/bundles/{id}/shadow/results/timeseries` | admin + redis client | `PermPolicyRead` (`policy.read`) | Policy shadowing | `MIGRATE` | Read-only shadow-eval timeseries; route is documented in comments/tests but not found in `gateway.go`. |

### Packs, pools, stream, telemetry, topics, velocity, and worker surfaces

| file:line | route | current guard | intended permission | ownership | recommended action | notes |
|---|---|---|---|---|---|---|
| `core/controlplane/gateway/handlers_packs.go:46` | `GET /api/v1/packs` | admin + configSvc | `PermPacksRead` (`packs.read`) | Extensibility / packs | `MIGRATE` | Read-only pack inventory should be inspectable without install/uninstall power. |
| `core/controlplane/gateway/handlers_packs.go:64` | `GET /api/v1/packs/{id}` | admin + configSvc | `PermPacksRead` (`packs.read`) | Extensibility / packs | `MIGRATE` | Pack detail should align with the same read permission as pack listing. |
| `core/controlplane/gateway/handlers_packs.go:518` | `POST /api/v1/packs/{id}/verify` | admin + safetyClient | `PermPacksVerify` (`packs.verify`) | Extensibility / packs | `MIGRATE` | Verification is a distinct least-privilege action from install/uninstall. |
| `core/controlplane/gateway/handlers_packs.go:1144` | `GET /api/v1/marketplace/packs` | admin + configSvc | `PermPacksRead` (`packs.read`) | Extensibility / marketplace | `MIGRATE` | Marketplace catalog browsing should align with the same read permission as installed pack inventory. |
| `core/controlplane/gateway/handlers_packs.go:1162` | `POST /api/v1/marketplace/install` | admin + lockStore | `PermPacksInstall` (`packs.install`) | Extensibility / marketplace | `MIGRATE` | An install permission already exists; the raw admin role is the leftover. |
| `core/controlplane/gateway/handlers_pools.go:135` | `PUT /api/v1/pools/{name}` | admin | `PermPoolsWrite` (`pools.write`) | Runtime topology | `MIGRATE` | Pool creation belongs to the worker/pool topology domain, not to blanket admin. |
| `core/controlplane/gateway/handlers_pools.go:211` | `PATCH /api/v1/pools/{name}` | admin | `PermPoolsWrite` (`pools.write`) | Runtime topology | `MIGRATE` | Update is the same mutating pool-management surface as create/delete. |
| `core/controlplane/gateway/handlers_pools.go:283` | `DELETE /api/v1/pools/{name}` | admin | `PermPoolsWrite` (`pools.write`) | Runtime topology | `MIGRATE` | Delete is the same mutating pool-management surface as create/update. |
| `core/controlplane/gateway/handlers_pools.go:351` | `POST /api/v1/pools/{name}/drain` | admin | `PermPoolsWrite` (`pools.write`) | Runtime topology | `MIGRATE` | Drain is an operational pool mutation and should stay inside the pool-management permission set. |
| `core/controlplane/gateway/handlers_pools.go:426` | `PUT /api/v1/pools/{name}/topics/{topic}` | admin | `PermPoolsWrite` (`pools.write`) | Runtime topology | `MIGRATE` | Topic membership is a mutating pool-management action. |
| `core/controlplane/gateway/handlers_pools.go:482` | `DELETE /api/v1/pools/{name}/topics/{topic}` | admin | `PermPoolsWrite` (`pools.write`) | Runtime topology | `MIGRATE` | Topic removal is the same mutating pool-management surface as add. |
| `core/controlplane/gateway/handlers_stream.go:799` | `/api/v1/stream (any-method WebSocket/SSE entry)` | admin when auth is enabled | `PermAdminAll` (`admin.*`) | Platform / break-glass | `KEEP_ROLE_WITH_JUSTIFICATION` | This is a global event firehose across workflows/jobs/tenants. Keep it raw-admin until subscriptions become resource-scoped. |
| `core/controlplane/gateway/handlers_telemetry.go:13` | `GET /api/v1/telemetry/status` | admin | `PermTelemetryRead` (`telemetry.read`) | Telemetry | `MIGRATE` | The dashboard license page depends on the status route. |
| `core/controlplane/gateway/handlers_telemetry.go:30` | `GET /api/v1/telemetry/inspect` | admin | `PermTelemetryExport` (`telemetry.export`) | Telemetry | `MIGRATE` | Inspect exposes the raw payload shape and should be stronger than plain status/usage. |
| `core/controlplane/gateway/handlers_telemetry.go:47` | `GET /api/v1/telemetry/export` | admin | `PermTelemetryExport` (`telemetry.export`) | Telemetry | `MIGRATE` | Download/export belongs with the same telemetry-export permission as inspect. |
| `core/controlplane/gateway/handlers_telemetry.go:71` | `GET /api/v1/telemetry/usage` | admin | `PermTelemetryRead` (`telemetry.read`) | Telemetry | `MIGRATE` | Usage is read-only summary data. |
| `core/controlplane/gateway/handlers_telemetry.go:88` | `POST /api/v1/telemetry/consent` | admin | `PermTelemetryWrite` (`telemetry.write`) | Telemetry | `MIGRATE` | Consent changes mutate telemetry behavior and should use a write permission. |
| `core/controlplane/gateway/handlers_topics.go:45` | `GET /api/v1/topics` | admin, operator, viewer | `PermTopicsRead` (`topics.read`) | Runtime topology | `MIGRATE` | Topic discovery is a read-only routing/topology surface. |
| `core/controlplane/gateway/handlers_topics.go:86` | `POST /api/v1/topics` | admin | `PermTopicsWrite` (`topics.write`) | Runtime topology | `MIGRATE` | Topic creation is the mutating half of the topic-registry surface. |
| `core/controlplane/gateway/handlers_topics.go:173` | `DELETE /api/v1/topics/{name}` | admin | `PermTopicsWrite` (`topics.write`) | Runtime topology | `MIGRATE` | Delete is the same mutating topic-registry surface as create. |
| `core/controlplane/gateway/handlers_velocity.go:115` | `GET /api/v1/policy/velocity-rules` | admin + configSvc | `PermPolicyRead` (`policy.read`) | Policy governance | `MIGRATE` | Reuse `policy.read` and add a server-side `velocity_rules` feature gate so the backend matches the existing dashboard gate. |
| `core/controlplane/gateway/handlers_velocity.go:141` | `POST /api/v1/policy/velocity-rules` | admin + configSvc | `PermPolicyWrite` (`policy.write`) | Policy governance | `MIGRATE` | Reuse `policy.write` and add a server-side `velocity_rules` feature gate. |
| `core/controlplane/gateway/handlers_velocity.go:207` | `GET /api/v1/policy/velocity-rules/stats` | admin + configSvc | `PermPolicyRead` (`policy.read`) | Policy governance | `MIGRATE` | Stats are read-only and should align with the same policy read permission plus a `velocity_rules` feature gate. |
| `core/controlplane/gateway/handlers_velocity.go:252` | `PUT /api/v1/policy/velocity-rules/{id}` | admin + configSvc | `PermPolicyWrite` (`policy.write`) | Policy governance | `MIGRATE` | Reuse `policy.write` and add a server-side `velocity_rules` feature gate. |
| `core/controlplane/gateway/handlers_velocity.go:326` | `DELETE /api/v1/policy/velocity-rules/{id}` | admin + configSvc | `PermPolicyWrite` (`policy.write`) | Policy governance | `MIGRATE` | Reuse `policy.write` and add a server-side `velocity_rules` feature gate. |
| `core/controlplane/gateway/handlers_worker_credentials.go:44` | `GET /api/v1/workers/credentials` | admin | `PermWorkerCredentialsRead` (`workerCredentials.read`) | Worker access | `MIGRATE` | Listing worker credentials is a distinct read surface from creating or deleting them. |
| `core/controlplane/gateway/handlers_worker_credentials.go:67` | `POST /api/v1/workers/credentials` | admin | `PermWorkerCredentialsWrite` (`workerCredentials.write`) | Worker access | `MIGRATE` | Create should be governed by a worker-credential write permission instead of blanket admin. |
| `core/controlplane/gateway/handlers_worker_credentials.go:209` | `DELETE /api/v1/workers/credentials/{worker_id}` | admin | `PermWorkerCredentialsWrite` (`workerCredentials.write`) | Worker access | `MIGRATE` | Delete belongs with the same mutating worker-credential permission as create. |
| `core/controlplane/gateway/handlers_workers.go:28` | `POST /api/v1/workers/{id}/revoke-session` | admin | `PermWorkersWrite` (`workers.write`) | Worker trust / sessions | `MIGRATE` | Worker session revocation is a worker-trust mutation; route is documented in comments/tests but not found in `gateway.go`. |

## Break-glass / keep-role surfaces

These are the raw-admin surfaces I would intentionally **not** convert to ordinary granular permissions in the first sweep:

- `GET /api/v1/admin/locks` — emergency distributed-lock inspection across internal prefixes.
- `POST /api/v1/license/reload` — manual recovery / signed-license reload, and the obvious place to thread expiry-aware break-glass handling.
- `POST /api/v1/locks/{acquire,release,renew}` — direct lock mutation should stay emergency-only.
- `/api/v1/stream` — global event firehose; keep raw-admin until subscriptions are resource-scoped.
- Inline admin diagnostics inside `GET /api/v1/status` — not a route gate, only a redaction branch for sensitive support data.
- `GET /api/v1/config`'s `requireStoreAndRole(..., nil, ...)` preflight — keep because it is a store-availability check, not a legacy auth decision.

## Dashboard entitlement matrix (settings truth surface)

Legend: `gate` = explicit page/tab entitlement gate, `partial` = only part of the page is gated, `matrix view` = read-only truth surface, `MISMATCH` = current UI wiring hides or misstates the entitlement.

Audit-export column intentionally treats `auditExport` and legacy `siemExport` as the same dashboard feature family, because the current hub/page logic checks both.

| surface | SSO | SAML | SCIM | RBAC | audit export | legal hold | velocity rules | agent identity | notes |
|---|---|---|---|---|---|---|---|---|---|
| SettingsHubPage (`/settings`) | card | card (OR with sso) | card | card | card (`auditExport \| siemExport`) | `MISMATCH` | n/a | n/a | Hub has no legal-hold-only entry point. It also locks the Users card on `rbac` even though basic user CRUD works without advanced RBAC. |
| SettingsUsersPage (`/settings/users`) | n/a | n/a | n/a | partial | n/a | n/a | n/a | n/a | RBAC only gates custom-role editing and the Roles-tab upgrade path. Basic user CRUD and built-in role assignment remain available even when `rbac` is false. |
| SettingsSSOPage (`/settings/sso`) | gate | `MISMATCH` | n/a | n/a | n/a | n/a | n/a | n/a | The page hard-checks `sso`, but the Settings hub unlocks the card on `sso OR saml`. A `saml=true, sso=false` license would show an unlocked card that lands on a locked page. |
| SettingsSCIMPage (`/settings/scim`) | n/a | n/a | gate | n/a | n/a | n/a | n/a | n/a | Aligned: page gate matches the entitlement and the backend SCIM service also self-gates. |
| SettingsAuditExportPage (`/settings/audit-export`) | n/a | n/a | n/a | n/a | gate (`auditExport \| siemExport`) | tab gate | n/a | n/a | The page is internally truthful, but the hub card only reflects audit-export entitlements. `legalHold=true` without export entitlements requires direct navigation. |
| LicensePage (`/settings/license`) | matrix view | matrix view | matrix view | matrix view | matrix view | matrix view | matrix view | matrix view | This is the one place the dashboard shows all eight entitlements together, plus break-glass admin and SIEM-export specifics. |
| AgentsPage identity tab (`/agents` → identity) | n/a | n/a | n/a | n/a | n/a | n/a | n/a | gate | Outside `/settings`, but this is the canonical dashboard owner for the agent-identity entitlement and already matches the backend feature gate. |
| VelocityRulesPage (`/govern/velocity-rules`) | n/a | n/a | n/a | n/a | n/a | n/a | gate | n/a | Outside `/settings`, but this is the canonical dashboard owner for velocity rules. Frontend gate exists today; backend handlers still need a matching server-side entitlement check. |

## Dashboard mismatches to fix after the RBAC sweep

1. **Settings hub vs Users page** — the hub locks `/settings/users` on `rbac`, but the page still supports user CRUD and built-in role assignment without advanced RBAC. Either loosen the hub card or move the non-RBAC user-management surface elsewhere.
2. **Settings hub vs SSO page** — the hub card unlocks on `sso OR saml`, but `SettingsSSOPage` itself only checks `sso`. Pick one entitlement model and make both surfaces match.
3. **Audit Export card vs Legal Hold tab** — `SettingsAuditExportPage` truthfully exposes both audit export and legal hold, but the Settings hub only reflects export entitlements. A `legalHold`-only license currently has no hub entry point.
4. **Velocity rules frontend vs backend** — the dashboard already gates `VelocityRulesPage` on `velocityRules`, but `handlers_velocity.go` has no server-side `requireFeatureEntitlement("velocity_rules", ...)` yet. The backend must enforce the same entitlement, not just RBAC permissions.
5. **License page dependencies** — `/settings/license` is the entitlement truth surface, but its backing read routes (`/api/v1/license`, `/api/v1/license/usage`, `/api/v1/telemetry/status`) are still raw-admin. The UI and backend role model will stay inconsistent until those migrate to named read permissions.

## Explicit follow-ups this audit implies

- Add the proposed permission constants to `core/controlplane/gateway/auth/rbac.go`, update `AllPermissions`, and seed them into the built-in roles deliberately (do **not** default every new permission to viewer access).
- Convert each `MIGRATE` row to `requirePermissionOrRole(...)` or `requireStoreAndPermissionOrRole(...)` as appropriate, preserving nil-store checks, tenant enforcement, and any existing feature-entitlement gates.
- Leave the `KEEP_ROLE_WITH_JUSTIFICATION` rows raw-admin for now, but thread the expiry/break-glass logic into those paths where it matters most (`license/reload`, auth/session recovery, admin diagnostics).
- Keep the dashboard matrix in sync while changing backend auth so the Settings hub does not advertise or hide the wrong surface.
