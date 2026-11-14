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
import { DataTable, type DataTableColumn } from "@/components/primitives/DataTable";
import { EmptyState } from "@/components/ui/EmptyState";
import { Link, useNavigate } from "react-router-dom";

interface JobRow { id: string; agent: string; decision: "allow" | "deny"; }

const columns: DataTableColumn<JobRow>[] = [
  { accessorKey: "agent", header: "Agent", enableSorting: true },
  {
    accessorKey: "id",
    header: "Job ID",
    // Rows have no keyboard handler, so give each row a real link: keyboard
    // and screen-reader users open the job from it. DataTable ignores row
    // clicks that start on a link, so the link and onRowClick never both fire.
    cell: ({ row }) => <Link to={`/jobs/${row.original.id}`}>{row.original.id}</Link>,
  },
];

// The host page passes its rows; a mouse click anywhere on a row also
// navigates, with react-router.
export function RecentJobsTable({ rows }: { rows: JobRow[] }) {
  const navigate = useNavigate();
  return (
    <DataTable<JobRow>
      columns={columns}
      data={rows}
      emptyState={<EmptyState title="No recent jobs" />}
      decisionAccessor={(row) => row.decision}
      onRowClick={(row) => navigate(`/jobs/${row.id}`)}
    />
  );
}
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

Client-side via TanStack `createSortedRowModel()` and the stock `sortFns`,
both registered in DataTable's module-level `tableFeatures`. Headers expose
`aria-sort`, are keyboard-focusable when sortable, and respond to Enter or
Space.

The behavior below is checked against **@tanstack/table-core 9.3.1
(dashboard/pnpm-lock.yaml)**. Source paths are relative to that package.

The first header click sorts in the column's first direction, the second
reverses it, and the third clears sorting. DataTable keeps TanStack's default
`enableSortingRemoval` (checked against `column_getNextSortingOrder` in
`dist/features/row-sorting/rowSortingFeature.utils.js`).

The first direction comes from the first non-nullish value among the first
10 filtered rows: a string column cycles **asc → desc → unsorted**; other
columns (numbers, dates, booleans, or no non-nullish sampled value) cycle
**desc → asc → unsorted**. `columnDef.sortDescFirst` overrides that first
direction (checked against `column_getAutoSortDir` and
`column_getFirstSortDir` in `dist/features/row-sorting/rowSortingFeature.utils.js`).

Automatic sorting samples the first 10 filtered rows and selects from the
registered stock `sortFns`: string values containing digits select
`alphanumeric` for natural order (`item2` before `item10`); other strings
select case-insensitive `text`; Dates select `datetime`. For mixed types,
the first Date or digit-containing string in the sample wins. Without the
`sortFns` slot these fall back to `basic` (checked against
`column_getAutoSortFn` in `dist/features/row-sorting/rowSortingFeature.utils.js`
and the comparators in `dist/features/row-sorting/sortFns.js`).
`DataTable.sortfns.test.tsx` pins natural order, case-insensitive order, and
the numeric toggle cycle.

The default sorting function places `undefined` at the end ascending and
the beginning descending — override via `columnDef.sortFn` (a comparator
function or a registered stock name such as `text` or `alphanumeric`) /
`columnDef.sortUndefined` when this is wrong for your data (checked against
`getDefaultColumnDef`, which sets `sortFn: "auto"` and `sortUndefined: 1`,
in `dist/features/row-sorting/rowSortingFeature.js`).

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

1. Replace the `Column<T>[]` shape with `DataTableColumn<T>[]` (exported from `DataTable`).
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
