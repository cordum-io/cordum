/*
 * DESIGN: "Control Surface" — Approvals
 * Matches cordumds-gj5mw4zm.manus.space showcase patterns
 */
import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import type { Approval } from "@/api/types";
import { useApprovals, useApproveJob, useRejectJob } from "@/hooks/useApprovals";
import { useDialogA11y } from "@/hooks/useDialogA11y";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import { Search, RefreshCw, UserCheck, CheckCircle2, XCircle, Clock, X, ArrowRight } from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { MetricValue } from "@/components/ui/MetricValue";

function approvalStatusVariant(status: string) {
  switch (status) {
    case "pending": return "warning" as const;
    case "approved": return "healthy" as const;
    case "denied": return "danger" as const;
    case "expired": return "muted" as const;
    default: return "muted" as const;
  }
}

// ---------------------------------------------------------------------------
// Exported for unit tests (following SettingsKeysPage pattern)
// ---------------------------------------------------------------------------

/** Drawer a11y configuration — tested to ensure ARIA attributes are wired. */
export const DRAWER_A11Y = {
  role: "dialog" as const,
  ariaModal: true,
  labelledById: "approval-drawer-title",
  /** The hook wired to the drawer — "useDialogA11y" provides Escape + focus trap */
  hookName: "useDialogA11y" as const,
} as const;

/** Resolves the deny reason: trims input, falls back to default. */
export function resolveDenyReason(raw: string): string {
  const trimmed = raw.trim();
  return trimmed || "Denied by operator";
}

/** Pure handler for deny confirmation — calls mutate with resolved reason. */
export function handleDenyConfirm(
  target: Approval | null,
  rawReason: string,
  deps: {
    mutate: (input: { id: string; reason: string }) => void;
    clearTarget: () => void;
  },
): void {
  if (!target) return;
  deps.mutate({ id: target.id, reason: resolveDenyReason(rawReason) });
  deps.clearTarget();
}

