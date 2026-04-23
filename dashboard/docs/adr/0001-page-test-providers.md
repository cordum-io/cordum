# ADR 0001: Page test providers and network fakes

## Status

Accepted.

## Context

`ApprovalsPage` now composes `McpApprovalsSection`. That section calls `useMcpApprovals`, which uses React Query. The existing page test rendered `ApprovalsPage` directly under a `MemoryRouter`, so 15 page-level assertions failed with `No QueryClient set, use QueryClientProvider to set one`.

A one-file fix that wrapped only `ApprovalsPage.test.ts` in `QueryClientProvider` was rejected because it would leave every page test to rebuild its own provider stack. The next page section that adds a query hook would repeat the same failure mode.

## Decision

Dashboard page tests must use `renderWithProviders` from `src/test-utils/render`.

`renderWithProviders` supplies the provider stack page tests need from `App.tsx`: a per-test React Query `QueryClient`, `MemoryRouter`, theme synchronization, and the Sonner toaster. Query retry, cache time, and stale time are disabled so tests are deterministic and do not share cache state.

Network data for hooks should be supplied with MSW handlers from `src/test-utils/msw`. Base handlers in `src/test-utils/handlers.ts` return empty but shape-valid API payloads for page-load queries. Tests that need specific data override those responses with `server.use(...)` inside the test.

New page tests must not mock hook modules such as `@/hooks/useApprovals`. The page should execute its real hooks; MSW intercepts the HTTP boundary.

## Rejected alternatives

### Shared provider only

A shared `QueryClientProvider` wrapper alone would fix the crash but would still push data setup back toward hook-level `vi.mock(...)`, which does not exercise the real query hook and violates the production-shaped test rule.

### MSW only

Using MSW without a shared render helper would make every page test rebuild the provider stack. That is the one-off pattern that failed here and would drift as `App.tsx` changes.

### Hook injection on pages

Adding injectable data sources to production page components would make the production surface more complex solely for tests. The page can stay production-shaped if tests provide App-like providers and API-shaped network responses.

## Consequences

- Add `msw` as a dashboard dev dependency.
- Add or update default handlers whenever a page-level query hook is introduced.
- Override MSW handlers per test with `server.use(...)` for non-empty or error states.
- Keep hook mocks for isolated hook/component unit tests only when they are not page-level provider tests; do not add new page-level hook mocks.
- A global test diagnostic points `No QueryClient set` failures back to this ADR and `renderWithProviders`.

## Migration rule

Any page-level test that renders a page composing React Query hooks must use:

```tsx
import { renderWithProviders } from "@/test-utils/render";
import { server, http, HttpResponse } from "@/test-utils/msw";
```

Default empty states should come from base handlers. Data-specific tests should call `server.use(...)` before rendering.