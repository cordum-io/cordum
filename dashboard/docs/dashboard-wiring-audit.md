# Dashboard wiring audit
_Updated: 2026-04-20 — second pass over production dashboard routes, including a page-level API route audit._

## Goal
Make the dashboard behaviorally truthful:
- if a surface is backed by a real hook / API / route, keep it
- if a surface is not truly wired yet, either wire it now or remove it
- do **not** leave polished placeholders that look production-ready

## First-pass audit method
- inventoried the routed surface from `src/App.tsx`
- checked route pages and key child panels for:
  - explicit disabled actions with “coming soon” / “not available” copy
  - tabs that open placeholder-only panels
  - dead CTAs that have no backend endpoint or no navigation target
  - pages that are read-only in practice but still advertise create/edit/manage actions
- cross-checked every page-level literal API path in `src/pages/**` against gateway route registration under `core/controlplane/gateway`
  - result after cleanup: **no page-level dashboard endpoint references remain without a matching gateway route**

## Decisions made in this pass

### Removed / hidden because they were misleading
- **`pages/SettingsMcpPage.tsx`**
  - removed the **Analytics** tab because it only opened a placeholder empty state
- **`components/settings/McpServerPanel.tsx`**
  - removed the dead **Refresh** button; it had no real backing action
- **`components/agents/WorkerDetailDrawer.tsx`**
  - removed dead **Drain** / **Restart** worker actions; no backend endpoints exist
- **`pages/SettingsEnvironmentsPage.tsx`**
  - removed dead **Add Environment** and per-card **Configure** actions
  - removed the empty-state “Coming soon” CTA
  - removed fake region/version/workers fields that were never backed by the real environment model
- **`pages/settings/LicensePage.tsx`**
  - removed the dead local **Select file** staging control
  - replaced it with a truthful operator handoff section that explains the real update path (`CORDUM_LICENSE_FILE` / `CORDUM_LICENSE_TOKEN`)
- **`pages/SchemaDetailPage.tsx`**
  - removed dead **Edit Schema** action
- **`pages/govern/BundlesPage.tsx`**
  - removed dead **Create Bundle** action
- **`pages/govern/TenantsPage.tsx`**
  - removed dead **Create Tenant** action

### Wired because a real config-backed path already existed
- **`pages/SettingsNotificationsPage.tsx`**
  - removed the dead `/notifications/channels` and `/notifications/preferences` page-level calls
  - rewired the page to the real config-backed hooks (`useNotificationChannels`, `useNotificationRules`, `useSaveNotificationRules`, `useDeleteNotificationChannel`)
  - restored actual dashboard behavior: add/edit/remove channels and add/edit/remove routing rules
- **`pages/SettingsEnvironmentsPage.tsx`**
  - removed the dead `/environments` page-level call
  - rewired the page to `useEnvironments()` so the inventory reflects saved system config
  - made the page truthful about being inventory-only and showed only fields that exist in the real environment model

### Kept because they are backed by real data/behavior
- route inventory and detail pages that already read from live hooks or route into real detail surfaces:
  - `HomePage`
  - `JobsPage` / `JobDetailPage`
  - `AgentsPage` / `AgentDetailPage` / `AgentIdentityDetailPage`
  - `ApprovalsPage` / `approvals/ApprovalDetailPage`
  - `WorkflowsPage` / `WorkflowStudioPage` / `RunDetailPage`
  - `PacksPage` / `PackDetailPage`
  - `SchemasPage`
  - `TopicsPage`
  - `AuditLogPage`
  - `DLQPage`
  - `SettingsHealthPage`
  - `SettingsKeysPage`
  - `SettingsUsersPage`
  - `SettingsConfigPage`
  - `SettingsMcpPage` (server-only view)
  - `SettingsLicensePage`
  - `SettingsSSOPage`
  - `SettingsSCIMPage`
  - `SettingsAuditExportPage`
  - govern overview / tenants / bundles / velocity / quarantine / replay / analytics detail surfaces that already have real bundle or query-backed data paths

