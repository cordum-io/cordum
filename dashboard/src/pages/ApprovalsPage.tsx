import { useCallback, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { CheckCircle, Clock, History } from "lucide-react";
import {
  useApprovals,
  useApprovalHistory,
  useApproveJob,
  useRejectJob,
} from "../hooks/useApprovals";
import { Badge } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { ApprovalDetailPanel } from "../components/approvals/ApprovalDetailPanel";
import { ApprovalHistory } from "../components/approvals/ApprovalHistory";
import { ApprovalQueueFilters, applyFilters } from "../components/approvals/ApprovalQueueFilters";
import type { FilterState } from "../components/approvals/ApprovalQueueFilters";
import { BulkActionBar } from "../components/approvals/BulkActionBar";
import { cn } from "../lib/utils";
import { useEventStore } from "../state/events";
import { useConfigStore } from "../state/config";
import type { Approval, UrgencyLevel } from "../api/types";
import { DataFreshness } from "../components/ui/DataFreshness";
import { usePageTitle } from "../hooks/usePageTitle";
import { RequireRole } from "../components/RequireRole";

type ApprovalsTab = "queue" | "history";

function formatWait(ms: number): string {
  const secs = Math.floor(ms / 1_000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  return `${hrs}h ${mins % 60}m`;
}

type Urgency = "default" | "warning" | "danger";

function urgencyToVariant(level?: UrgencyLevel): Urgency {
  if (level === "critical" || level === "breach") return "danger";
  if (level === "aging") return "warning";
  return "default";
}

const urgencyLabel: Record<Urgency, string> = { default: "Normal", warning: "Aging", danger: "Critical" };
const urgencyDotColor: Record<Urgency, string> = { default: "bg-emerald-500", warning: "bg-yellow-500", danger: "bg-red-500" };

function StatsStrip({ approvals, selectedCount, totalCount, onSelectAll }: { approvals: Approval[]; resolvedToday: number; selectedCount: number; totalCount: number; onSelectAll: () => void }) {
  const pending = approvals.length;
  const critical = approvals.filter((a) => a.urgencyLevel === "critical" || a.urgencyLevel === "breach").length;
  const slaMs = useConfigStore((s) => s.approvalSlaMs);
  const slaBreaches = approvals.filter((a) => (a.waitMs ?? 0) > slaMs).length;
  const avgWait = pending > 0 ? Math.round(approvals.reduce((sum, a) => sum + (a.waitMs ?? 0), 0) / pending) : 0;
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
      {totalCount > 0 && (<><input type="checkbox" checked={selectedCount > 0 && selectedCount === totalCount} ref={(el) => { if (el) el.indeterminate = selectedCount > 0 && selectedCount < totalCount; }} onChange={onSelectAll} className="h-3.5 w-3.5 rounded border-border text-accent focus:ring-accent cursor-pointer" title="Select all" /><span aria-hidden>&middot;</span></>)}
      <span><span className="font-semibold text-ink">{pending}</span> pending</span>
      <span aria-hidden>&middot;</span>
      <span><span className={cn("font-semibold", critical > 0 ? "text-danger" : "text-ink")}>{critical}</span> critical</span>
      <span aria-hidden>&middot;</span>
      <span>avg wait <span className="font-semibold text-ink">{formatWait(avgWait)}</span></span>
      {slaBreaches > 0 && (<><span aria-hidden>&middot;</span><span><span className="font-semibold text-danger">{slaBreaches}</span> SLA breach{slaBreaches !== 1 ? "es" : ""}</span></>)}
    </div>
  );
}

function UrgencyBadge({ urgency }: { urgency: Urgency }) {
  return (<Badge variant={urgency} className={cn(urgency === "danger" && "animate-pulse")}>{urgencyLabel[urgency]}</Badge>);
}

function ApprovalCard({ approval, onClick, selected, onToggleSelect }: { approval: Approval; onClick: () => void; selected?: boolean; onToggleSelect?: (id: string) => void }) {
  const urgency = urgencyToVariant(approval.urgencyLevel);
  return (
    <Card className={cn("cursor-pointer transition-shadow hover:shadow-lift", urgency === "danger" && "border-l-4 border-l-danger", urgency === "warning" && "border-l-4 border-l-warning", selected && "ring-2 ring-accent/40")} onClick={onClick}>
      <div className="flex gap-3">
        {onToggleSelect && (<div className="flex items-start pt-0.5"><input type="checkbox" checked={selected ?? false} onChange={() => onToggleSelect(approval.id)} onClick={(e) => e.stopPropagation()} className="h-4 w-4 rounded border-border text-accent focus:ring-accent cursor-pointer" /></div>)}
        <div className="min-w-0 flex-1 space-y-3">
          <div className="flex items-center justify-between">
            <UrgencyBadge urgency={urgency} />
            <span className="text-xs font-mono text-muted">Waiting {formatWait(approval.waitMs ?? 0)}</span>
          </div>
          <div>
            <p className="text-sm font-semibold text-ink">
              {approval.humanSummary || (<>Job{" "}<Link to={`/jobs/${approval.jobId}`} className="font-mono text-accent hover:underline" onClick={(e) => e.stopPropagation()}>{approval.jobId.slice(0, 8)}</Link>{" "}requires approval</>)}
            </p>
            {approval.reason && <p className="mt-1 text-xs text-muted">{approval.reason}</p>}
          </div>
          {approval.policyRule && (<div className="text-xs"><span className="text-muted">Policy rule: </span><span className="font-medium text-ink">{approval.policyRule}</span></div>)}
          {((approval.capabilities?.length ?? 0) > 0 || (approval.riskTags?.length ?? 0) > 0) && (
            <div className="flex flex-wrap gap-2">
              {approval.capabilities?.map((c) => (<Badge key={c} variant="info">{c}</Badge>))}
              {approval.riskTags?.map((t) => (<Badge key={t} variant="warning">{t}</Badge>))}
            </div>
          )}
        </div>
      </div>
    </Card>
  );
}

function MiniCard({ approval, active, onClick }: { approval: Approval; active: boolean; onClick: () => void }) {
  const urgency = urgencyToVariant(approval.urgencyLevel);
  return (
    <button type="button" onClick={onClick} className={cn("w-full text-left rounded-xl border px-3 py-2.5 transition-colors", active ? "border-accent bg-accent/5" : "border-border bg-surface hover:bg-surface2/50")}>
      <div className="flex items-center gap-2">
        <span className={cn("h-2.5 w-2.5 shrink-0 rounded-full", urgencyDotColor[urgency])} />
        <p className="min-w-0 flex-1 truncate text-xs font-medium text-ink">{approval.humanSummary || `Job ${approval.jobId.slice(0, 8)}`}</p>
        <span className="shrink-0 font-mono text-[10px] text-muted">{formatWait(approval.waitMs ?? 0)}</span>
      </div>
    </button>
  );
}

export default function ApprovalsPage() {
  usePageTitle("Approvals");
  const { data, isLoading, isError, dataUpdatedAt, refetch, isRefetching } = useApprovals();
  const { data: historyData } = useApprovalHistory();
  const approveJob = useApproveJob();
  const rejectJob = useRejectJob();
  const [searchParams, setSearchParams] = useSearchParams();
  const activeTab: ApprovalsTab = (searchParams.get("tab") as ApprovalsTab) || "queue";
  const setActiveTab = useCallback(
    (tab: ApprovalsTab) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        if (tab === "queue") next.delete("tab");
        else next.set("tab", tab);
        // Clear other tab's params
        next.delete("page");
        return next;
      }, { replace: true });
    },
    [setSearchParams],
  );
  const approvals = data?.items ?? [];
  const resolvedToday = historyData?.items?.length ?? 0;

  const filters = useMemo<FilterState>(() => ({
    urgency: (searchParams.get("urgency") as FilterState["urgency"]) || "all",
    workflow: searchParams.get("workflow") || "",
    rule: searchParams.get("rule") || "",
    risk: (searchParams.get("risk") as FilterState["risk"]) || "all",
    sortBy: (searchParams.get("sortBy") as FilterState["sortBy"]) || "waitTime",
    assignment: (searchParams.get("assignment") as FilterState["assignment"]) || "all",
  }), [searchParams]);

  const setFilters = useCallback((next: FilterState) => {
    const params: Record<string, string> = {};
    const currentTab = searchParams.get("tab");
    if (currentTab) params.tab = currentTab;
    const currentId = searchParams.get("id");
    if (currentId) params.id = currentId;
    if (next.urgency !== "all") params.urgency = next.urgency;
    if (next.workflow) params.workflow = next.workflow;
    if (next.rule) params.rule = next.rule;
    if (next.risk !== "all") params.risk = next.risk;
    if (next.sortBy !== "waitTime") params.sortBy = next.sortBy;
    if (next.assignment !== "all") params.assignment = next.assignment;
    setSearchParams(params);
  }, [searchParams, setSearchParams]);

  // Subscribe to assignment count as a lightweight change signal —
  // avoids re-rendering the full page on every individual assignment update.
  const assignmentVersion = useEventStore((s) => s.approvalAssignments.size);
  const sorted = useMemo(() => applyFilters(approvals, filters), [approvals, filters, assignmentVersion]);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const toggleSelect = useCallback((id: string) => { setSelectedIds((prev) => { const next = new Set(prev); if (next.has(id)) next.delete(id); else next.add(id); return next; }); }, []);
  const selectAll = useCallback(() => { setSelectedIds((prev) => prev.size === sorted.length ? new Set() : new Set(sorted.map((a) => a.id))); }, [sorted]);
  const clearSelection = useCallback(() => setSelectedIds(new Set()), []);

  const selectedId = searchParams.get("id");
  const selectedApproval = useMemo(() => sorted.find((a) => a.id === selectedId) ?? null, [sorted, selectedId]);
  const openPanel = useCallback((id: string) => { const params: Record<string, string> = { id }; const t = searchParams.get("tab"); if (t) params.tab = t; if (filters.urgency !== "all") params.urgency = filters.urgency; if (filters.workflow) params.workflow = filters.workflow; if (filters.rule) params.rule = filters.rule; if (filters.risk !== "all") params.risk = filters.risk; if (filters.sortBy !== "waitTime") params.sortBy = filters.sortBy; if (filters.assignment !== "all") params.assignment = filters.assignment; setSearchParams(params); }, [filters, searchParams, setSearchParams]);
  const closePanel = useCallback(() => { const params: Record<string, string> = {}; const t = searchParams.get("tab"); if (t) params.tab = t; if (filters.urgency !== "all") params.urgency = filters.urgency; if (filters.workflow) params.workflow = filters.workflow; if (filters.rule) params.rule = filters.rule; if (filters.risk !== "all") params.risk = filters.risk; if (filters.sortBy !== "waitTime") params.sortBy = filters.sortBy; if (filters.assignment !== "all") params.assignment = filters.assignment; setSearchParams(params); }, [filters, searchParams, setSearchParams]);
  const panelOpen = !!selectedApproval;

  const handleApprove = useCallback((id: string, comment?: string) => approveJob.mutateAsync({ id, comment }), [approveJob]);
  const handleReject = useCallback((id: string, reason: string) => rejectJob.mutateAsync({ id, reason }), [rejectJob]);

  return (
    <div className="space-y-4 pb-20">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="font-display text-2xl font-bold text-ink">Approvals</h1>
          <DataFreshness dataUpdatedAt={dataUpdatedAt} onRefresh={refetch} isRefetching={isRefetching} />
        </div>
        {sorted.length > 0 && <Badge variant="warning">{sorted.length} pending</Badge>}
      </div>
      <StatsStrip approvals={approvals} resolvedToday={resolvedToday} selectedCount={selectedIds.size} totalCount={sorted.length} onSelectAll={selectAll} />
      <div className="flex gap-1 rounded-full border border-border p-1 w-fit" role="tablist" aria-label="Approval views">
        <button type="button" role="tab" aria-selected={activeTab === "queue"} aria-controls="tabpanel-queue" id="tab-queue" className={cn("flex items-center gap-2 rounded-full px-5 py-2 text-xs font-semibold uppercase tracking-widest transition", activeTab === "queue" ? "bg-accent/15 text-accent" : "text-muted hover:text-ink")} onClick={() => setActiveTab("queue")}>
          <Clock className="h-3.5 w-3.5" />Queue{sorted.length > 0 ? ` (${sorted.length})` : ""}
        </button>
        <button type="button" role="tab" aria-selected={activeTab === "history"} aria-controls="tabpanel-history" id="tab-history" className={cn("flex items-center gap-2 rounded-full px-5 py-2 text-xs font-semibold uppercase tracking-widest transition", activeTab === "history" ? "bg-accent/15 text-accent" : "text-muted hover:text-ink")} onClick={() => setActiveTab("history")}>
          <History className="h-3.5 w-3.5" />History
        </button>
      </div>
      {activeTab === "queue" && (
        <div id="tabpanel-queue" role="tabpanel" aria-labelledby="tab-queue">
          {!isLoading && approvals.length > 0 && <ApprovalQueueFilters approvals={approvals} filters={filters} onFiltersChange={setFilters} />}
          {isLoading && (<div className="space-y-3">{Array.from({ length: 4 }, (_, i) => (<Card key={i} className="animate-pulse"><div className="space-y-3"><div className="h-5 w-1/3 rounded bg-surface2" /><div className="h-4 w-2/3 rounded bg-surface2" /><div className="h-4 w-1/2 rounded bg-surface2" /></div></Card>))}</div>)}
          {!isLoading && isError && (<Card><p className="py-8 text-center text-muted">Failed to load approvals. Please try again.</p></Card>)}
          {!isLoading && !isError && sorted.length === 0 && (<div className="py-16 text-center"><CheckCircle className="mx-auto mb-3 h-10 w-10 text-success opacity-60" /><p className="text-sm font-semibold text-ink">All clear — no pending approvals</p><p className="mt-1 text-xs text-muted">Nothing needs your attention right now.</p><Button variant="ghost" size="sm" className="mt-4" onClick={() => setActiveTab("history")}>View History</Button></div>)}
          {!isLoading && !isError && sorted.length > 0 && (
            panelOpen ? (
              <div className="hidden md:block space-y-1.5">{sorted.map((approval) => (<MiniCard key={approval.id} approval={approval} active={approval.id === selectedId} onClick={() => openPanel(approval.id)} />))}</div>
            ) : (
              <div className="space-y-3">{sorted.map((approval: Approval) => (<ApprovalCard key={approval.id} approval={approval} onClick={() => openPanel(approval.id)} selected={selectedIds.has(approval.id)} onToggleSelect={toggleSelect} />))}</div>
            )
          )}
          {panelOpen && selectedApproval && (<ApprovalDetailPanel approval={selectedApproval} allApprovals={approvals} onClose={closePanel} onApprove={handleApprove} onReject={handleReject} />)}
          {selectedIds.size > 0 && (<RequireRole roles={["admin", "operator"]}><BulkActionBar selectedIds={selectedIds} approvals={sorted} onApprove={handleApprove} onReject={handleReject} onClear={clearSelection} onDone={clearSelection} /></RequireRole>)}
        </div>
      )}
      {activeTab === "history" && <div id="tabpanel-history" role="tabpanel" aria-labelledby="tab-history"><ApprovalHistory /></div>}
    </div>
  );
}
