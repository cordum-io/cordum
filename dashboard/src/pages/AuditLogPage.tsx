import { useState, useMemo, useCallback } from "react";
import { ChevronUp, ChevronDown, ChevronsUpDown } from "lucide-react";
import { useAuditLog, type AuditFilters } from "../hooks/useAudit";
import { Card } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Button } from "../components/ui/Button";
import { AuditExport } from "../components/audit/AuditExport";
import { cn } from "../lib/utils";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatTimestamp(iso?: string): string {
  if (!iso) return "\u2014";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    fractionalSecondDigits: 3,
  } as Intl.DateTimeFormatOptions);
}

const TIME_PRESETS = [
  { label: "1h", value: "1h" },
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
  { label: "30d", value: "30d" },
  { label: "All", value: "" },
] as const;

const PER_PAGE_OPTIONS = [25, 50, 100] as const;

type SortKey = "time" | "action";
type SortDir = "asc" | "desc";

// ---------------------------------------------------------------------------
// Sortable header
// ---------------------------------------------------------------------------

function SortableHeader({
  label,
  sortKey,
  activeKey,
  activeDir,
  onSort,
}: {
  label: string;
  sortKey: SortKey;
  activeKey: SortKey | null;
  activeDir: SortDir;
  onSort: (key: SortKey) => void;
}) {
  const isActive = activeKey === sortKey;
  return (
    <th
      className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-muted cursor-pointer select-none hover:text-ink transition-colors"
      onClick={() => onSort(sortKey)}
    >
      <span className="inline-flex items-center gap-1">
        {label}
        {isActive ? (
          activeDir === "asc" ? (
            <ChevronUp className="h-3 w-3" />
          ) : (
            <ChevronDown className="h-3 w-3" />
          )
        ) : (
          <ChevronsUpDown className="h-3 w-3 opacity-40" />
        )}
      </span>
    </th>
  );
}

// ---------------------------------------------------------------------------
// AuditLogPage
// ---------------------------------------------------------------------------

