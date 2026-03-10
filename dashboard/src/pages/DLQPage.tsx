/*
 * DESIGN: "Control Surface" — Dead Letter Queue
 * PRD: Bulk actions with checkbox selection + floating action bar
 */
import { Fragment, useState, useMemo } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { useDLQ, useRetryDLQ, useDeleteDLQ, useBulkRetryDLQ, useBulkDeleteDLQ } from "@/hooks/useDLQ";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import { Search, RefreshCw, AlertTriangle, Play, Trash2, CheckCircle2, Download, X } from "lucide-react";
import { cn, formatRelativeTime, clickableRowProps } from "@/lib/utils";
import { toast } from "sonner";

// ---------------------------------------------------------------------------
// Exported for unit tests (following SettingsKeysPage pattern)
// ---------------------------------------------------------------------------

/** Builds the structured diagnostics object shown in the expanded DLQ row. */
export function buildDLQEntryDetails(d: {
  jobId: string;
  status?: string;
  reasonCode?: string;
  lastState?: string;
  originalTopic?: string;
  attempts?: number;
  retryCount?: number;
  failedAt?: string;
  createdAt?: string;
}) {
  return {
    jobId: d.jobId,
    status: d.status,
    reasonCode: d.reasonCode,
    lastState: d.lastState,
    originalTopic: d.originalTopic,
    attempts: d.attempts ?? d.retryCount ?? 0,
    failedAt: d.failedAt ?? d.createdAt,
  };
}

/** Resolves the error message for the expanded DLQ row, with fallback chain. */
export function resolveDLQError(d: { error?: string; reason?: string }): string {
  return d.error || d.reason || "No error message";
}

