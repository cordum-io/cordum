# Cordum Dashboard Agent Notes

## Dependency Hygiene

The dashboard dependency standard is **pnpm**. `pnpm-lock.yaml` is the only
committed dashboard lockfile; do not reintroduce `package-lock.json` or `npm ci`
for dashboard installs.

`dashboard/package.json` has `dependencies` + `devDependencies` + `overrides`
+ `pnpm.overrides` blocks. Override semantics is a common source of drift
bugs, especially when a package manager silently accepts stale metadata. Two
rules:

**Rule 1 — bump direct dep AND override together.** If a dep listed in
`dependencies` (or `devDependencies`) has a matching entry in `overrides` /
`pnpm.overrides`, bumping one without the other can produce
non-intersecting semver ranges. Keep direct dependency ranges and override
ranges aligned.
Example of the failure mode:

```jsonc
"dependencies":  { "lodash":  "^4.17.21" }   // >=4.17.21, <4.18.0
"overrides":     { "lodash":  "^4.18.0"  }   // >=4.18.0,  <4.19.0  ← no overlap
```

**Rule 2 — regenerate the lockfile after any `package.json` edit.** Edits
that don't touch `pnpm-lock.yaml` will fail CI's frozen-lockfile gate. After
any `package.json` edit, run:

```bash
cd dashboard
pnpm install --lockfile-only
git add package.json pnpm-lock.yaml
```

CI enforces both rules via `tools/scripts/check_dashboard_deps.sh`
(EDGE-074), which runs in the `dashboard-test` job before
`pnpm install --frozen-lockfile` and fails the PR on pnpm dependency errors or
lockfile drift.

## Generated API hooks