export default function AuditLogPage() {
  // Filter state
  const [eventType, setEventType] = useState("");
  const [actor, setActor] = useState("");
  const [resourceType, setResourceType] = useState("");
  const [timeRange, setTimeRange] = useState("");

  // Sort state
  const [sortKey, setSortKey] = useState<SortKey | null>("time");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  // Pagination state
  const [page, setPage] = useState(0);
  const [perPage, setPerPage] = useState<number>(25);

  // Build filters
  const filters: AuditFilters = useMemo(
    () => ({
      eventType: eventType ? [eventType] : undefined,
      actor: actor || undefined,
      resourceType: resourceType || undefined,
      timeRange: timeRange || undefined,
      sort: sortKey ? `${sortKey}-${sortDir}` : undefined,
    }),
    [eventType, actor, resourceType, timeRange, sortKey, sortDir],
  );

  const { data, isLoading, isError, filtered } = useAuditLog(filters);
  const allItems = data?.items ?? [];

  // Derive unique values for filter dropdowns
  const eventTypes = useMemo(
    () => [...new Set(allItems.map((e) => e.eventType).filter(Boolean))].sort(),
    [allItems],
  );
  const resourceTypes = useMemo(
    () => [...new Set(allItems.map((e) => e.resourceType).filter(Boolean))].sort(),
    [allItems],
  );

  // Pagination
  const totalFiltered = filtered.length;
  const totalPages = Math.max(1, Math.ceil(totalFiltered / perPage));
  const paged = useMemo(
    () => filtered.slice(page * perPage, (page + 1) * perPage),
    [filtered, page, perPage],
  );

  // Reset page when filters change
  const resetPage = useCallback(() => setPage(0), []);

  const handleSort = useCallback(
    (key: SortKey) => {
      if (sortKey === key) {
        setSortDir((d) => (d === "asc" ? "desc" : "asc"));
      } else {
        setSortKey(key);
        setSortDir("desc");
      }
      resetPage();
    },
    [sortKey, resetPage],
  );

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="font-display text-2xl font-bold text-ink">Audit Log</h1>
          <p className="text-sm text-muted">
            Policy audit events from the control plane.
          </p>
        </div>
        <AuditExport filters={filters} />
      </div>

      {/* Filter bar */}
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-44">
          <label className="mb-1 block text-[11px] font-semibold uppercase tracking-wider text-muted">
            Event Type
          </label>
          <Select
            value={eventType}
            onChange={(e) => {
              setEventType(e.target.value);
              resetPage();
            }}
          >
            <option value="">All</option>
            {eventTypes.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </Select>
        </div>
        <div className="w-44">
          <label className="mb-1 block text-[11px] font-semibold uppercase tracking-wider text-muted">
            Actor
          </label>
          <Input
            value={actor}
            onChange={(e) => {
              setActor(e.target.value);
              resetPage();
            }}
            placeholder="Filter by actor\u2026"
            className="h-[42px]"
          />
        </div>
        <div className="w-44">
          <label className="mb-1 block text-[11px] font-semibold uppercase tracking-wider text-muted">
            Resource Type
          </label>
          <Select
            value={resourceType}
            onChange={(e) => {
              setResourceType(e.target.value);
              resetPage();
            }}
          >
            <option value="">All</option>
            {resourceTypes.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </Select>
        </div>
        <div>
          <label className="mb-1 block text-[11px] font-semibold uppercase tracking-wider text-muted">
            Time Range
          </label>
          <div className="flex gap-1">
            {TIME_PRESETS.map((p) => (
              <button
                key={p.value}
                type="button"
                onClick={() => {
                  setTimeRange(p.value);
                  resetPage();
                }}
                className={cn(
                  "rounded-full px-3 py-1.5 text-xs font-semibold transition",
                  timeRange === p.value
                    ? "bg-accent/15 text-accent"
                    : "text-muted hover:bg-surface2",
                )}
              >
                {p.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* Loading */}
      {isLoading && (
        <Card>
          <p className="py-8 text-center text-sm text-muted">Loading audit log\u2026</p>
        </Card>
      )}

      {/* Error */}
      {!isLoading && isError && (
        <Card>
          <p className="py-8 text-center text-sm text-danger">
            Failed to load audit log.
          </p>
        </Card>
      )}

      {/* Empty */}
      {!isLoading && !isError && totalFiltered === 0 && (
        <Card>
          <p className="py-8 text-center text-sm text-muted">
            {allItems.length > 0
              ? "No entries match the current filters."
              : "No audit entries."}
          </p>
        </Card>
      )}

      {/* Table */}
      {!isLoading && !isError && totalFiltered > 0 && (
        <>
          <div className="overflow-hidden rounded-2xl border border-border">
            <table className="w-full text-sm">
              <thead className="border-b border-border bg-surface2/50">
                <tr>
                  <SortableHeader
                    label="Time"
                    sortKey="time"
                    activeKey={sortKey}
                    activeDir={sortDir}
                    onSort={handleSort}
                  />
                  <SortableHeader
                    label="Action"
                    sortKey="action"
                    activeKey={sortKey}
                    activeDir={sortDir}
                    onSort={handleSort}
                  />
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-muted">
                    Actor
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-muted">
                    Resource
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-muted">
                    Message
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {paged.map((entry) => (
                  <tr key={entry.id} className="hover:bg-surface2/40 transition-colors">
                    <td className="px-4 py-3 text-xs text-muted whitespace-nowrap">
                      {formatTimestamp(entry.timestamp)}
                    </td>
                    <td className="px-4 py-3">
                      <Badge variant="info">{entry.eventType || entry.action}</Badge>
                    </td>
                    <td className="px-4 py-3 text-xs text-ink">
                      {entry.actor || "\u2014"}
                    </td>
                    <td className="px-4 py-3 text-xs text-ink">
                      {entry.resourceType}
                      {entry.resourceId ? `:${entry.resourceId}` : ""}
                    </td>
                    <td className="px-4 py-3 text-xs text-muted">
                      {entry.message || "\u2014"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* Pagination */}
          <div className="flex items-center justify-between text-sm">
            <span className="text-xs text-muted">
              Showing {page * perPage + 1}\u2013
              {Math.min((page + 1) * perPage, totalFiltered)} of{" "}
              {totalFiltered} entries
            </span>
            <div className="flex items-center gap-3">
              <Select
                className="h-8 w-20 text-xs"
                value={perPage}
                onChange={(e) => {
                  setPerPage(Number(e.target.value));
                  setPage(0);
                }}
              >
                {PER_PAGE_OPTIONS.map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </Select>
              <div className="flex gap-1">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page === 0}
                  onClick={() => setPage((p) => p - 1)}
                >
                  Newer
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page >= totalPages - 1}
                  onClick={() => setPage((p) => p + 1)}
                >
                  Older
                </Button>
              </div>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
