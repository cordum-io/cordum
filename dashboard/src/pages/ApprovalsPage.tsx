/*
 * DESIGN: "Decision Console" — Approvals
 * Primary hierarchy: what is being approved, why it matters, what happens next.
 * Secondary hierarchy: workflow/job audit metadata and raw payloads for drill-down.
 */
import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { motion, AnimatePresence } from "framer-motion";
import type { Approval } from "@/api/types";
import {
  useApprovals,
  useApproveJob,
  useRejectJob,
} from "@/hooks/useApprovals";
import { useDialogA11y } from "@/hooks/useDialogA11y";
import { PageHeader } from "@/components/layout/PageHeader";
import { McpApprovalsSection } from "@/components/approvals/McpApprovalsSection";
import { WorkflowContext } from "@/components/approvals/WorkflowContext";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import {
  Search,
  RefreshCw,
  UserCheck,
  CheckCircle2,
  XCircle,
  Clock,
  Timer,
  X,
  ArrowRight,
  Info,
} from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import { CodeBlock } from "@/components/ui/CodeBlock";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { MetricValue } from "@/components/ui/MetricValue";
import { friendlyError } from "@/lib/friendlyError";
import { toast } from "sonner";

interface ApprovalFact {
  label: string;
  value: string;
}

interface ApprovalAuditRow {
  label: string;
  value?: string;
}

function approvalStatusVariant(status: string): BadgeVariant {
  switch (status) {
    case "pending":
      return "warning";
    case "approved":
      return "healthy";
    case "rejected":
      return "governance";
    case "expired":
      return "muted";
    case "invalidated":
      return "danger";
    case "repaired":
      return "info";
    default:
      return "muted";
  }
}

function compactValue(value?: string, max = 16): string | undefined {
  if (!value) return undefined;
  const trimmed = value.trim();
  if (trimmed.length <= max) return trimmed;
  return `${trimmed.slice(0, max)}…`;
}

function formatApprovalStatusLabel(status: string): string {
  switch (status) {
    case "rejected":
      return "Denied";
    case "invalidated":
      return "Invalidated";
    case "repaired":
      return "Repaired";
    case "approved":
      return "Approved";
    case "expired":
      return "Expired";
    case "pending":
      return "Pending";
    default:
      return status.replace(/_/g, " ");
  }
}

function isApprovalActionable(approval: Approval): boolean {
  if (approval.actionability) {
    return approval.actionability === "actionable";
  }
  return approval.status === "pending";
}

function getApprovalLifecycleNote(approval: Approval): string | undefined {
  switch (approval.status) {
    case "approved":
      return "Decision recorded. Review the audit detail for who approved it and when.";
    case "rejected":
      return "Decision recorded. Review the denial reason and workflow impact before retrying the request.";
    case "expired":
      return "This approval timed out before a decision was recorded.";
    case "invalidated":
      return "This approval is no longer valid because the workflow or request changed after it was created.";
    case "repaired":
      return "This approval was repaired from an inconsistent legacy state. Review the audit trail before taking follow-up action.";
    default:
      return undefined;
  }
}

export function formatApprovalAmount(
  amount?: number,
  currency?: string,
): string | undefined {
  if (typeof amount !== "number" || !Number.isFinite(amount)) return undefined;
  if (!currency?.trim()) return amount.toLocaleString();
  try {
    return new Intl.NumberFormat(undefined, {
      style: "currency",
      currency: currency.trim().toUpperCase(),
      maximumFractionDigits: Number.isInteger(amount) ? 0 : 2,
    }).format(amount);
  } catch {
    return `${amount.toLocaleString()} ${currency.trim().toUpperCase()}`;
  }
}

export function getApprovalSourceMeta(approval: Approval): {
  label: string;
  variant: BadgeVariant;
} {
  if (
    approval.workflowContext ||
    approval.decisionSummary?.source?.startsWith("workflow")
  ) {
    return { label: "Workflow Gate", variant: "cordum" };
  }
  return { label: "Safety Policy", variant: "muted" };
}

