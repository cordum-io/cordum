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

`renderWithProviders` supports an opt-in `runAxe: true` option that asserts no
critical or serious WCAG 2 AA violations on the rendered container. The opt-in
returns a `Promise<RenderWithProvidersResult>` (the helper drives `axe-core`
asynchronously); the call must be `await`ed:

```tsx
const { container } = await renderWithProviders(<MyComponent />, {
  runAxe: true,
});
```

`axeMode: "dark"` is also accepted to test the dark theme. The opt-in delegates
to `assertNoSeriousAxeViolations` from `src/test-utils/a11y.ts`, so the gate
semantics match the existing dedicated `*.a11y.test.tsx` files: `wcag2a` +
`wcag2aa` tags, filtered to critical/serious impact only. jsdom does not
composite `backdrop-filter`, so axe's `color-contrast` rule fires false-
negatives on glass-panel surfaces; the impact filter absorbs those, and
structural contrast is the Lighthouse CI gate (Phase 5b).

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
