# Cordum Dashboard Agent Notes

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
