/*
 * DESIGN: "Control Surface" — Audit Log
 * Matches cordumds-gj5mw4zm.manus.space showcase patterns
 */
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get } from "@/api/client";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { Search, RefreshCw, FileText, Download } from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import { toast } from "sonner";

interface AuditEvent {
  id: string;
  action: string;
  actor: string;
  resource: string;
  resourceId?: string;
  detail?: string;
  timestamp: string;
  ip?: string;
}

function actionColor(action: string) {
  if (action.includes("created") || action.includes("registered")) return "text-[var(--color-success)] bg-[var(--color-success)]/10 border-[var(--color-success)]/20";
  if (action.includes("failed") || action.includes("deleted")) return "text-destructive bg-destructive/10 border-destructive/20";
  if (action.includes("updated") || action.includes("decided")) return "text-[var(--color-warning)] bg-[var(--color-warning)]/10 border-[var(--color-warning)]/20";
  return "text-cordum bg-cordum/10 border-cordum/20";
}

export default function AuditLogPage() {
  const [search, setSearch] = useState("");
  const [actionFilter, setActionFilter] = useState("");

  const { data, isLoading, refetch } = useQuery({
    queryKey: ["audit", actionFilter],
    queryFn: async () => {
      const params = new URLSearchParams({ limit: "200" });
      if (actionFilter) params.set("action", actionFilter);
      const res = await get<{ items: Record<string, unknown>[] }>(`/policy/audit?${params}`);
      return (res.items ?? []).map((e): AuditEvent => ({
        id: (e.id as string) ?? "",
        action: (e.action as string) ?? "",
        actor: (e.actor_id as string) || (e.role as string) || (e.actor as string) || "unknown",
        resource: (e.resource_type as string) || (e.resource as string) || "",
        resourceId: (e.resource_id as string) || (e.resourceId as string) || undefined,
        detail: (e.message as string) || (e.detail as string) || undefined,
        timestamp: (e.created_at as string) || (e.timestamp as string) || "",
      }));
    },
  });

  const events = (data ?? []).filter((e) => {
    if (!search) return true;
    const q = search.toLowerCase();
    return e.action.toLowerCase().includes(q) || e.actor.toLowerCase().includes(q) || e.resource.toLowerCase().includes(q) || (e.detail ?? "").toLowerCase().includes(q);
  });

  return (
    <div className="space-y-6">
      <PageHeader
        label="Platform"
        title="Audit Log"
        subtitle="System-wide activity trail"
        actions={
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              <RefreshCw className="w-3 h-3 mr-1" />
              Refresh
            </Button>
            <Button variant="outline" size="sm" onClick={() => {
              const rows = events.map((e) => [e.timestamp, e.action, e.actor, e.resource, e.resourceId ?? "", e.detail ?? ""].join(","));
              const csv = ["timestamp,action,actor,resource,resourceId,detail", ...rows].join("\n");
              const blob = new Blob([csv], { type: "text/csv" });
              const url = URL.createObjectURL(blob);
              const a = document.createElement("a");
              a.href = url;
              a.download = `audit-export-${new Date().toISOString().slice(0, 10)}.csv`;
              a.click();
              URL.revokeObjectURL(url);
              toast.success(`Exported ${events.length} events`);
            }}>
              <Download className="w-3 h-3 mr-1" />
              Export CSV
            </Button>
          </div>
        }
      />

      {/* Filters — showcase style */}
      <div className="flex items-center gap-3 flex-wrap">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search events..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-8 w-full pl-8 pr-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
          />
        </div>
        <select
          value={actionFilter}
          onChange={(e) => setActionFilter(e.target.value)}
          className="h-8 px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
        >
          <option value="">All Actions</option>
          <option value="job.created">Job Created</option>
          <option value="job.completed">Job Completed</option>
          <option value="job.failed">Job Failed</option>
          <option value="approval.decided">Approval Decided</option>
          <option value="policy.updated">Policy Updated</option>
          <option value="worker.registered">Worker Registered</option>
        </select>
      </div>

      {/* Table — showcase style */}
      {isLoading ? (
        <div className="instrument-card">
          <SkeletonTable rows={10} />
        </div>
      ) : events.length === 0 ? (
        <EmptyState icon={<FileText className="w-5 h-5" />} title="No audit events" description="Events will appear as actions occur in the system" />
      ) : (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3 }}
          className="instrument-card overflow-hidden"
        >
          <div className="overflow-x-auto">
          <table className="w-full min-w-[700px]">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th className="text-left px-5 py-3 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest">Time</th>
                <th className="text-left px-5 py-3 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest">Action</th>
                <th className="text-left px-5 py-3 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest">Actor</th>
                <th className="text-left px-5 py-3 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest">Resource</th>
                <th className="text-left px-5 py-3 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest">Detail</th>
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <tr key={e.id} className="border-b border-border hover:bg-surface-1 transition-colors">
                  <td className="px-5 py-3 font-mono text-xs text-muted-foreground whitespace-nowrap">{formatRelativeTime(e.timestamp)}</td>
                  <td className="px-5 py-3">
                    <span className={cn("text-xs font-mono px-2 py-0.5 rounded-full border", actionColor(e.action))}>
                      {e.action}
                    </span>
                  </td>
                  <td className="px-5 py-3 text-sm text-foreground">{e.actor}</td>
                  <td className="px-5 py-3">
                    <span className="text-sm text-foreground">{e.resource}</span>
                    {e.resourceId && <span className="text-xs text-muted-foreground font-mono ml-1">({e.resourceId.slice(0, 12)})</span>}
                  </td>
                  <td className="px-5 py-3 text-xs text-muted-foreground truncate max-w-[200px]">{e.detail ?? "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
        </motion.div>
      )}
    </div>
  );
}