export default function DLQPage() {
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [confirmBulk, setConfirmBulk] = useState<"retry" | "purge" | null>(null);
  const [expandedRow, setExpandedRow] = useState<string | null>(null);

  const { data, isLoading, refetch } = useDLQ({ limit: 200 });
  const retryMutation = useRetryDLQ();
  const purgeMutation = useDeleteDLQ();
  const bulkRetryMutation = useBulkRetryDLQ();
  const bulkPurgeMutation = useBulkDeleteDLQ();

  const items = useMemo(() => {
    return (data?.items ?? []).filter((d) => {
      if (!search) return true;
      const q = search.toLowerCase();
      return d.jobId.toLowerCase().includes(q) || (d.originalTopic ?? "").toLowerCase().includes(q) || (d.error ?? "").toLowerCase().includes(q);
    });
  }, [data, search]);

  const avgAttempts = items.length > 0 ? (items.reduce((s, i) => s + (i.attempts ?? i.retryCount ?? 0), 0) / items.length).toFixed(1) : "0";
  const allSelected = items.length > 0 && items.every((i) => selected.has(i.id));

  const toggleAll = () => {
    if (allSelected) {
      setSelected(new Set());
    } else {
      setSelected(new Set(items.map((i) => i.id)));
    }
  };

  const toggleOne = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const exportCSV = () => {
    const rows = items.map((d) => [d.id, d.jobId, d.originalTopic ?? "", d.error ?? "", d.attempts ?? d.retryCount ?? 0, d.failedAt ?? d.createdAt ?? ""].join(","));
    const csv = ["id,jobId,topic,error,attempts,failedAt", ...rows].join("\n");
    const blob = new Blob([csv], { type: "text/csv" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `dlq-export-${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(url);
    toast.success("Exported CSV");
  };

  return (
    <div className="space-y-6">
      <PageHeader
        label="Platform"
        title="Dead Letter Queue"
        subtitle="Failed messages requiring attention"
        actions={
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={exportCSV}>
              <Download className="w-3 h-3 mr-1" />
              Export CSV
            </Button>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              <RefreshCw className="w-3 h-3 mr-1" />
              Refresh
            </Button>
          </div>
        }
      />

      {/* KPI Row */}
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3 }}
        className="grid grid-cols-2 lg:grid-cols-3 gap-4"
      >
        {isLoading ? (
          Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)
        ) : (
          <>
            <div className={cn("instrument-card", items.length > 0 ? "status-danger" : "")}>
              <div className="flex items-center justify-between mb-3">
                <span className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Dead Letters</span>
                <AlertTriangle className={cn("w-4 h-4", items.length > 0 ? "text-destructive" : "text-[var(--color-success)]")} />
              </div>
              <span className={cn("font-mono text-2xl font-bold", items.length > 0 ? "text-destructive" : "text-[var(--color-success)]")}>{data?.items?.length ?? 0}</span>
              <p className="text-xs text-muted-foreground mt-1">{items.length > 0 ? "Requires attention" : "Queue clear"}</p>
            </div>
            <div className="instrument-card">
              <div className="flex items-center justify-between mb-3">
                <span className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Avg Attempts</span>
              </div>
              <span className="font-mono text-2xl font-bold text-foreground">{avgAttempts}</span>
              <p className="text-xs text-muted-foreground mt-1">Before dead-lettering</p>
            </div>
            <div className="instrument-card">
              <div className="flex items-center justify-between mb-3">
                <span className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Status</span>
                <span className={cn("w-1.5 h-1.5 rounded-full status-pulse", items.length > 0 ? "bg-destructive" : "bg-[var(--color-success)]")} />
              </div>
              <span className={cn("font-mono text-sm font-bold", items.length > 0 ? "text-[var(--color-warning)]" : "text-[var(--color-success)]")}>
                {items.length > 0 ? "Attention Required" : "All Clear"}
              </span>
            </div>
          </>
        )}
      </motion.div>

      {/* Search */}
      <div className="relative max-w-sm">
        <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
        <input
          type="text"
          placeholder="Search by job ID, topic, or error..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="h-8 w-full pl-8 pr-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
        />
      </div>

      {/* Table with checkboxes */}
      {isLoading ? (
        <div className="instrument-card">
          <SkeletonTable rows={6} />
        </div>
      ) : items.length === 0 ? (
        <EmptyState
          icon={<CheckCircle2 className="w-5 h-5" />}
          title="DLQ is empty"
          description="No failed messages — all systems healthy"
        />
      ) : (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3, delay: 0.1 }}
          className="instrument-card status-danger overflow-hidden"
        >
          <div className="overflow-x-auto">
          <table className="w-full min-w-[750px]">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th className="w-10 px-3 py-3">
                  <input
                    type="checkbox"
                    checked={allSelected}
                    onChange={toggleAll}
                    className="w-3.5 h-3.5 rounded border-border bg-surface-0 text-cordum focus:ring-cordum accent-[oklch(0.82_0.18_165)]"
                  />
                </th>
                <th className="text-left px-4 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Job ID</th>
                <th className="text-left px-4 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Topic</th>
                <th className="text-left px-4 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Error</th>
                <th className="text-center px-4 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Attempts</th>
                <th className="text-right px-4 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-wider">Failed</th>
                <th className="px-4 py-3"></th>
              </tr>
            </thead>
            <tbody>
              {items.map((d) => (
                <Fragment key={d.id}>
                  <tr
                    className={cn(
                      "border-b border-border hover:bg-surface-1 transition-colors cursor-pointer",
                      selected.has(d.id) && "bg-cordum/5",
                      expandedRow === d.id && "bg-surface-1"
                    )}
                    {...clickableRowProps(() => setExpandedRow(expandedRow === d.id ? null : d.id))}
                  >
                    <td className="w-10 px-3 py-3" onClick={(e) => e.stopPropagation()}>
                      <input
                        type="checkbox"
                        checked={selected.has(d.id)}
                        onChange={() => toggleOne(d.id)}
                        className="w-3.5 h-3.5 rounded border-border bg-surface-0 text-cordum focus:ring-cordum accent-[oklch(0.82_0.18_165)]"
                      />
                    </td>
                    <td className="px-4 py-3 font-mono text-sm text-foreground">{(d.jobId ?? d.id ?? "").slice(0, 16)}</td>
                    <td className="px-4 py-3 text-sm text-foreground">{d.originalTopic ?? "—"}</td>
                    <td className="px-4 py-3">
                      <span className="text-xs text-destructive truncate max-w-[250px] block font-mono">{d.error ?? "—"}</span>
                    </td>
                    <td className="px-4 py-3 text-center font-mono text-xs text-muted-foreground">{d.attempts ?? d.retryCount ?? 0}</td>
                    <td className="px-4 py-3 text-right text-xs text-muted-foreground font-mono">{formatRelativeTime(d.failedAt ?? d.createdAt ?? "")}</td>
                    <td className="px-4 py-3" onClick={(e) => e.stopPropagation()}>
                      <div className="flex gap-1 justify-end">
                        <button
                          onClick={() => retryMutation.mutate({ id: d.id })}
                          disabled={retryMutation.isPending || purgeMutation.isPending}
                          className="p-1.5 rounded hover:bg-surface-2 transition-colors text-cordum disabled:opacity-50 disabled:pointer-events-none"
                          title="Retry"
                        >
                          <Play className="w-3.5 h-3.5" />
                        </button>
                        <button
                          onClick={() => purgeMutation.mutate(d.id)}
                          disabled={purgeMutation.isPending || retryMutation.isPending}
                          className="p-1.5 rounded hover:bg-surface-2 transition-colors text-destructive disabled:opacity-50 disabled:pointer-events-none"
                          title="Purge"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      </div>
                    </td>
                  </tr>
                  {/* Expanded row — payload preview */}
                  <AnimatePresence>
                    {expandedRow === d.id && (
                      <tr key={`${d.id}-expand`}>
                        <td colSpan={7} className="px-0 py-0">
                          <motion.div
                            initial={{ height: 0, opacity: 0 }}
                            animate={{ height: "auto", opacity: 1 }}
                            exit={{ height: 0, opacity: 0 }}
                            transition={{ duration: 0.2 }}
                            className="overflow-hidden"
                          >
                            <div className="px-12 py-4 bg-surface-0/50 border-b border-border space-y-3">
                              <div>
                                <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-2">Entry Details</p>
                                <pre className="text-xs font-mono text-foreground bg-surface-0 border border-border rounded-2xl p-3 max-h-40 overflow-auto">
                                  {JSON.stringify(buildDLQEntryDetails(d), null, 2)}
                                </pre>
                              </div>
                              <div>
                                <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-1">Full Error</p>
                                <p className="text-xs font-mono text-destructive">{resolveDLQError(d)}</p>
                              </div>
                            </div>
                          </motion.div>
                        </td>
                      </tr>
                    )}
                  </AnimatePresence>
                </Fragment>
              ))}
            </tbody>
          </table>
          </div>
        </motion.div>
      )}

      {/* Floating bulk action bar */}
      <AnimatePresence>
        {selected.size > 0 && (
          <motion.div
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: 20 }}
            transition={{ duration: 0.2 }}
            className="fixed bottom-6 left-1/2 -translate-x-1/2 z-50"
          >
            <div className="flex items-center gap-3 px-5 py-3 bg-surface-1 border border-border rounded-xl shadow-2xl">
              <span className="text-xs font-mono text-foreground">
                <span className="font-bold text-cordum">{selected.size}</span> selected
              </span>
              <div className="w-px h-5 bg-border" />
              <button
                onClick={() => setConfirmBulk("retry")}
                disabled={bulkRetryMutation.isPending || bulkPurgeMutation.isPending}
                className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-full bg-cordum/10 text-cordum hover:bg-cordum/20 transition-colors disabled:opacity-50 disabled:pointer-events-none"
              >
                <Play className="w-3 h-3" />
                Retry All
              </button>
              <button
                onClick={() => setConfirmBulk("purge")}
                disabled={bulkRetryMutation.isPending || bulkPurgeMutation.isPending}
                className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-full bg-destructive/10 text-destructive hover:bg-destructive/20 transition-colors disabled:opacity-50 disabled:pointer-events-none"
              >
                <Trash2 className="w-3 h-3" />
                Purge All
              </button>
              <button
                onClick={() => setSelected(new Set())}
                className="p-1.5 rounded-full hover:bg-surface-2 text-muted-foreground transition-colors"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* Bulk confirm dialogs */}
      <ConfirmDialog
        open={confirmBulk === "retry"}
        onClose={() => setConfirmBulk(null)}
        onConfirm={() => bulkRetryMutation.mutate([...selected], { onSuccess: () => { setSelected(new Set()); setConfirmBulk(null); } })}
        title={`Retry ${selected.size} items?`}
        description={`This will re-queue ${selected.size} dead letter items for processing.`}
        confirmLabel="Retry All"
        loading={bulkRetryMutation.isPending}
      />
      <ConfirmDialog
        open={confirmBulk === "purge"}
        onClose={() => setConfirmBulk(null)}
        onConfirm={() => bulkPurgeMutation.mutate([...selected], { onSuccess: () => { setSelected(new Set()); setConfirmBulk(null); } })}
        title={`Purge ${selected.size} items?`}
        description="This will permanently delete the selected items. This action cannot be undone."
        confirmLabel="Purge All"
        variant="destructive"
        loading={bulkPurgeMutation.isPending}
      />
    </div>
  );
}
