# ADR-007: Dashboard State Management — Zustand + React Query

- Status: Accepted
- Date: 2026-01-20

## Context

The Cordum dashboard is a React SPA that needs to:
- Fetch and cache server data (jobs, workflows, policies, approvals)
- Manage client-only UI state (sidebar, modals, toasts, theme)
- Handle real-time updates via WebSocket
- Support optimistic updates for mutations (approve, cancel, delete)

## Decision

Use **React Query** for server state and **Zustand** for client state. No Redux.

### React Query for Server State

All data fetching uses React Query hooks (`useQuery`, `useMutation`):
- Automatic cache invalidation on mutations
- Background refetch with configurable intervals
- Optimistic updates for approval/cancel actions
- Query key conventions: `["jobs"]`, `["job", id]`, `["workflow-runs", workflowId]`

Pattern:
```typescript
export function useJobs(filters?: JobFilters) {
  return useQuery({
    queryKey: ["jobs", filters],
    queryFn: () => get<Job[]>("/jobs", { params: filters }),
  });
}

export function useCancelJob() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => post(`/jobs/${id}/cancel`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["jobs"] });
      useToastStore.getState().addToast({ type: "success", title: "Job cancelled" });
    },
  });
}
```

### Zustand for Client State

Minimal stores for UI-only concerns:
- `useToastStore` — toast notification queue
- `useUIStore` — sidebar collapse, theme, command palette
- `useAuthStore` — API key, tenant, principal (from localStorage)

Zustand was chosen over Redux for:
- Zero boilerplate (no actions, reducers, selectors)
- Direct store access outside React (`useToastStore.getState()`)
- Tiny bundle size (~1 KB)

### Why Not Redux

| Concern | Redux Toolkit | Zustand + React Query |
|---------|--------------|----------------------|
| Server cache | Manual or RTK Query | React Query (built for this) |
| Boilerplate | Slices, thunks, selectors | Minimal store definitions |
| Bundle size | ~12 KB | ~1 KB + ~13 KB |
| DevTools | Redux DevTools | React Query DevTools + Zustand middleware |
| Learning curve | High (actions, reducers, middleware) | Low (just functions) |

Redux is designed for complex client state graphs. The dashboard's client state
is simple (toasts, UI toggles, auth token). React Query handles the complex
part (server data lifecycle).

### Auth Model

Stateless authentication — no cookies, no CSRF:
- API key stored in Zustand + localStorage
- JWT tokens for user/password auth (short-lived, no refresh in v1)
- WebSocket auth via subprotocol header (`cordum-api-key.<base64>`)
- `X-Tenant-ID` header on every request

Key source files:
- `dashboard/src/hooks/` — React Query hooks per domain
- `dashboard/src/state/` — Zustand stores
- `dashboard/src/lib/api.ts` — HTTP client with auth headers

## Consequences

Positive:
- Clear separation: server data (React Query) vs UI state (Zustand)
- Automatic cache management reduces manual state synchronization
- Optimistic updates provide responsive UI for approval workflows
- Small bundle, fast initial load

Tradeoffs:
- Two state systems to understand (React Query + Zustand)
- No single store for debugging all state (use respective DevTools)
- Stale closure risk with Zustand in React effects (mitigated with `useRef` pattern)
