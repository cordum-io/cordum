# Cordum Dashboard

Frontend control-plane UI for Cordum operators.

## Local development

```bash
npm install
npm run dev
```

## Navigation — Govern section

Policy management lives under the **Govern** section (`/govern/*`). Each page has a single responsibility:

| Route | Page | Purpose |
|-------|------|---------|
| `/govern/overview` | Policy Overview | Cross-bundle policy posture summary, rule browser, scope filtering |
| `/govern/input-rules` | Input Rules | Ordered input policy rules with first-match-wins semantics |
| `/govern/output-rules` | Output Rules | Output scanners, detectors, content pattern rules |
| `/govern/tenants` | Tenants | Multi-tenant policy hierarchy and per-tenant overrides |
| `/govern/tenants/:id` | Tenant Detail | Individual tenant policy configuration |
| `/govern/bundles` | Bundles | Policy bundle inventory, status, and metadata |
| `/govern/bundles/:id` | Bundle Detail | YAML editor, visual preview, diff, snapshot history |
| `/govern/simulator` | Simulator | Dry-run policy evaluation against test payloads |
| `/govern/quarantine` | Quarantine | Output quarantine queue for review and release |

### Legacy route migration

All old `/policies/*` URLs are automatically redirected to their `/govern/*` equivalents. Bookmarks and external links will continue to work.

### Key behaviors

- Input rules are **ordered** and evaluated with **first-match-wins** semantics.
- `default_decision` applies only when no input rule matches (recommended: `deny`).
- YAML parse errors are displayed inline and block save until fixed.
- Bundle save writes to `/api/v1/policy/bundles/{id}`.
- Simulate uses `/api/v1/policy/bundles/{id}/simulate`.
- RBAC: viewers see read-only UI; editors can save drafts; publishers can publish and rollback.
