# `src/components/primitives/`

Cordum dashboard v2.5 ships a small, deliberately-curated primitive set for
the in-place hero rewrites (Audit, Jobs, Home, Policy Studio, Workflow
Studio). Primitives in this folder are:

- **Generic** — typed by `<T>`, no domain coupling.
- **Composable** — accept slot props (`emptyState`, `header`, etc.) instead
  of hard-coding child markup.
- **Theme-aware** — only consume CSS variables from `src/styles/index.css`;
  never introduce new colors.
- **Test-co-located** — each primitive ships with a sibling `.test.tsx`.

## Inventory

| Primitive   | Purpose                                                                                          | Powered by                                  |
| ----------- | ------------------------------------------------------------------------------------------------ | ------------------------------------------- |
| `DataTable` | Sortable, virtualization-aware table with an optional decision-identity left-edge variant.       | `@tanstack/react-table` + `@tanstack/react-virtual` |

More primitives land alongside their hero-page rewrites; add an entry above
when shipping a new one.

## DataTable

### When to use

Reach for `primitives/DataTable` whenever you would have hand-rolled a
`<table>` plus sort handlers. It is the canonical surface for any list that:

- needs client-side sort,
- benefits from row-level identity (decision tier, status edge),
- might exceed ~100 rows.

For very small static lists where you want zero overhead, a plain `<table>`
is still fine. For server-side sort + paging, the legacy `ui/LegacyDataTable`
is still alive — see migration story below.

### Quick example

```tsx
import { type ColumnDef } from "@tanstack/react-table";
import { DataTable } from "@/components/primitives/DataTable";
import { EmptyState } from "@/components/ui/EmptyState";

interface JobRow { id: string; agent: string; decision: "allow" | "deny"; }

const columns: ColumnDef<JobRow, unknown>[] = [
  { accessorKey: "agent", header: "Agent", enableSorting: true },
  { accessorKey: "id", header: "Job ID" },
];

<DataTable<JobRow>
  columns={columns}
  data={rows}
  emptyState={<EmptyState title="No recent jobs" />}
  decisionAccessor={(row) => row.decision}
  onRowClick={(row) => navigate(`/jobs/${row.id}`)}
/>
```

### Variants

- **Default** — standard table rows.
- **Decision-identity** — pass `decisionAccessor`. Each row gets a 3px
  left-edge `box-shadow` tinted by the safety tier:

  | Tier                       | Token                  |
  | -------------------------- | ---------------------- |
  | `allow`                    | `--color-success`      |
  | `deny`                     | `--color-danger`       |
  | `require_approval`         | `--color-warning`      |
  | `allow_with_constraints`   | `--color-warning`      |
  | `throttle`                 | `--color-warning`      |

  Tokens come from the existing palette — no new colors. The edge swaps
  automatically between light and dark themes.

### Virtualization

Triggers when `data.length > 100` (the exported `VIRTUALIZE_THRESHOLD`
constant). Above the threshold the body is wrapped in a fixed-height
scrollable container (`virtualizedHeight` prop, default 480px) and only the
visible window of `<tr>` elements is mounted. Native `<table>` semantics
are preserved via spacer `<tr>` elements that absorb the unrendered scroll
height.

When you adopt the primitive on a page that previously rendered an
unbounded scrolling list, you may need to allocate a fixed-height parent —
the primitive does not stretch to fill the page; it owns its own scroll.

### Sort

Client-side via TanStack `getSortedRowModel`. Toggle order is
**asc → desc → unsorted** on repeated header clicks. Headers expose
`aria-sort`, are keyboard-focusable when sortable, and respond to Enter or
Space. The default sorting function places `undefined` at the end ascending
and the beginning descending — override via `columnDef.sortingFn` /
`columnDef.sortUndefined` when this is wrong for your data.

For server-side sort + paging (e.g. an admin audit list with a backend
ORDER BY), keep using `ui/LegacyDataTable` until a server-sort sibling
primitive lands.

### Row click vs interactive children

`onRowClick` fires when a row is clicked **outside** an interactive child.
The primitive checks `event.target.closest(...)` against
`button, a, input, select, textarea, [role='button'], [data-row-action]`
before invoking the handler. Decorate any custom non-interactive control
that should opt out of the row click with `data-row-action`.

## Migration from `ui/DataTable`

`ui/DataTable` was renamed to `ui/LegacyDataTable` (Phase 2). Existing
consumers — DelegationsPage, AgentDelegationsPanel, evals/RunHistoryTable,
evals/DatasetList — kept their behavior unchanged; only the import name
moved.

When migrating a page from the legacy primitive to the new one:

1. Replace the `Column<T>[]` shape with TanStack `ColumnDef<T, unknown>[]`.
2. Move sort state out of the parent (the new primitive owns it). If the
   page also pages server-side, keep `LegacyDataTable` for now.
3. Replace `keyExtractor` with `accessorKey` / `id` on each column.
4. Replace `emptyMessage="…"` with `emptyState={<EmptyState … />}`.
5. (Optional) opt into `decisionAccessor` for governance pages.
6. (Optional) the primitive virtualizes >100 rows automatically — verify
   the parent has a sensible height bound.

The deletion of `ui/LegacyDataTable` and its consumers' final cutover is
planned for **Phase 5** (post-hero-rewrite cleanup); do not delete it as
part of a Phase 3 / Phase 4 page migration.
