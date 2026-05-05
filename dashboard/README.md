# Cordum Dashboard

Frontend control-plane UI for Cordum operators.

## Local development

The dashboard uses **pnpm**. `pnpm-lock.yaml` is the committed lockfile used by
local development, CI, and the dashboard Docker image.

```bash
pnpm install
pnpm run dev
```

## Testing

Page-level tests that render a page composing React Query hooks must use the
shared provider helper:

```tsx
import { renderWithProviders } from "@/test-utils/render";
import { http, HttpResponse, server } from "@/test-utils/msw";

server.use(http.get("*/api/v1/example", () => HttpResponse.json({ items: [] })));
const { container } = renderWithProviders(<ExamplePage />, {
  initialEntries: ["/example"],
});
```

Rules:

- Use `renderWithProviders` from `src/test-utils/render` as the one sanctioned
  entry point for page tests.
- Any new hook added to a page must have a default MSW handler in
  `src/test-utils/handlers.ts` so the page's empty-state render works without
  per-file setup.
- Do not add page-level `vi.mock("@/hooks/...")` for data. Use `server.use(...)`
  to override network responses for the test case.
- See `docs/adr/0001-page-test-providers.md` for the decision record and
  rejected alternatives.

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