## Follow-up candidates after this pass
These pages still need a deeper behavioral audit beyond the obvious dead CTAs removed here:
- `JobDetailPage.tsx`
- `RunDetailPage.tsx`
  - `pages/settings/SettingsSSOPage.tsx`
  - `pages/settings/SettingsSCIMPage.tsx`
  - `pages/govern/PolicyOverviewPage.tsx`
  - `pages/govern/PolicyAnalyticsPage.tsx`
  - `pages/govern/ReplayPage.tsx`

Reason: these surfaces are live enough to keep, but they are complex enough that we should separately verify every subpanel/action for backend truthfulness instead of relying only on the dead-CTA scan.

## Route-by-route classification snapshot

### Operate
- `/` → `HomePage` — **live**
- `/agents` → `AgentsPage` — **live**
- `/agents/:id` → `AgentDetailPage` — **live**
- `/agents/identity/:id` → `AgentIdentityDetailPage` — **live**
- `/jobs` → `JobsPage` — **live**
- `/jobs/:id` → `JobDetailPage` — **live**

### Orchestrate
- `/workflows` → `WorkflowsPage` — **live**
- `/workflows/studio/new` → `WorkflowStudioPage` — **live**
- `/workflows/:id/studio` → `WorkflowStudioPage` — **live**
- `/workflows/:id/runs/:runId` → `RunDetailPage` — **live**
- `/workflows/new`, `/workflows/:id/edit`, `/workflows/:id` — **redirects only**

### Govern
- `/govern/overview` → `PolicyOverviewPage` — **live**
- `/govern/velocity-rules` → `VelocityRulesPage` — **live**
- `/govern/tenants` → `TenantsPage` — **live; dead create CTA removed**
- `/govern/tenants/:id` → `TenantDetailPage` — **live**
- `/govern/bundles/:id` → `BundleDetailPage` — **live**
- `/govern/quarantine` → `QuarantinePage` — **live**
- `/govern/replay` → `ReplayPage` — **live**
- `/govern/analytics` → `PolicyAnalyticsPage` — **live**
- `/govern/input-rules`, `/govern/output-rules`, `/govern/bundles`, `/govern/simulator` — **redirects into the live govern overview surface**

### Extend / Observe
- `/packs` → `PacksPage` — **live**
- `/packs/:id` → `PackDetailPage` — **live**
- `/topics` → `TopicsPage` — **live**
- `/schemas` → `SchemasPage` — **live**
- `/schemas/:id` → `SchemaDetailPage` — **live; dead edit CTA removed**
- `/audit` → `AuditLogPage` — **live**
- `/dlq` → `DLQPage` — **live**

### Settings
- `/settings` → `SettingsHubPage` — **live**
- `/settings/health` → `SettingsHealthPage` — **live**
- `/settings/keys` → `SettingsKeysPage` — **live**
- `/settings/users` → `SettingsUsersPage` — **live**
- `/settings/notifications` → `SettingsNotificationsPage` — **rewired to live config-backed hooks**
- `/settings/environments` → `SettingsEnvironmentsPage` — **rewired to live config-backed hooks**
- `/settings/config` → `SettingsConfigPage` — **live**
- `/settings/mcp` → `SettingsMcpPage` — **live; placeholder analytics tab removed**
- `/settings/sso` → `SettingsSSOPage` — **live**
- `/settings/scim` → `SettingsSCIMPage` — **live**
- `/settings/audit-export` → `SettingsAuditExportPage` — **live**
- `/settings/license` → `LicensePage` — **live; dead local file-picker removed**

### Utility
- `/login` → `LoginPage` — **live**
- `*` → `NotFoundPage` — **live**
- legacy `/policies/*`, `/quarantine`, `/pools`, `/system`, `/security`, `/traces`, `/safety/*` paths — **redirects only**