export function getApprovalPrimaryTitle(approval: Approval): string {
  const title = approval.decisionSummary?.title?.trim();
  if (title) return title;
  if (approval.humanSummary?.trim()) return approval.humanSummary.trim();
  if (approval.workflowContext?.workflowName?.trim()) {
    return approval.workflowContext.workflowName.trim();
  }
  if (approval.workflowContext?.workflowId?.trim()) {
    const step =
      approval.workflowContext.stepName || approval.workflowContext.stepId;
    return step
      ? `${approval.workflowContext.workflowId} — ${step}`
      : approval.workflowContext.workflowId;
  }
  if (approval.topic?.trim()) return `Review ${approval.topic.trim()}`;
  return "Approval request";
}

export function getApprovalPrimaryReason(
  approval: Approval,
): string | undefined {
  const preferred = approval.decisionSummary?.why?.trim();
  if (preferred) return preferred;
  const fallback = approval.reason?.trim();
  return fallback || undefined;
}

export function getApprovalEscalationReason(
  approval: Approval,
): string | undefined {
  const escalation = approval.decisionSummary?.escalationReason?.trim();
  const reason = getApprovalPrimaryReason(approval);
  if (!escalation || escalation === reason) return undefined;
  return escalation;
}

export function getApprovalImpactText(approval: Approval): string {
  const nextEffect = approval.decisionSummary?.nextEffect?.trim();
  if (nextEffect) return nextEffect;
  if (approval.workflowContext?.stepName || approval.workflowContext?.stepId) {
    const step =
      approval.workflowContext.stepName || approval.workflowContext.stepId;
    return `Approve to continue ${step}.`;
  }
  if (approval.workflowContext?.workflowId) {
    return "Approve to continue the workflow.";
  }
  return "Approve to release the blocked job execution.";
}

export function getApprovalRejectImpactText(approval: Approval): string {
  if (approval.workflowContext?.workflowId) {
    return "Reject to stop this approval path and preserve the workflow audit trail.";
  }
  return "Reject to keep the job out of execution and record the denial.";
}

export function getApprovalFacts(approval: Approval): ApprovalFact[] {
  const facts: ApprovalFact[] = [];
  const amount = formatApprovalAmount(
    approval.decisionSummary?.amount,
    approval.decisionSummary?.currency,
  );
  if (amount) facts.push({ label: "Amount", value: amount });
  if (approval.decisionSummary?.vendor?.trim()) {
    facts.push({
      label: "Vendor",
      value: approval.decisionSummary.vendor.trim(),
    });
  }
  if (approval.decisionSummary?.itemCount) {
    facts.push({
      label: "Items",
      value: `${approval.decisionSummary.itemCount} item${approval.decisionSummary.itemCount === 1 ? "" : "s"}`,
    });
  } else if (approval.decisionSummary?.itemsPreview?.length) {
    facts.push({
      label: "Items",
      value: approval.decisionSummary.itemsPreview.slice(0, 2).join(", "),
    });
  }
  const step =
    approval.workflowContext?.stepName || approval.workflowContext?.stepId;
  if (step) facts.push({ label: "Step", value: step });
  return facts;
}

export function getApprovalAuditRows(approval: Approval): ApprovalAuditRow[] {
  return [
    { label: "Approval ID", value: approval.id },
    { label: "Job ID", value: approval.jobId },
    { label: "Topic", value: approval.topic },
    {
      label: "Workflow",
      value:
        approval.workflowContext?.workflowName ||
        approval.workflowContext?.workflowId,
    },
    { label: "Run ID", value: approval.workflowContext?.runId },
    { label: "Policy snapshot", value: approval.policySnapshot },
    { label: "Job hash", value: approval.jobHash },
    { label: "Approval ref", value: approval.approvalRef },
    { label: "Context pointer", value: approval.contextPtr },
    {
      label: "Requested",
      value: approval.requestedAt
        ? formatRelativeTime(approval.requestedAt)
        : undefined,
    },
    { label: "Decided by", value: approval.actor },
    {
      label: "Resolved",
      value: approval.resolvedAt
        ? formatRelativeTime(approval.resolvedAt)
        : undefined,
    },
  ].filter((row) => !!row.value);
}

