# Enterprise entitlement matrix

Truth-source checklist for the remaining enterprise-gated dashboard and gateway surfaces. This complements `docs/security/rbac-route-audit-20260420.md` by pinning the exact **license projection key**, **representative backend route**, **dashboard owner**, and **test coverage** for each enterprise feature family.

## Scope

- `/api/v1/license` is the canonical backend projection consumed by the dashboard.
- Dashboard surfaces must never advertise a feature as available when the gateway still returns `tier_limit_exceeded`.
- SSO/SAML/SCIM have dedicated backend entitlement tests in `core/controlplane/gateway/auth/*_test.go`; the aggregate gateway matrix test below focuses on license projection plus representative gateway-owned feature gates.

## Matrix

| Entitlement | `/api/v1/license` key | Representative backend route(s) | Dashboard owner | Disabled behavior | Coverage |
|---|---|---|---|---|---|
| SSO | `sso` | `GET /api/v1/auth/sso/oidc/login` (see auth adapter tests) | `dashboard/src/pages/settings/SettingsSSOPage.tsx` | Entire SSO page is locked; no provider handoff surfaces render. | `core/controlplane/gateway/auth/oidc_flow_test.go`, `dashboard/src/pages/settings/enterprise-matrix.test.tsx` |
| SAML add-on | `saml` | `GET /api/v1/auth/sso/saml/{metadata,login}` / `POST /acs` (see auth adapter tests) | `SettingsSSOPage` + `SamlConfigPanel` | OIDC remains visible if `sso=true`, but SAML metadata/testing surfaces stay explicitly locked. | `core/controlplane/gateway/auth/saml_test.go`, `dashboard/src/pages/settings/enterprise-matrix.test.tsx` |
| SCIM | `scim` | `/api/v1/scim/*` (see auth adapter tests) | `dashboard/src/pages/settings/SettingsSCIMPage.tsx` | Full-page upgrade prompt; provisioning endpoint/token UI stays hidden. | `core/controlplane/gateway/auth/scim_test.go`, `dashboard/src/pages/settings/enterprise-matrix.test.tsx` |
| Advanced RBAC | `rbac` | `PUT /api/v1/auth/roles/{name}` | `dashboard/src/pages/SettingsHubPage.tsx`, `dashboard/src/pages/SettingsUsersPage.tsx` | Settings hub still links to user management, but custom-role editing remains gated inside the Roles view. | `core/controlplane/gateway/enterprise_matrix_test.go`, `core/controlplane/gateway/rbac_route_enforcement_test.go` |
| Audit export | `audit_export` and legacy `siem_export` | `GET /api/v1/audit/export` | `dashboard/src/pages/settings/SettingsAuditExportPage.tsx` | Export tab shows an upgrade prompt; test/export actions stay unavailable. | `core/controlplane/gateway/enterprise_matrix_test.go`, `core/controlplane/gateway/handlers_audit_compliance_test.go`, `dashboard/src/pages/settings/SettingsAuditExportPage.test.tsx` |
| Legal hold | `legal_hold` | `POST /api/v1/audit/legal-hold` | `SettingsHubPage` + `SettingsAuditExportPage` legal-hold tab | Audit card still appears when legal hold is the only entitlement; legal-hold tab alone stays gated if the entitlement is absent. | `core/controlplane/gateway/enterprise_matrix_test.go`, `dashboard/src/pages/settings/enterprise-matrix.test.tsx`, `dashboard/src/pages/settings/SettingsAuditExportPage.test.tsx` |
| Velocity rules | `velocity_rules` | `/api/v1/policy/velocity-rules` | `dashboard/src/pages/govern/VelocityRulesPage.tsx` | Entitlement overlay appears and the top-level “New rule” CTA is disabled so the page does not advertise an unavailable mutation. | `core/controlplane/gateway/enterprise_matrix_test.go`, `core/controlplane/gateway/handlers_velocity_test.go`, `dashboard/src/pages/settings/enterprise-matrix.test.tsx` |
| Break-glass admin | `break_glass_admin` | projected only; runtime enforcement lives in break-glass middleware | `dashboard/src/pages/settings/LicensePage.tsx` | Matrix-only visibility today; no separate settings route. | `core/controlplane/gateway/enterprise_matrix_test.go`, `dashboard/src/pages/settings/LicensePage.test.tsx` |
| Agent identity | `agent_identity` | `/api/v1/agents` | `dashboard/src/pages/AgentsPage.tsx` identity directory | Agent identity registry remains locked and gateway routes return `tier_limit_exceeded`. | `core/controlplane/gateway/enterprise_matrix_test.go`, `core/controlplane/gateway/handlers_agents_test.go`, `dashboard/src/pages/AgentIdentityTab.test.tsx` |

## Current truthfulness rules

1. **Settings hub mirrors the real entry points**
   - `Users & RBAC` is no longer hidden behind `rbac`; basic user management is available on lower tiers.
   - `Audit Export` unlocks when **either** export entitlement or `legal_hold` is active, so legal-hold-only licenses keep a visible entry point.
   - `SSO & SAML` follows the base `sso` entitlement. The SAML add-on is enforced inside the page, not at the hub-card level.

2. **Settings SSO is split correctly**
   - `sso=false` locks the full page.
   - `sso=true, saml=false` keeps OIDC operator handoff visible while SAML metadata, login testing, and dashboard controls remain explicitly locked.

3. **Velocity rules never expose an enabled create action when unlicensed**
   - The entitlement overlay remains the primary gate.
   - The header-level “New rule” CTA is also disabled so the page headline cannot suggest a writable surface that the gateway will reject.

## Verification commands

### Backend

```bash
go test ./core/licensing/... -run 'TestDefaultEntitlementsByTier' -count=1
go test ./core/controlplane/gateway -run 'TestEnterpriseEntitlementMatrix|TestLicenseEndpointProjectsAgentIdentityEntitlement' -count=1
```

### Dashboard

```bash
cd dashboard
npm test -- --run src/pages/settings/enterprise-matrix.test.tsx src/pages/settings/SettingsSSOPage.test.tsx src/pages/settings/SettingsSCIMPage.test.tsx src/pages/settings/SettingsAuditExportPage.test.tsx
```
