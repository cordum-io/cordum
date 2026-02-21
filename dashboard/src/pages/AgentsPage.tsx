import { useMemo, useState } from "react";
import { ChevronDown, ChevronUp, ChevronsUpDown, Users, List, LayoutGrid } from "lucide-react";
import { Badge } from "../components/ui/Badge";
import { Select } from "../components/ui/Select";
import { useWorkers } from "../hooks/useWorkers";
import { useUiStore } from "../state/ui";
import { PoolGroupedView } from "../components/agents/PoolGroupedView";
import { WorkerDetailDrawer } from "../components/agents/WorkerDetailDrawer";
import { SnapshotWriterBadge } from "../components/agents/SnapshotWriterBadge";
import { cn } from "../lib/utils";
import type { Worker } from "../api/types";
import { DataFreshness } from "../components/ui/DataFreshness";
import { usePageTitle } from "../hooks/usePageTitle";
import { useStatus } from "../hooks/useStatus";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type SortKey =
  | "name"
  | "pool"
  | "status"
  | "capabilities"
  | "load"
  | "lastHeartbeat"
  | "uptime"
  | "version";

type SortDir = "asc" | "desc";

function statusVariant(status: string): "success" | "warning" | "danger" | "default" {
  switch (status) {
    case "online":
    case "active":
      return "success";
    case "draining":
      return "warning";
    case "offline":
    case "error":
      return "danger";
    default:
      return "default";
  }
}

function relativeTime(iso?: string): string {
  if (!iso) return "\u2014";
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 0) return "just now";
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

function formatUptime(seconds?: number): string {
  if (seconds == null) return "\u2014";
  if (seconds < 60) return `${seconds}s`;
  const mins = Math.floor(seconds / 60);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  const remainMins = mins % 60;
  if (hrs < 24) return remainMins > 0 ? `${hrs}h ${remainMins}m` : `${hrs}h`;
  const days = Math.floor(hrs / 24);
  const remainHrs = hrs % 24;
  return remainHrs > 0 ? `${days}d ${remainHrs}h` : `${days}d`;
}

function compare(a: Worker, b: Worker, key: SortKey): number {
  switch (key) {
    case "name":
      return a.name.localeCompare(b.name);
    case "pool":
      return a.pool.localeCompare(b.pool);
    case "status":
      return a.status.localeCompare(b.status);
    case "capabilities":
      return a.capabilities.length - b.capabilities.length;
    case "load":
      return a.activeJobs / (a.capacity || 1) - b.activeJobs / (b.capacity || 1);
    case "lastHeartbeat":
      return (
        new Date(a.lastHeartbeat ?? 0).getTime() -
        new Date(b.lastHeartbeat ?? 0).getTime()
      );
    case "uptime":
      return (a.uptime ?? 0) - (b.uptime ?? 0);
    case "version":
      return (a.version ?? "").localeCompare(b.version ?? "");
    default:
      return 0;
  }
}

// ---------------------------------------------------------------------------
// Sort header
// ---------------------------------------------------------------------------