export function getApprovalSearchText(approval: Approval): string {
  return [
    approval.id,
    approval.jobId,
    approval.topic,
    approval.humanSummary,
    approval.reason,
    approval.decisionSummary?.title,
    approval.decisionSummary?.why,
    approval.decisionSummary?.vendor,
    approval.decisionSummary?.nextEffect,
    approval.decisionSummary?.itemsPreview?.join(" "),
    approval.workflowContext?.workflowId,
    approval.workflowContext?.workflowName,
    approval.workflowContext?.runId,
    approval.workflowContext?.stepId,
    approval.workflowContext?.stepName,
    approval.policySnapshot,
    approval.jobHash,
    approval.approvalRef,
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

function renderJson(data: unknown): string {
  try {
    return JSON.stringify(data, null, 2);
  } catch {
    return String(data);
  }
}

function DecisionFacts({
  approval,
  compact = false,
}: {
  approval: Approval;
  compact?: boolean;
}) {
  const facts = getApprovalFacts(approval);
  if (!facts.length) return null;

  return (
    <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1 rounded-md border border-border/70 bg-surface-1/55 px-3 py-2 text-[11px] text-muted-foreground">
      {totalCount > 0 && (<><input type="checkbox" checked={selectedCount > 0 && selectedCount === totalCount} ref={(el) => { if (el) el.indeterminate = selectedCount > 0 && selectedCount < totalCount; }} onChange={onSelectAll} className="h-3.5 w-3.5 rounded border-border text-accent focus:ring-accent cursor-pointer" title="Select all" aria-label="Select all pending approvals" /><span aria-hidden>&middot;</span></>)}
      <span><span className="font-semibold text-foreground">{pending}</span> pending</span>
      <span aria-hidden>&middot;</span>
      <span><span className={cn("font-semibold", critical > 0 ? "text-danger" : "text-foreground")}>{critical}</span> critical</span>
      <span aria-hidden>&middot;</span>
      <span>avg wait <span className="font-semibold text-foreground">{formatWait(avgWait)}</span></span>
      <span aria-hidden>&middot;</span>
      <span><span className="font-semibold text-foreground">{resolvedToday}</span> resolved today</span>
      {slaBreaches > 0 && (<><span aria-hidden>&middot;</span><span><span className="font-semibold text-danger">{slaBreaches}</span> SLA breach{slaBreaches !== 1 ? "es" : ""}</span></>)}
    </div>
  );
}

function MiniCard({ approval, active, onClick }: { approval: Approval; active: boolean; onClick: () => void }) {
  const urgency = approval.urgencyLevel === "critical" || approval.urgencyLevel === "breach"
    ? "danger"
    : approval.urgencyLevel === "aging"
      ? "warning"
      : "default";
  const urgencyDotColor: Record<typeof urgency, string> = { default: "bg-success", warning: "bg-warning", danger: "bg-danger" };
  const summary = approval.humanSummary || `Job ${approval.jobId.slice(0, 8)} requires approval`;
  const wait = formatWait(approval.waitMs ?? 0);
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={active ? "true" : undefined}
      aria-pressed={active}
      aria-label={`${summary}, waiting ${wait}`}
      className={cn(
        "w-full rounded-md border px-3 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35 focus-visible:ring-offset-1 focus-visible:ring-offset-surface-0",
        active ? "border-accent bg-status-info-bg" : "border-border bg-surface-1/50 hover:bg-surface-2/45"
      )}
    >
      <div className="flex items-center gap-2">
        <span className={cn("h-2.5 w-2.5 shrink-0 rounded-full", urgencyDotColor[urgency])} />
        <p className="min-w-0 flex-1 truncate text-xs font-medium text-foreground">{approval.humanSummary || `Job ${approval.jobId.slice(0, 8)}`}</p>
        <span className="shrink-0 font-mono text-[10px] text-muted-foreground">{wait}</span>
      </div>
    </button>
  );
}

export default function ApprovalsPage() {
  usePageTitle("Approvals");
  const { data, isLoading, isError, error, dataUpdatedAt, refetch, isRefetching } = useApprovals();
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
  const filtersActive = !isDefaultFilters(filters);
  const filteredOutCount = Math.max(0, approvals.length - sorted.length);
  const hasVisibleQueueItems = !isLoading && !isError && sorted.length > 0;
  const queueFilteredToZero = !isLoading && !isError && approvals.length > 0 && sorted.length === 0;
  const approvalsErrorMessage = useMemo(() => {
    if (!isError) return "";
    const raw = String((error as { message?: string } | null)?.message ?? "");
    const msg = raw.toLowerCase();
    if (msg.includes("timeout")) return "Approval API timed out. Retry to refresh queue state.";
    if (msg.includes("network")) return "Unable to reach approval service. Check gateway connectivity and retry.";
    return "Failed to load approvals. Retry to refresh queue state.";
  }, [isError, error]);

  const handleApprove = useCallback((id: string, comment?: string) => approveJob.mutateAsync({ id, comment }), [approveJob]);
  const handleReject = useCallback((id: string, reason: string) => rejectJob.mutateAsync({ id, reason }), [rejectJob]);

  return (
    <div className="space-y-6">
      <PageHeader
        label="Safety"
        title="Approvals"
        subtitle="Review the business decision first, then inspect technical audit detail only when needed."
        actions={
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            <RefreshCw className="mr-1 h-3 w-3" />
            Refresh
          </Button>
        }
      />

      {/* MCP per-tool approval queue — rendered as its own section so
          operators see pending tool calls at the top of the page, not
          commingled with job-level approvals which have different
          action copy and lifecycle states. */}
      <McpApprovalsSection statusFilter="pending" />

      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3 }}
        className="grid gap-4 md:grid-cols-3 xl:grid-cols-6"
      >
        {isLoading ? (
          Array.from({ length: 6 }).map((_, i) => <SkeletonCard key={i} />)
        ) : (
          <>
            <InstrumentCard accent={pending.length > 0 ? "warning" : "muted"}>
              <MetricValue
                label="Pending"
                value={pending.length}
                icon={
                  <Clock
                    className={cn(
                      "h-4 w-4",
                      pending.length > 0
                        ? "text-[var(--color-warning)]"
                        : "text-muted-foreground",
                    )}
                  />
                }
              />
            </InstrumentCard>

            <InstrumentCard accent="healthy">
              <MetricValue
                label="Approved"
                value={approved.length}
                icon={
                  <CheckCircle2 className="h-4 w-4 text-[var(--color-success)]" />
                }
              />
            </InstrumentCard>

            <InstrumentCard accent={denied.length > 0 ? "governance" : "muted"}>
              <MetricValue
                label="Denied"
                value={denied.length}
                icon={
                  <XCircle
                    className={cn(
                      "h-4 w-4",
                      denied.length > 0
                        ? "text-[var(--color-governance)]"
                        : "text-muted-foreground",
                    )}
                  />
                }
              />
            </InstrumentCard>

            <InstrumentCard accent="muted">
              <MetricValue
                label="Expired"
                value={expired.length}
                icon={<Timer className="h-4 w-4 text-muted-foreground" />}
              />
            </InstrumentCard>

            <InstrumentCard accent={invalidated.length > 0 ? "danger" : "muted"}>
              <MetricValue
                label="Invalidated"
                value={invalidated.length}
                icon={
                  <XCircle
                    className={cn(
                      "h-4 w-4",
                      invalidated.length > 0
                        ? "text-destructive"
                        : "text-muted-foreground",
                    )}
                  />
                }
              />
            </InstrumentCard>

            <InstrumentCard accent={repaired.length > 0 ? "cordum" : "muted"}>
              <MetricValue
                label="Repaired"
                value={repaired.length}
                icon={
                  <RefreshCw
                    className={cn(
                      "h-4 w-4",
                      repaired.length > 0
                        ? "text-cordum"
                        : "text-muted-foreground",
                    )}
                  />
                }
              />
            </InstrumentCard>
          </>
        )}
      </motion.div>

      <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        <div className="relative w-full max-w-md">
          <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <input
            type="text"
            aria-label="Search approvals"
            placeholder="Search decision summaries, vendors, workflow steps, or IDs"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-10 w-full rounded-2xl border border-border bg-surface-1 pl-8 pr-3 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
          />
        </div>
        <div className="flex flex-wrap items-center gap-1 rounded-2xl border border-border bg-surface-1 p-0.5">
          {[
            { id: "pending", label: "Pending", count: pending.length },
            { id: "approved", label: "Approved", count: approved.length },
            { id: "rejected", label: "Denied", count: denied.length },
            { id: "expired", label: "Expired", count: expired.length },
            { id: "invalidated", label: "Invalidated", count: invalidated.length },
            { id: "repaired", label: "Repaired", count: repaired.length },
            { id: "all", label: "All", count: all.length },
          ].map((tab) => (
            <button
              type="button"
              key={tab.id}
              aria-pressed={activeTab === tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                "rounded-xl px-3 py-2 text-xs font-medium transition-colors",
                activeTab === tab.id
                  ? "bg-cordum/10 text-cordum"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {tab.label}
              {tab.count > 0 && (
                <span className="ml-1.5 rounded-full bg-surface-2 px-1.5 py-0.5 font-mono text-xs">
                  {tab.count}
                </span>
              )}
            </button>
          ))}
        </div>
        {sorted.length > 0 && <Badge variant="warning" className="px-2 py-0.5 text-[10px] uppercase tracking-[0.08em]">{sorted.length} pending</Badge>}
      </div>
      <StatsStrip approvals={approvals} resolvedToday={resolvedToday} selectedCount={selectedIds.size} totalCount={sorted.length} onSelectAll={selectAll} />
      <div className="flex w-fit gap-1 rounded-md border border-border/80 bg-surface-1/45 p-1" role="tablist" aria-label="Approval views">
        <button type="button" role="tab" aria-selected={activeTab === "queue"} aria-controls="tabpanel-queue" id="tab-queue" className={cn("flex items-center gap-1.5 rounded-md px-3 py-1.5 text-[11px] font-semibold uppercase tracking-[0.12em] transition", activeTab === "queue" ? "bg-status-info-bg text-accent" : "text-muted-foreground hover:text-foreground")} onClick={() => setActiveTab("queue")}>
          <Clock className="h-3.5 w-3.5" />Queue{sorted.length > 0 ? ` (${sorted.length})` : ""}
        </button>
        <button type="button" role="tab" aria-selected={activeTab === "history"} aria-controls="tabpanel-history" id="tab-history" className={cn("flex items-center gap-1.5 rounded-md px-3 py-1.5 text-[11px] font-semibold uppercase tracking-[0.12em] transition", activeTab === "history" ? "bg-status-info-bg text-accent" : "text-muted-foreground hover:text-foreground")} onClick={() => setActiveTab("history")}>
          <History className="h-3.5 w-3.5" />History
        </button>
      </div>
      {activeTab === "queue" && (
        <div id="tabpanel-queue" role="tabpanel" aria-labelledby="tab-queue" className="space-y-3">
          {!isLoading && approvals.length > 0 && <ApprovalQueueFilters approvals={approvals} filters={filters} onFiltersChange={setFilters} />}
          {hasVisibleQueueItems && (
            <div role="status" aria-live="polite" className="flex items-center gap-2 rounded-md border border-border/70 bg-surface-1/45 px-3 py-2 text-[11px] text-muted-foreground">
              <SlidersHorizontal className="h-3.5 w-3.5 text-muted-foreground" />
              <span>
                Showing <span className="font-semibold text-foreground">{sorted.length}</span> approval{sorted.length !== 1 ? "s" : ""}
                {filtersActive ? (
                  <> after filters{filteredOutCount > 0 ? ` (${filteredOutCount} hidden)` : ""}</>
                ) : (
                  <> in queue</>
                )}
              </span>
            </div>
          )}
          {!isLoading && isRefetching && (
            <div role="status" aria-live="polite" className="flex items-center gap-2 rounded-md border border-status-info-border bg-status-info-bg px-3 py-2 text-[11px] text-info">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              Refreshing queue state\u2026
            </div>
          )}
          {isLoading && (
            <div aria-live="polite" role="status" className="space-y-2.5">
              <p className="text-xs text-muted-foreground">Loading pending approvals\u2026</p>
              {Array.from({ length: 4 }, (_, i) => (
                <Card key={i} className="animate-pulse p-4">
                  <div className="space-y-2.5">
                    <div className="h-4 w-1/3 rounded bg-surface-2/70" />
                    <div className="h-3.5 w-2/3 rounded bg-surface-2/70" />
                    <div className="h-3.5 w-1/2 rounded bg-surface-2/70" />
                  </div>
                </Card>
              ))}
            </div>
          )}
          {!isLoading && isError && (
            <Card variant="warning" role="alert" aria-live="assertive">
              <div className="flex flex-col items-center gap-3 py-8 text-center">
                <div className="rounded-full border border-status-warning-border bg-status-warning-bg p-2 text-warning">
                  <AlertTriangle className="h-4 w-4" />
                </div>
                <p className="text-sm font-medium text-warning">{approvalsErrorMessage}</p>
                <p className="max-w-md text-xs text-muted-foreground">Queue data is unavailable. Confirm gateway/auth health and retry.</p>
                <Button variant="outline" size="sm" onClick={() => refetch()}>
                  Retry
                </Button>
              </div>
            </Card>
          )}
          {!isLoading && !isError && sorted.length === 0 && (
            <div className="rounded-lg border border-border/70 bg-surface-1/45 py-14 text-center" role="status" aria-live="polite">
              {queueFilteredToZero ? (
                <>
                  <SlidersHorizontal className="mx-auto mb-3 h-9 w-9 text-info opacity-70" />
                  <p className="text-sm font-semibold text-foreground">No approvals match the current filters</p>
                  <p className="mt-1 text-xs text-muted-foreground">Clear filters to restore the full queue view.</p>
                  <Button variant="ghost" size="sm" className="mt-4" onClick={() => setFilters(DEFAULT_FILTERS)}>
                    Clear filters
                  </Button>
                </>
              ) : (
                <>
                  <CheckCircle className="mx-auto mb-3 h-9 w-9 text-success opacity-70" />
                  <p className="text-sm font-semibold text-foreground">All clear — no pending approvals</p>
                  <p className="mt-1 text-xs text-muted-foreground">Nothing needs your attention right now.</p>
                  <Button variant="ghost" size="sm" className="mt-4" onClick={() => setActiveTab("history")}>
                    View History
                  </Button>
                </>
              )}
            </div>
          )}
          {!isLoading && !isError && sorted.length > 0 && (
            panelOpen ? (
              <div className="hidden space-y-1.5 md:block" role="list" aria-label="Pending approvals quick list">
                {sorted.map((approval) => (
                  <div key={approval.id} role="listitem">
                    <MiniCard approval={approval} active={approval.id === selectedId} onClick={() => openPanel(approval.id)} />
                  </div>
                ))}
              </div>
            ) : (
              <div className="space-y-3" role="list" aria-label="Pending approvals queue">
                {sorted.map((approval: Approval) => (
                  <div key={approval.id} role="listitem">
                    <ApprovalCardV2
                      approval={approval}
                      onApprove={(id, comment) => { void handleApprove(id, comment); }}
                      onReject={(id, reason) => { void handleReject(id, reason); }}
                      onReview={openPanel}
                      selected={selectedIds.has(approval.id)}
                      onToggleSelect={toggleSelect}
                    />
                  </div>
                ))}
              </div>
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