`src/api/generated/` is produced by [orval](https://orval.dev/) from
`cordum/docs/api/openapi/cordum-api.yaml`. Generated React Query hooks call
the `apiClient` mutator exported from `src/api/client.ts`, so auth headers,
tenant routing, the 30s request timeout, structured logging, and 401-redirect
behavior remain centralized in the existing http layer.

Regenerate after any spec edit:

```bash
cd dashboard
pnpm run generate-api
git add src/api/generated/
```

Rules:

- **Do not hand-edit `src/api/generated/`.** orval runs with `clean: true` and
  will overwrite local edits on the next regen.
- The orval config lives at `dashboard/orval.config.ts`. The mutator override
  points at `./src/api/client.ts` (`apiClient`); changing the mutator path
  requires also updating that export.
- CI runs `pnpm run check-api-codegen` in the `dashboard-test` job (after
  `pnpm install`, before `tsc --noEmit`). Drift between the committed tree and
  what the spec would regenerate fails the PR.
- The check refuses to run with uncommitted changes in `src/api/generated/` —
  commit or revert first, since regen would otherwise silently wipe the edits.

## Logging

Production paths in `dashboard/src/` must use the structured logger at
`src/lib/logger.ts` rather than `console.*` directly. The logger emits
structured entries (`component`, `msg`, `fields`) with level filtering
(`VITE_LOG_LEVEL`) and category filtering (`VITE_DEBUG_CATEGORIES`);
plain `console.log/warn/error/debug/info` bypasses both. The `no-console`
ESLint rule (in `eslint.config.mjs`, added by task-1acf9c07 Pass C)
enforces this on all files matching `src/**/*.{ts,tsx}` except:

- `src/test-utils/**`
- `src/**/*.test.{ts,tsx}`
- `src/**/__tests__/**`
- `src/**/*.stories.{ts,tsx}`

Those paths can call `console.*` directly without restriction.

```ts
import { logger } from "@/lib/logger";

logger.warn("transform", "unknown governance verdict, defaulting to deny", { raw });
//          ^component   ^short message                                    ^optional fields
```

`src/lib/logger.ts` itself is the write-out primitive — its three
`console[fn](...)` call sites carry `// eslint-disable-next-line no-console`
comments documenting the carve-out. Do NOT add similar disable comments
elsewhere unless the use case is genuinely below the logger (e.g. a
critical-error-only fallback when the logger module itself fails to
load); document the rationale on the same line.

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

## Accessibility (Phase 5a)

`renderWithProviders` supports an opt-in `runAxe: true` option that asserts
**zero** WCAG 2 A/AA violations (any impact) on the rendered container. The
opt-in returns a `Promise<RenderWithProvidersResult>` (the helper drives
`axe-core` asynchronously); the call must be `await`ed:

```tsx
const { container } = await renderWithProviders(<MyComponent />, {
  runAxe: true,
});
```

`axeMode: "dark"` is also accepted to test the dark theme. The strict gate
runs `axe.run` directly with `runOnly: { type: "tag", values: ["wcag2a",
"wcag2aa"] }` and disables ONLY the `color-contrast` rule (jsdom doesn't
composite `backdrop-filter`, so color-contrast fires false-negatives on
glass-panel surfaces — Lighthouse CI / Phase 5b is the canonical
color-contrast gate). Any moderate/minor/serious/critical violation
throws a descriptive Error with target selectors + failure summaries.

The pre-existing helper `assertNoSeriousAxeViolations` (filters to
critical/serious only) remains in `src/test-utils/a11y.ts` for the
dedicated `*.a11y.test.tsx` files that intentionally use the looser
gate (HomePage / SettingsHubPage / PolicyOverviewPage). New tests
should prefer `runAxe: true` for the strict gate.

When to use:

- Component tests for shared primitives (`Button`, `Card`, `EmptyState`,
  `Drawer`, etc.) where the canonical render is synchronous.
- New tests for surfaces customers will see, when no `waitFor` preamble is
  required.

When NOT to use:

- Tests that intentionally render an inaccessible state for negative-test
  purposes — leave them synchronous (default `runAxe: false`) so axe doesn't
  run on the deliberate violation.
- Page tests whose first paint depends on async data — keep using a separate
  `*.a11y.test.tsx` file that calls `assertNoSeriousAxeViolations(container,
  { mode })` after `await waitFor(...)`. The `runAxe` opt-in is a sugar layer
  over the same helper, suited for tests that don't need a `waitFor` preamble.

### Strict a11y CI gate

`pnpm run lint:a11y` runs ESLint with a narrow flat config
(`eslint.a11y.config.mjs`) that escalates the gate-relevant jsx-a11y rules
(alt-text, ARIA correctness, heading-has-content, anchor-has-content,
iframe-has-title) to `error`. The default `pnpm run lint` keeps lower-impact
rules at `warn` so existing surfaces don't block unrelated PRs; the strict
gate is the one CI should fail on. The narrow config ignores
`src/api/generated/**` (orval-emitted, hand-edits forbidden).
## Lighthouse CI (Phase 5b)

CI runs Lighthouse against `http://127.0.0.1:4173/login` on every PR
via the `lhci-login` job in `.github/workflows/ci.yml`. Scores are
posted as a PR comment; assertions are warn-only (perf ≥ 0.7, a11y ≥
0.9, best-practices ≥ 0.85, SEO off) so the gate surfaces regressions
without blocking merges.

Run locally from `dashboard/`:

```bash
pnpm run lhci
```

This boots `vite preview` on port 4173 via `start-server-and-test`,
runs `lhci autorun` (3 runs averaged, desktop preset), and tears the
preview server down cleanly. `pnpm exec lhci healthcheck` validates the
config + Chrome installation without running a full audit. Local runs on
Windows hosts can hit chrome-launcher tmp-cleanup races (`EPERM`) that
do not occur on the Linux CI runner — use `healthcheck` for local
validation if the autorun fails.

Configuration lives in `dashboard/lighthouserc.json`. Authenticated
surfaces (`/`, `/jobs`, `/audit`, `/policies`, `/agents`) are
deferred to follow-up task **task-63603c2e** (cookie-bridge + test
credentials required); current /login-only gate catches login regressions
but does not measure the customer-value surfaces.

## Bundle size (Phase 5d)

`pnpm run build` emits `dist/stats.html` (a treemap visualizer from
`rollup-plugin-visualizer`) on every build. CI parses `dist/assets/*.js`
via `scripts/parse-bundle-stats.mjs` and posts a per-chunk size table as
a PR comment (`peter-evans/create-or-update-comment@v4`, body-tag
`<!-- bundle-size-report -->` so subsequent pushes update the same
comment instead of appending).

**Soft thresholds** (warn-only; parser exits 0 even on breach):

- Initial chunk (`index-*.js`): ≤ 400 KB raw / 120 KB gzip
- Total (all `.js` in `dist/assets/`): ≤ 3100 KB raw / 950 KB gzip

Threshold values live in `scripts/parse-bundle-stats.mjs`. Set
~25-30% above the 2026-05-09 baseline (initial 305 KB / 92 KB gzip;
total 2533 KB / 759 KB gzip) so PRs have headroom without losing
regression signal. See
[`docs/code-hygiene-sweep.md`](./docs/code-hygiene-sweep.md#bundle-size-baseline-phase-5d-task-50bbfd7d-2026-05-09)
for the full baseline table and tightening guidance.

**Reading `dist/stats.html`** locally:

```bash
pnpm run build
# Then open dist/stats.html in a browser. The treemap shows source-file
# contribution to each chunk; click a chunk to drill in.
```

Stats.html is also uploaded by the `dashboard-test` CI job as the
`dashboard-bundle-stats` artifact (14-day retention) so reviewers can
download the rich visualizer when the PR-comment summary isn't enough.

## Error boundaries (Phase 5e)

Render errors inside a Route are caught by a per-route `RouteBoundary`
defined in `src/components/RouteBoundary.tsx`. It pairs the primitive
`ErrorBoundary` (`src/components/ErrorBoundary.tsx`) with
`RouteErrorFallback` (`src/components/RouteErrorFallback.tsx`) and uses
`useLocation().pathname` as the boundary's `resetKey` so the boundary
auto-clears when the user navigates away.

Wiring pattern in `App.tsx`:

```tsx
<Route
  path="/jobs"
  element={
    <RouteBoundary name="Jobs">
      <JobsPage />
    </RouteBoundary>
  }
/>
```

The `name` prop is the human-readable route label that surfaces in the
"Couldn't load X" header + bug-report mailto subject. Use a phrase a
non-engineer would recognize ("Approvals", "Bundle details") rather
than the URL slug.

**When to use a per-route boundary** — every leaf-page route gets one.
Pure-redirect routes (components that return `<Navigate>`) are NOT
wrapped because they don't render UI and never throw a render error.

**When to delegate to the outer ErrorBoundaryWrapper instead** — a
render error in the AppShell layout (sidebar, header, command palette
mount) lands in the outer `ErrorBoundaryWrapper`, which is the OUTERMOST
component returned from `ProtectedRoutes` and wraps `<AppShell>`
itself. That fallback is the default generic "Something went wrong"
full-page card. Don't move shell-render-time failures into a per-route
boundary; they aren't scoped to one route.

The `/login` route — rendered by the App-level `<Routes>` outside
`ProtectedRoutes` — gets its own `RouteBoundary` so a render error
during login still surfaces a route-scoped fallback (with Retry +
Report-bug). It is the only route NOT covered by `ErrorBoundaryWrapper`
because it lives outside the protected shell.

**Fallback props** — the optional `fallback` render prop on
`ErrorBoundary` receives `{ error: Error, reset: () => void }`. Calling
`reset` clears `hasError` so the boundary re-renders its children
(the typical user flow on Retry). Reset BEFORE updating the underlying
state will retrigger the throw — fix the root cause first, then reset.

**Logging** — `ErrorBoundary.componentDidCatch` writes a structured
`logger.error("error-boundary", ...)` entry with the message + stack +
componentStack. Don't add additional `console.error` next to a
boundary; the logger already captures it.