function SortHeader({
  label,
  sortKey,
  activeKey,
  activeDir,
  onSort,
}: {
  label: string;
  sortKey: SortKey;
  activeKey: SortKey;
  activeDir: SortDir;
  onSort: (key: SortKey) => void;
}) {
  const isActive = activeKey === sortKey;
  return (
    <th
      className="cursor-pointer select-none whitespace-nowrap px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-muted hover:text-ink"
      onClick={() => onSort(sortKey)}
    >
      <span className="inline-flex items-center gap-1">
        {label}
        {isActive ? (
          activeDir === "asc" ? (
            <ChevronUp className="h-3.5 w-3.5" />
          ) : (
            <ChevronDown className="h-3.5 w-3.5" />
          )
        ) : (
          <ChevronsUpDown className="h-3.5 w-3.5 opacity-30" />
        )}
      </span>
    </th>
  );
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export default function AgentsPage() {
  usePageTitle("Agent Fleet");
  const { data: workers = [], isLoading, error, dataUpdatedAt, refetch, isRefetching } = useWorkers();
  const { data: statusData } = useStatus();
  const agentsView = useUiStore((s) => s.agentsView);
  const setAgentsView = useUiStore((s) => s.setAgentsView);

  const [sortKey, setSortKey] = useState<SortKey>("name");
  const [sortDir, setSortDir] = useState<SortDir>("asc");
  const [poolFilter, setPoolFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [selectedWorkerId, setSelectedWorkerId] = useState<string | null>(null);

  function handleSort(key: SortKey) {
    if (key === sortKey) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir("asc");
    }
  }

  const pools = useMemo(
    () => [...new Set(workers.map((w) => w.pool))].sort(),
    [workers],
  );

  const filtered = useMemo(() => {
    let result = workers;
    if (poolFilter) result = result.filter((w) => w.pool === poolFilter);
    if (statusFilter) result = result.filter((w) => w.status === statusFilter);
    return [...result].sort((a, b) => {
      const c = compare(a, b, sortKey);
      return sortDir === "asc" ? c : -c;
    });
  }, [workers, poolFilter, statusFilter, sortKey, sortDir]);

  const sh = (label: string, key: SortKey) => (
    <SortHeader
      label={label}
      sortKey={key}
      activeKey={sortKey}
      activeDir={sortDir}
      onSort={handleSort}
    />
  );

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="font-display text-2xl font-bold text-ink">Agents</h1>
          <DataFreshness dataUpdatedAt={dataUpdatedAt} onRefresh={refetch} isRefetching={isRefetching} />
        </div>
        <div className="flex rounded-lg border border-border">
          <button
            type="button"
            className={cn(
              "flex items-center gap-1 rounded-l-lg px-3 py-1.5 text-xs font-medium transition",
              agentsView === "table"
                ? "bg-accent/15 text-accent"
                : "text-muted hover:text-ink",
            )}
            onClick={() => setAgentsView("table")}
            aria-label="Table view"
          >
            <List className="h-4 w-4" />
          </button>
          <button
            type="button"
            className={cn(
              "flex items-center gap-1 rounded-r-lg px-3 py-1.5 text-xs font-medium transition",
              agentsView === "cards"
                ? "bg-accent/15 text-accent"
                : "text-muted hover:text-ink",
            )}
            onClick={() => setAgentsView("cards")}
            aria-label="Card view"
          >
            <LayoutGrid className="h-4 w-4" />
          </button>
        </div>
      </div>

      <SnapshotWriterBadge snapshotMeta={statusData?.snapshot_meta} />

      {/* Filters */}
      <div className="flex flex-wrap gap-3">
        <Select
          value={poolFilter}
          onChange={(e) => setPoolFilter(e.target.value)}
          className="w-44"
        >
          <option value="">All pools</option>
          {pools.map((p) => (
            <option key={p} value={p}>
              {p}
            </option>
          ))}
        </Select>

        <Select
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
          className="w-44"
        >
          <option value="">All statuses</option>
          <option value="online">online</option>
          <option value="offline">offline</option>
          <option value="draining">draining</option>
        </Select>
      </div>

      {isLoading && <p className="text-sm text-muted">Loading workers...</p>}

      {error && (
        <p className="text-sm text-danger">
          Failed to load workers:{" "}
          {error instanceof Error ? error.message : "Unknown error"}
        </p>
      )}

      {!isLoading && !error && workers.length === 0 && (
        <div className="flex flex-col items-center justify-center rounded-3xl border border-dashed border-border py-16 text-center">
          <Users className="mb-3 h-10 w-10 text-muted" />
          <p className="text-sm text-muted">No workers registered.</p>
        </div>
      )}

      {!isLoading && !error && workers.length > 0 && filtered.length === 0 && (
        <div className="flex flex-col items-center justify-center rounded-3xl border border-dashed border-border py-12 text-center">
          <p className="text-sm text-muted">
            No workers match the current filters.
          </p>
        </div>
      )}

      {filtered.length > 0 && agentsView === "table" && (
        <div className="overflow-x-auto rounded-2xl border border-border">
          <table className="w-full text-sm">
            <thead className="border-b border-border bg-surface2/50">
              <tr>
                {sh("Name", "name")}
                {sh("Pool", "pool")}
                {sh("Status", "status")}
                {sh("Capabilities", "capabilities")}
                {sh("Active / Cap", "load")}
                {sh("Heartbeat", "lastHeartbeat")}
                {sh("Uptime", "uptime")}
                {sh("Version", "version")}
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {filtered.map((w) => (
                <tr
                  key={w.id}
                  className="cursor-pointer transition-colors hover:bg-surface2/30"
                  onClick={() => setSelectedWorkerId(w.id)}
                >
                  <td className="whitespace-nowrap px-4 py-3 font-medium text-ink">
                    {w.name}
                  </td>
                  <td className="whitespace-nowrap px-4 py-3 text-muted">
                    {w.pool}
                  </td>
                  <td className="whitespace-nowrap px-4 py-3">
                    <Badge variant={statusVariant(w.status)}>{w.status}</Badge>
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex flex-wrap gap-1">
                      {w.capabilities.length > 0 ? (
                        w.capabilities.map((c) => (
                          <Badge key={c} variant="info" className="text-[11px]">
                            {c}
                          </Badge>
                        ))
                      ) : (
                        <span className="text-muted">&mdash;</span>
                      )}
                    </div>
                  </td>
                  <td className="whitespace-nowrap px-4 py-3">
                    <span
                      className={
                        w.activeJobs >= w.capacity && w.capacity > 0
                          ? "text-danger font-semibold"
                          : "text-ink"
                      }
                    >
                      {w.activeJobs}/{w.capacity}
                    </span>
                  </td>
                  <td className="whitespace-nowrap px-4 py-3 text-muted">
                    {relativeTime(w.lastHeartbeat)}
                  </td>
                  <td className="whitespace-nowrap px-4 py-3 text-muted">
                    {formatUptime(w.uptime)}
                  </td>
                  <td className="whitespace-nowrap px-4 py-3 text-muted">
                    {w.version ?? "\u2014"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {filtered.length > 0 && agentsView === "cards" && (
        <PoolGroupedView workers={filtered} onWorkerClick={setSelectedWorkerId} />
      )}

      <WorkerDetailDrawer
        workerId={selectedWorkerId}
        onClose={() => setSelectedWorkerId(null)}
      />
    </div>
  );
}