export default function ApprovalsPage() {
  const [search, setSearch] = useState("");
  const [activeTab, setActiveTab] = useState("pending");
  const [selectedApproval, setSelectedApproval] = useState<Approval | null>(null);
  const [denyTarget, setDenyTarget] = useState<Approval | null>(null);
  const [denyReason, setDenyReason] = useState("");

  const drawerRef = useDialogA11y(() => setSelectedApproval(null));
  const { data: approvalsData, isLoading, refetch } = useApprovals();
  const approvals = approvalsData?.items ?? [];
  const approveMutation = useApproveJob();
  const rejectMutation = useRejectJob();

  const handleApprove = (approval: Approval) => {
    if (approveMutation.isPending) return;
    approveMutation.mutate({ id: approval.id });
  };

  const handleDeny = (approval: Approval, reason: string) => {
    if (rejectMutation.isPending) return;
    rejectMutation.mutate({ id: approval.id, reason });
  };

  const all = approvals ?? [];
  const pending = all.filter((a) => a.status === "pending");
  const approved = all.filter((a) => a.status === "approved");
  const denied = all.filter((a) => a.status === "denied");

  const filtered = all
    .filter((a) => {
      if (activeTab !== "all" && a.status !== activeTab) return false;
      if (search) {
        const q = search.toLowerCase();
        return a.id.toLowerCase().includes(q) || (a.topic ?? "").toLowerCase().includes(q) || (a.jobId ?? "").toLowerCase().includes(q);
      }
      return true;
    })
    .sort((a, b) => {
      if (a.status === "pending" && b.status !== "pending") return -1;
      if (b.status === "pending" && a.status !== "pending") return 1;
      return new Date(b.requestedAt ?? 0).getTime() - new Date(a.requestedAt ?? 0).getTime();
    });

  return (
    <div className="space-y-6">
      <PageHeader
        label="Safety"
        title="Approvals"
        subtitle="Human-in-the-loop review queue for agent actions"
        actions={
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            <RefreshCw className="w-3 h-3 mr-1" />
            Refresh
          </Button>
        }
      />

      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3 }}
        className="grid grid-cols-3 gap-4"
      >
        {isLoading ? (
          Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)
        ) : (
          <>
            <InstrumentCard accent={pending.length > 0 ? "warning" : "muted"}>
              <MetricValue
                label="Pending"
                value={pending.length}
                icon={<Clock className={cn("w-4 h-4", pending.length > 0 ? "text-[var(--color-warning)]" : "text-muted-foreground")} />}
              />
            </InstrumentCard>

            <InstrumentCard accent="healthy">
              <MetricValue
                label="Approved"
                value={approved.length}
                icon={<CheckCircle2 className="w-4 h-4 text-[var(--color-success)]" />}
              />
            </InstrumentCard>

            <InstrumentCard accent={denied.length > 0 ? "danger" : "muted"}>
              <MetricValue
                label="Denied"
                value={denied.length}
                icon={<XCircle className={cn("w-4 h-4", denied.length > 0 ? "text-destructive" : "text-muted-foreground")} />}
              />
            </InstrumentCard>
          </>
        )}
      </motion.div>

      {/* Filters — showcase style */}
      <div className="flex items-center gap-3">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search approvals..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-8 w-full pl-8 pr-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
          />
        </div>
        <div className="flex items-center gap-1 bg-surface-1 border border-border rounded-2xl p-0.5">
          {[
            { id: "pending", label: "Pending", count: pending.length },
            { id: "approved", label: "Approved", count: approved.length },
            { id: "denied", label: "Denied", count: denied.length },
            { id: "all", label: "All", count: all.length },
          ].map((tab) => (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                "px-3 py-1.5 text-xs font-medium rounded transition-colors",
                activeTab === tab.id
                  ? "bg-cordum/10 text-cordum"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {tab.label}
              {tab.count > 0 && (
                <span className="ml-1.5 px-1.5 py-0.5 rounded-full text-[10px] font-mono bg-surface-2">{tab.count}</span>
              )}
            </button>
          ))}
        </div>
      </div>

      {/* Approval Cards — showcase style */}
      {isLoading ? (
        <SkeletonTable rows={5} />
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<UserCheck className="w-5 h-5" />}
          title={activeTab === "pending" ? "No pending approvals" : "No approvals found"}
          description={activeTab === "pending" ? "All clear — no actions waiting for review" : "Try adjusting your search or filters"}
        />
      ) : (
        <div className="space-y-3">
          <AnimatePresence mode="popLayout">
          {filtered.map((approval) => (
            <motion.div
              key={approval.id}
              layout
              initial={{ opacity: 0, y: 8 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, x: -100, height: 0, marginBottom: 0, overflow: "hidden" }}
              transition={{ duration: 0.3 }}
              className={cn(
                "instrument-card cursor-pointer",
                approval.status === "pending" && "border-[var(--color-warning)]/30",
                approval.status === "denied" && "status-danger",
              )}
              onClick={() => setSelectedApproval(approval)}
            >
              <div className="flex items-start justify-between gap-4">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2 mb-2.5">
                    <span className="font-mono text-sm text-cordum">{approval.id.slice(0, 16)}</span>
                    <StatusBadge variant={approvalStatusVariant(approval.status)} dot pulse={approval.status === "pending"}>
                      {approval.status}
                    </StatusBadge>
                    {approval.workflowContext && (
                      <span className="px-1.5 py-0.5 text-[10px] font-mono bg-cordum/10 text-cordum rounded">
                        Workflow Gate
                      </span>
                    )}
                    <span className="text-[10px] text-muted-foreground font-mono">
                      {approval.requestedAt ? formatRelativeTime(approval.requestedAt) : "—"}
                    </span>
                  </div>
                  <h3 className="text-sm font-semibold font-display text-foreground leading-snug">
                    {approval.workflowContext
                      ? `${approval.workflowContext.workflowId} — ${approval.workflowContext.stepName || approval.workflowContext.stepId}`
                      : approval.humanSummary || approval.topic || "Approval Request"}
                  </h3>
                </div>
                {approval.status === "pending" ? (
                  <div className="flex gap-2 shrink-0">
                    <Button
                      size="sm"
                      variant="danger"
                      disabled={rejectMutation.isPending || approveMutation.isPending}
                      onClick={(e) => {
                        e.stopPropagation();
                        setDenyTarget(approval);
                        setDenyReason("");
                      }}
                    >
                      <XCircle className="w-3.5 h-3.5 mr-1" />
                      Deny
                    </Button>
                    <Button
                      size="sm"
                      variant="primary"
                      disabled={approveMutation.isPending || rejectMutation.isPending}
                      loading={approveMutation.isPending}
                      onClick={(e) => {
                        e.stopPropagation();
                        handleApprove(approval);
                      }}
                    >
                      <CheckCircle2 className="w-3.5 h-3.5 mr-1" />
                      Approve
                    </Button>
                  </div>
                ) : (
                  <div className="shrink-0 text-muted-foreground group-hover:text-cordum transition-colors pt-1">
                    <ArrowRight className="w-4 h-4" />
                  </div>
                )}
              </div>
            </motion.div>
          ))}
          </AnimatePresence>
        </div>
      )}

      {/* Deny Confirmation Dialog */}
      <ConfirmDialog open={!!denyTarget}
        onClose={() => setDenyTarget(null)}
        onConfirm={() => handleDenyConfirm(denyTarget, denyReason, { mutate: (input) => handleDeny({ id: input.id } as Approval, input.reason), clearTarget: () => setDenyTarget(null) })}
        title="Deny Approval"
        description={
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">Are you sure you want to deny this approval request?</p>
            <div>
              <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1">Reason</label>
              <textarea
                value={denyReason}
                onChange={(e) => setDenyReason(e.target.value)}
                placeholder="Why is this request being denied?"
                rows={3}
                className="w-full px-3 py-2 text-sm bg-surface-2 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum resize-none"
              />
            </div>
          </div>
        }
        confirmLabel="Deny"
        variant="destructive" />

      {/* Detail Drawer */}
      {selectedApproval && (
        <>
          <div className="fixed inset-0 bg-black/40 z-40" onClick={() => setSelectedApproval(null)} />
          <motion.div
            ref={drawerRef}
            role="dialog"
            aria-modal="true"
            aria-labelledby="approval-drawer-title"
            initial={{ x: 440 }}
            animate={{ x: 0 }}
            transition={{ type: "spring", stiffness: 300, damping: 30 }}
            className="fixed inset-y-0 right-0 w-[440px] bg-surface-1 border-l border-border shadow-2xl z-50 overflow-y-auto"
          >
            <div className="p-5 border-b border-border flex items-center justify-between">
              <div>
                <h2 id="approval-drawer-title" className="font-display font-semibold text-sm text-foreground">Approval Detail</h2>
                <p className="text-xs text-muted-foreground font-mono mt-0.5">{selectedApproval.id}</p>
              </div>
              <button
                onClick={() => setSelectedApproval(null)}
                className="p-1.5 rounded-full hover:bg-surface-2 text-muted-foreground hover:text-foreground transition-colors"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="p-5 space-y-5">
              <div className="flex items-center gap-2">
                <StatusBadge variant={approvalStatusVariant(selectedApproval.status)} dot pulse={selectedApproval.status === "pending"}>
                  {selectedApproval.status}
                </StatusBadge>
                {selectedApproval.workflowContext && (
                  <span className="px-1.5 py-0.5 text-[10px] font-mono bg-cordum/10 text-cordum rounded">
                    Workflow Gate
                  </span>
                )}
              </div>
              <dl className="space-y-3">
                {[
                  ["Source", selectedApproval.workflowContext ? "Workflow Gate" : "Safety Policy"],
                  ["Topic", selectedApproval.workflowContext?.workflowId || selectedApproval.topic],
                  ...(selectedApproval.workflowContext
                    ? [["Step", selectedApproval.workflowContext.stepName || selectedApproval.workflowContext.stepId], ["Run ID", selectedApproval.workflowContext.runId]]
                    : []),
                  ["Job ID", selectedApproval.jobId],
                  ["Requested", selectedApproval.requestedAt ? formatRelativeTime(selectedApproval.requestedAt) : "—"],
                  ["Summary", selectedApproval.humanSummary],
                  ["Decided By", selectedApproval.actor],
                  ["Reason", selectedApproval.reason],
                ].map(([label, value]) => (
                  <div key={label as string}>
                    <dt className="text-[10px] font-mono uppercase tracking-wider text-muted-foreground mb-0.5">{label}</dt>
                    <dd className="text-sm text-foreground">{(value as string) || "—"}</dd>
                  </div>
                ))}
              </dl>
              {selectedApproval.jobInput && Object.keys(selectedApproval.jobInput).length > 0 && (
                <div>
                  <p className="text-[10px] font-mono uppercase tracking-wider text-muted-foreground mb-1">Job Input</p>
                  <div className="rounded-2xl bg-surface-2/50 border border-border p-3 font-mono text-xs text-foreground overflow-auto max-h-[200px]">
                    <pre>{JSON.stringify(selectedApproval.jobInput, null, 2)}</pre>
                  </div>
                </div>
              )}
              {selectedApproval.jobContext && (
                <div>
                  <p className="text-[10px] font-mono uppercase tracking-wider text-muted-foreground mb-1">Context</p>
                  <div className="rounded-2xl bg-surface-2/50 border border-border p-3 font-mono text-xs text-foreground overflow-auto max-h-[200px]">
                    <pre>{JSON.stringify(selectedApproval.jobContext, null, 2)}</pre>
                  </div>
                </div>
              )}
              {selectedApproval.status === "pending" && (
                <div className="flex gap-2 pt-2">
                  <Button
                    variant="primary"
                    className="flex-1"
                    disabled={approveMutation.isPending || rejectMutation.isPending}
                    loading={approveMutation.isPending}
                    onClick={() => handleApprove(selectedApproval)}
                  >
                    <CheckCircle2 className="w-3.5 h-3.5 mr-1" />
                    Approve
                  </Button>
                  <Button
                    variant="danger"
                    className="flex-1"
                    disabled={rejectMutation.isPending || approveMutation.isPending}
                    onClick={() => { setDenyTarget(selectedApproval); setDenyReason(""); }}
                  >
                    <XCircle className="w-3.5 h-3.5 mr-1" />
                    Deny
                  </Button>
                </div>
              )}
            </div>
          </motion.div>
        </>
      )}
    </div>
  );
}
