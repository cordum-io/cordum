# GOVERN Parity Audit Checklist

Status: COMPLETE

## Findings

| # | File | Issue | Severity | Status |
|---|------|-------|----------|--------|
| 1 | QuarantinePage.tsx:99 | Legacy CTA navigates to `/quarantine` (circular redirect) + "legacy" label | CRITICAL | RESOLVED |
| 2 | SimulatorPage.tsx:34 | Subtitle references "legacy simulator" — transitional framing | CRITICAL | RESOLVED |
| 3 | QuarantinePage.tsx:41 | Subtitle says "shell" — transitional framing | MODERATE | RESOLVED |
| 4 | App.tsx | Missing `/govern/input-rules/:id` detail route | LOW | DEFERRED |
| 5 | App.tsx | Missing `/govern/output-rules/:id` detail route | LOW | DEFERRED |
| 6 | usePolicyAccess.ts | RBAC matrix verified correct — no changes needed | INFO | RESOLVED |

## Detail Route Deferral Rationale (Findings 4-5)

Input Rules and Output Rules use inline drawer editors (InputRuleEditorDrawer, OutputRuleEditorDrawer)
for rule editing. Individual rules are not standalone resources with their own detail pages — they are
entries within a policy bundle. The `/govern/tenants/:id` and `/govern/bundles/:id` detail routes exist
because tenants and bundles are standalone resources with complex metadata, YAML editors, and lifecycle
management. Rules are edited in-place via drawers from the list page. No detail routes needed.

## RBAC Matrix (Finding 6)

| Role | canEdit | canPublish | canRelease | isReadOnly |
|------|---------|------------|------------|------------|
| viewer | - | - | - | yes |
| auditor | - | - | - | yes |
| editor | yes | - | - | - |
| publisher | - | yes | - | - |
| operator | yes | yes | yes | - |
| secops | yes | yes | yes | - |
| admin | yes | yes | yes | - |
| owner | yes | yes | yes | - |
| release_manager | - | - | yes | - |

canManageOutputRules = canEdit (intentional — output rules share edit scope).
canManageTenants = canEdit (intentional — tenant overrides share edit scope).

## Validation Results

- **Build**: Production build passes (5.74s)
- **Tests**: 156/156 pass across 36 test files (3.31s)
- **TypeScript**: Clean (no errors)
- **GOVERN test files**: 13 files, 52 tests — all pass
- **No legacy /policies references**: Confirmed via grep — zero active references in GOVERN pages
