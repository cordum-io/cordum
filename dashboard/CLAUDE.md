# Cordum Dashboard Agent Notes

## Dependency Hygiene

`dashboard/package.json` has `dependencies` + `devDependencies` + `overrides`
+ `pnpm.overrides` blocks. The override semantics is the most common source
of drift bugs — and the silent kind that only fires when a docker layer
cache misses. Two rules:

**Rule 1 — bump direct dep AND override together.** If a dep listed in
`dependencies` (or `devDependencies`) has a matching entry in `overrides` /
`pnpm.overrides`, bumping one without the other can produce
non-intersecting semver ranges and `npm install` fails with `EOVERRIDE`.
Example of the failure mode:

```jsonc
"dependencies":  { "lodash":  "^4.17.21" }   // >=4.17.21, <4.18.0
"overrides":     { "lodash":  "^4.18.0"  }   // >=4.18.0,  <4.19.0  ← no overlap
```

**Rule 2 — regenerate the lockfile after any `package.json` edit.** Edits
that don't touch `package-lock.json` go silently green when docker caches
the prior `npm ci` layer, then explode at build time inside the
container. After any `package.json` edit, run:

```bash
cd dashboard
npm install --package-lock-only --legacy-peer-deps
git add package.json package-lock.json
```

CI enforces both rules via `tools/scripts/check_dashboard_deps.sh`
(EDGE-074), which runs in the `dashboard-test` job before `npm ci` and
fails the PR on EOVERRIDE / ERESOLVE / EUSAGE / lockfile drift.

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

- Use `renderWithProviders` from `src/test-utils/render` as the sanctioned
  entry point for page tests.
- Any new hook added to a page must have a default MSW handler in
  `src/test-utils/handlers.ts` so the page's empty-state render works without
  per-file setup.
- Do not add page-level `vi.mock("@/hooks/...")` for data. Use `server.use(...)`
  to override network responses for the test case.
- MSW is opt-in through `renderWithProviders`; legacy tests with direct
  `globalThis.fetch` spies keep their existing isolation.
- See `docs/adr/0001-page-test-providers.md` for the decision record and
  rejected alternatives.
