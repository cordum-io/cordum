/*
 * DESIGN: "Control Surface" — Quarantine Queue v2
 * Spec: Quarantined outputs with findings detail, redaction preview, bulk actions
 */
import { useState, useMemo } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { EmptyState } from "@/components/ui/EmptyState";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import {
  ShieldAlert, ChevronDown, ChevronRight, Eye, CheckCircle2,
  XCircle, Clock, FileText, Search,
} from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import { toast } from "sonner";
import {
  useQuarantinedJobs,
  useReleaseQuarantinedJob,
  useConfirmQuarantine,
  useOutputPolicyStats,
} from "@/hooks/useOutputPolicy";

type SeverityFilter = "all" | "high" | "medium" | "low";

export default function QuarantineQueuePage() {
  const { data: quarantinedData, isLoading } = useQuarantinedJobs({});
  const { data: statsData } = useOutputPolicyStats();
  const releaseMutation = useReleaseQuarantinedJob();
  const confirmMutation = useConfirmQuarantine();

  // useQuarantinedJobs returns ApiResponse<Job[]> which has { items: Job[] }
  const items = quarantinedData?.items ?? [];
  // OutputPolicyStats has: totalChecks24h, quarantined24h, avgLatencyMs, lastCheckAt
  const stats = statsData ?? { totalChecks24h: 0, quarantined24h: 0, avgLatencyMs: 0 };

  const [search, setSearch] = useState("");
  const [severityFilter, setSeverityFilter] = useState<SeverityFilter>("all");
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [showBulkRelease, setShowBulkRelease] = useState(false);
  const [showBulkDiscard, setShowBulkDiscard] = useState(false);

  const filtered = useMemo(() => {
    let result = items;
    if (severityFilter !== "all") {
      result = result.filter((item) => {
        // Infer severity from output_safety findings
        const findings = item.output_safety?.findings ?? [];
        const maxSev = findings.reduce((acc: string, f) => {
          if (f.severity === "critical" || f.severity === "high") return "high";
          if (f.severity === "medium" && acc !== "high") return "medium";
          return acc;
        }, "low");
        return maxSev === severityFilter;
      });
    }
    if (search.trim()) {
      const q = search.toLowerCase();
      result = result.filter((item) =>
        item.id.toLowerCase().includes(q) ||
        (item.output_safety?.reason ?? "").toLowerCase().includes(q),
      );
    }
    return result;
  }, [items, severityFilter, search]);

  const toggleSelect = (id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  };

  const toggleSelectAll = () => {
    if (selectedIds.size === filtered.length) setSelectedIds(new Set());
    else setSelectedIds(new Set(filtered.map((i) => i.id)));
  };

  const handleRelease = (jobId: string) => {
    releaseMutation.mutate(jobId, {
      onSuccess: () => toast.success("Released from quarantine"),
      onError: (err: Error) => toast.error("Release failed", { description: err.message }),
    });
  };

  const handleConfirm = (jobId: string) => {
    confirmMutation.mutate(jobId, {
      onSuccess: () => toast.success("Quarantine confirmed — output discarded"),
      onError: (err: Error) => toast.error("Confirm failed", { description: err.message }),
    });
  };

  return (
    <>
      <div className="space-y-6">
        <PageHeader label="Safety" title="Quarantine Queue" subtitle={`${items.length} quarantined output${items.length !== 1 ? "s" : ""}`} />
        {/* KPIs */}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          {[
            { label: "Quarantined (24h)", value: stats.quarantined24h, icon: ShieldAlert, color: "text-red-400" },
            { label: "Total Checks (24h)", value: stats.totalChecks24h, icon: Eye, color: "text-blue-400" },
            { label: "Avg Latency", value: `${stats.avgLatencyMs}ms`, icon: Clock, color: "text-amber-400" },
            { label: "Queue Size", value: items.length, icon: FileText, color: "text-emerald-400" },
          ].map((kpi, i) => {
            const Icon = kpi.icon;
            return (
              <motion.div key={kpi.label} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: i * 0.05 }}
                className="instrument-card p-5">
                <div className="flex items-center justify-between mb-2">
                  <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">{kpi.label}</span>
                  <Icon className={cn("w-4 h-4", kpi.color)} />
                </div>
                <span className="text-2xl font-mono font-bold text-foreground">{kpi.value}</span>
              </motion.div>
            );
          })}
        </div>

        {/* Toolbar */}
        <div className="flex items-center justify-between flex-wrap gap-3">
          <div className="flex items-center gap-3">
            <div className="flex items-center gap-1 bg-surface-1 rounded-lg p-0.5 border border-border">
              {(["all", "high", "medium", "low"] as SeverityFilter[]).map((s) => (
                <button key={s} onClick={() => setSeverityFilter(s)}
                  className={cn("px-3 py-1.5 text-xs font-mono rounded-md transition-all capitalize",
                    severityFilter === s ? "bg-cordum/15 text-cordum font-medium" : "text-muted-foreground hover:text-foreground")}>
                  {s}
                </button>
              ))}
            </div>
            <div className="relative">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
              <input type="text" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search quarantine…"
                className="h-8 w-56 pl-8 pr-3 text-xs font-mono bg-surface-1 border border-border rounded-md text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum" />
            </div>
          </div>
          {selectedIds.size > 0 && (
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground">{selectedIds.size} selected</span>
              <Button variant="outline" size="sm" onClick={() => setShowBulkRelease(true)}>
                <CheckCircle2 className="w-3 h-3 mr-1" />Release
              </Button>
              <Button variant="danger" size="sm" onClick={() => setShowBulkDiscard(true)}>
                <XCircle className="w-3 h-3 mr-1" />Discard
              </Button>
            </div>
          )}
        </div>

        {/* Queue */}
        {isLoading ? (
          <div className="space-y-3">{[1, 2, 3].map((i) => <SkeletonCard key={i} />)}</div>
        ) : filtered.length === 0 ? (
          <EmptyState icon={<ShieldAlert className="w-8 h-8" />} title="No quarantined items" description="Outputs flagged by output policy scanners will appear here." />
        ) : (
          <div className="instrument-card p-0 overflow-hidden">
            <div className="px-4 py-3 bg-surface-0 border-b border-border flex items-center justify-between">
              <div className="flex items-center gap-3">
                <input type="checkbox" checked={selectedIds.size === filtered.length && filtered.length > 0}
                  onChange={toggleSelectAll} className="rounded border-border" />
                <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">
                  {filtered.length} item{filtered.length !== 1 ? "s" : ""}
                </p>
              </div>
            </div>
            <div className="divide-y divide-border">
              {filtered.map((item) => {
                const isExpanded = expandedId === item.id;
                const findings = item.output_safety?.findings ?? [];
                const severity = findings.some((f) => f.severity === "critical" || f.severity === "high") ? "high" : findings.some((f) => f.severity === "medium") ? "medium" : "low";
                return (
                  <div key={item.id}>
                    <div className="flex items-center gap-3 px-4 py-3 hover:bg-surface-1/50 transition-colors">
                      <input type="checkbox" checked={selectedIds.has(item.id)}
                        onChange={() => toggleSelect(item.id)} className="rounded border-border shrink-0" />
                      <button onClick={() => setExpandedId(isExpanded ? null : item.id)} className="flex items-center gap-3 flex-1 text-left">
                        <div className="w-4 h-4 flex items-center justify-center shrink-0">
                          {isExpanded ? <ChevronDown className="w-3.5 h-3.5 text-muted-foreground" /> : <ChevronRight className="w-3.5 h-3.5 text-muted-foreground" />}
                        </div>
                        <div className={cn("w-7 h-7 rounded-lg flex items-center justify-center shrink-0",
                          severity === "high" ? "bg-red-400/15 text-red-400" :
                          severity === "medium" ? "bg-amber-400/15 text-amber-400" :
                          "bg-blue-400/15 text-blue-400")}>
                          <ShieldAlert className="w-3.5 h-3.5" />
                        </div>
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="text-sm font-mono text-foreground">{item.id.slice(0, 16)}…</span>
                            <StatusBadge variant={severity === "high" ? "danger" : severity === "medium" ? "warning" : "info"}>{severity}</StatusBadge>
                          </div>
                          <p className="text-xs text-muted-foreground mt-0.5 truncate">{item.output_safety?.reason ?? "Flagged by output scanner"}</p>
                        </div>
                        <span className="text-[10px] font-mono text-muted-foreground flex items-center gap-1 shrink-0">
                          <Clock className="w-3 h-3" />{formatRelativeTime(item.updatedAt)}
                        </span>
                      </button>
                      <div className="flex items-center gap-1 shrink-0">
                        <Button variant="ghost" size="sm" onClick={() => handleRelease(item.id)} disabled={releaseMutation.isPending}>
                          <CheckCircle2 className="w-3 h-3" />
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => handleConfirm(item.id)} disabled={confirmMutation.isPending}>
                          <XCircle className="w-3 h-3 text-red-400" />
                        </Button>
                      </div>
                    </div>
                    <AnimatePresence>
                      {isExpanded && (
                        <motion.div initial={{ height: 0, opacity: 0 }} animate={{ height: "auto", opacity: 1 }} exit={{ height: 0, opacity: 0 }} transition={{ duration: 0.2 }}>
                          <div className="px-4 pb-4 pl-16 space-y-3">
                            {/* Output preview */}
                            <div className="bg-surface-0 rounded-lg border border-border overflow-hidden">
                              <div className="px-3 py-2 border-b border-border bg-surface-1/50">
                                <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Output Content</p>
                              </div>
                              <div className="p-3">
                                <pre className="text-xs font-mono text-foreground whitespace-pre-wrap max-h-40 overflow-y-auto">
                                  {item.resultPtr ?? "Output content not available in this view"}
                                </pre>
                              </div>
                            </div>
                            {/* Findings */}
                            {findings.length > 0 && (
                              <div className="bg-surface-0 rounded-lg border border-border overflow-hidden">
                                <div className="px-3 py-2 border-b border-border bg-surface-1/50">
                                  <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Findings</p>
                                </div>
                                <div className="divide-y divide-border">
                                  {findings.map((finding, fi) => (
                                    <div key={fi} className="flex items-center gap-3 px-3 py-2">
                                      <StatusBadge variant={finding.severity === "high" || finding.severity === "critical" ? "danger" : finding.severity === "medium" ? "warning" : "info"}>
                                        {finding.type}
                                      </StatusBadge>
                                      <span className="text-xs text-foreground flex-1">{finding.detail}</span>
                                      {finding.confidence != null && <span className="text-[10px] font-mono text-muted-foreground">confidence: {finding.confidence}</span>}
                                    </div>
                                  ))}
                                </div>
                              </div>
                            )}
                            {/* Metadata */}
                            <div className="flex items-center gap-4 text-[10px] font-mono text-muted-foreground">
                              <span>Job: {item.id}</span>
                              <span>Updated: {new Date(item.updatedAt).toLocaleString()}</span>
                              {item.output_safety?.rule_id && <span>Rule: {item.output_safety.rule_id}</span>}
                            </div>
                          </div>
                        </motion.div>
                      )}
                    </AnimatePresence>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </div>

      <ConfirmDialog open={showBulkRelease} onClose={() => setShowBulkRelease(false)}
        title="Release Selected Items" description={`Release ${selectedIds.size} item(s) from quarantine? Their outputs will be delivered.`}
        onConfirm={() => { selectedIds.forEach((id) => releaseMutation.mutate(id)); setSelectedIds(new Set()); setShowBulkRelease(false); }}
        confirmLabel="Release All" variant="default" />
      <ConfirmDialog open={showBulkDiscard} onClose={() => setShowBulkDiscard(false)}
        title="Discard Selected Items" description={`Permanently discard ${selectedIds.size} quarantined output(s)? This cannot be undone.`}
        onConfirm={() => { selectedIds.forEach((id) => confirmMutation.mutate(id)); setSelectedIds(new Set()); setShowBulkDiscard(false); }}
        confirmLabel="Discard All" variant="destructive" />
    </>
  );
}
