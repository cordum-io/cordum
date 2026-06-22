import { useMemo, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import type { EdgeApproval } from "@/api/types";
import {
  useEdgeApprovals,
  useApproveEdgeApproval,
  useRejectEdgeApproval,
} from "@/hooks/useEdgeSessions";
import { useDialogA11y } from "@/hooks/useDialogA11y";
import {
  StatusBadge,
  statusToneBorderClasses,
  type BadgeVariant,
} from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import { Textarea } from "@/components/ui/Textarea";
import { Tabs } from "@/components/ui/Tabs";
import { StatTile } from "@/components/ui/StatTile";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import {
  CheckCircle2,
  XCircle,
  Clock,
  Timer,
  X,
  ArrowRight,
  RefreshCw,
  ShieldAlert,
} from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import {
  approvalStatusVariant,
  compactHash,
  isExpired,
  isTerminal,
} from "@/components/edge/edgeApprovalUtils";
import { friendlyError } from "@/lib/friendlyError";
import { toast } from "sonner";

function edgeApprovalTitle(approval: EdgeApproval): string {
  return approval.metadata?.action || approval.reason || "Governed Edge action";
}

function formatEdgeStatusLabel(status: string): string {
  if (status === "rejected") return "Denied";
  return status.charAt(0).toUpperCase() + status.slice(1);
}

function isEdgeApprovalActionable(approval: EdgeApproval): boolean {
  return approval.status === "pending" && !isExpired(approval);
}

interface FactRowProps {
  label: string;
  value?: string | null;
  mono?: boolean;
}

function FactRow({ label, value, mono }: FactRowProps) {
  if (!value) return null;
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
        {label}
      </span>
      <span className={cn("text-xs text-foreground break-all", mono && "font-mono")}>
        {value}
      </span>
    </div>
  );
}

interface DetailDrawerProps {
  approval: EdgeApproval;
  onClose: () => void;
  drawerRef: React.RefObject<HTMLDivElement | null>;
  onApprove: (a: EdgeApproval) => void;
  onDeny: (a: EdgeApproval) => void;
  approving: boolean;
  denying: boolean;
}

function DetailDrawer({
  approval,
  onClose,
  drawerRef,
  onApprove,
  onDeny,
  approving,
  denying,
}: DetailDrawerProps) {
  const actionable = isEdgeApprovalActionable(approval);
  const expired = isExpired(approval);
  const terminal = isTerminal(approval);
  const title = edgeApprovalTitle(approval);

  return (
    <>
      <div
        className="fixed inset-0 z-40 bg-black/40"
        onClick={onClose}
        aria-hidden
      />
      <motion.div
        ref={drawerRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="edge-approval-drawer-title"
        initial={{ x: 440 }}
        animate={{ x: 0 }}
        transition={{ type: "spring", stiffness: 300, damping: 30 }}
        className="fixed inset-y-0 right-0 z-50 w-full max-w-[520px] overflow-y-auto border-l border-border bg-surface-1 shadow-2xl"
      >
        <div className="flex items-start justify-between gap-4 border-b border-border p-5">
          <div className="space-y-2">
            <div className="flex flex-wrap items-center gap-2">
              <StatusBadge
                variant={approvalStatusVariant(approval.status)}
                dot
                pulse={actionable}
              >
                {formatEdgeStatusLabel(approval.status)}
              </StatusBadge>
              <StatusBadge variant="info">Edge · Claude Code</StatusBadge>
              {expired && !terminal && (
                <StatusBadge variant="danger">Expired</StatusBadge>
              )}
            </div>
            <h2
              id="edge-approval-drawer-title"
              className="font-display text-base font-semibold text-foreground"
            >
              {title}
            </h2>
            <p className="font-mono text-xs text-muted-foreground">
              {approval.approvalRef}
            </p>
          </div>
          <button
            type="button"
            aria-label="Close approval detail"
            onClick={onClose}
            className="flex min-h-[44px] min-w-[44px] items-center justify-center -mr-2 rounded-full text-muted-foreground transition-colors hover:bg-surface-2 hover:text-foreground"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-5 p-5">
          <section
            aria-labelledby="edge-appr-reason-section"
            className="rounded-3xl border border-border bg-surface-2/60 p-4"
          >
            <p
              id="edge-appr-reason-section"
              className="mb-2 text-xs font-mono uppercase tracking-wide text-muted-foreground"
            >
              Trigger reason
            </p>
            <p className="text-sm text-foreground">{approval.reason}</p>
            {approval.resolutionReason && (
              <p className="mt-2 text-xs text-muted-foreground">
                Resolution: {approval.resolutionReason}
              </p>
            )}
          </section>

          <section
            aria-labelledby="edge-appr-facts-section"
            className="rounded-3xl border border-border bg-surface-2/40 p-4"
          >
            <p
              id="edge-appr-facts-section"
              className="mb-3 text-xs font-mono uppercase tracking-wide text-muted-foreground"
            >
              Action details
            </p>
            <div className="grid grid-cols-2 gap-3">
              <FactRow label="Requester" value={approval.requester || approval.principalId} />
              <FactRow label="Rule" value={approval.ruleId} mono />
              <FactRow label="Action hash" value={compactHash(approval.actionHash)} mono />
              <FactRow label="Input hash" value={compactHash(approval.inputHash)} mono />
              <FactRow label="Policy snapshot" value={compactHash(approval.policySnapshot)} mono />
              <FactRow label="Session" value={compactHash(approval.sessionId)} mono />
              <FactRow
                label="Created"
                value={approval.createdAt ? formatRelativeTime(approval.createdAt) : undefined}
              />
              <FactRow
                label="Expires"
                value={approval.expiresAt ? formatRelativeTime(approval.expiresAt) : "—"}
              />
              {approval.resolvedAt && (
                <FactRow label="Resolved" value={formatRelativeTime(approval.resolvedAt)} />
              )}
              {approval.resolvedBy && (
                <FactRow label="Resolved by" value={approval.resolvedBy} />
              )}
            </div>
          </section>

          {approval.metadata && Object.keys(approval.metadata).length > 0 && (
            <section
              aria-labelledby="edge-appr-metadata-section"
              className="rounded-3xl border border-border bg-surface-2/30 p-4"
            >
              <p
                id="edge-appr-metadata-section"
                className="mb-3 text-xs font-mono uppercase tracking-wide text-muted-foreground"
              >
                Metadata
              </p>
              <div className="grid grid-cols-2 gap-3">
                {Object.entries(approval.metadata).map(([k, v]) => (
                  <FactRow key={k} label={k.replace(/_/g, " ")} value={v} mono />
                ))}
              </div>
            </section>
          )}

          {actionable && (
            <section
              aria-label="Approval actions"
              className="rounded-3xl border border-border bg-surface-2/40 p-4"
            >
              <div className="grid gap-2 sm:grid-cols-2">
                <Button
                  variant="primary"
                  className="w-full"
                  aria-label={`Approve ${title}`}
                  disabled={approving || denying}
                  loading={approving}
                  onClick={() => onApprove(approval)}
                >
                  <CheckCircle2 className="mr-1 h-3.5 w-3.5" />
                  Approve
                </Button>
                <Button
                  variant="danger"
                  className="w-full"
                  aria-label={`Deny ${title}`}
                  disabled={denying || approving}
                  loading={denying}
                  onClick={() => onDeny(approval)}
                >
                  <XCircle className="mr-1 h-3.5 w-3.5" />
                  Deny
                </Button>
              </div>
            </section>
          )}
        </div>
      </motion.div>
    </>
  );
}

interface EdgeApprovalsSectionProps {
  /** When true the section renders a full standalone page view (stat tiles + tabs). */
  fullPage?: boolean;
}

export function EdgeApprovalsSection({ fullPage = false }: EdgeApprovalsSectionProps) {
  const [activeTab, setActiveTab] = useState("pending");
  const [selected, setSelected] = useState<EdgeApproval | null>(null);
  const [denyTarget, setDenyTarget] = useState<EdgeApproval | null>(null);
  const [denyReason, setDenyReason] = useState("");

  const drawerRef = useDialogA11y(() => setSelected(null), {
    enabled: !!selected,
    initialFocusSelector: 'button[aria-label="Close approval detail"]',
  });

  const { data, isLoading, isError, error, refetch } = useEdgeApprovals({ limit: 200 });
  const approveMutation = useApproveEdgeApproval();
  const rejectMutation = useRejectEdgeApproval();

  const allItems = data?.items ?? [];

  const pending = useMemo(() => allItems.filter((a) => a.status === "pending"), [allItems]);
  const approved = useMemo(() => allItems.filter((a) => a.status === "approved"), [allItems]);
  const denied = useMemo(() => allItems.filter((a) => a.status === "rejected"), [allItems]);
  const expired = useMemo(
    () => allItems.filter((a) => a.status === "expired" || a.status === "invalidated"),
    [allItems],
  );

  const filtered = useMemo(() => {
    const base =
      activeTab === "all"
        ? allItems
        : activeTab === "pending"
          ? pending
          : activeTab === "approved"
            ? approved
            : activeTab === "rejected"
              ? denied
              : expired;
    return [...base].sort((a, b) => {
      if (a.status === "pending" && b.status !== "pending") return -1;
      if (b.status === "pending" && a.status !== "pending") return 1;
      return new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime();
    });
  }, [activeTab, allItems, pending, approved, denied, expired]);

  const handleApprove = (approval: EdgeApproval) => {
    if (approveMutation.isPending) return;
    approveMutation.mutate(
      { approvalRef: approval.approvalRef },
      {
        onSuccess: () => {
          if (selected?.approvalRef === approval.approvalRef) setSelected(null);
        },
        onError: (err) => {
          const friendly = friendlyError(err, "approve edge action");
          toast.error(friendly.title, { description: friendly.description });
        },
      },
    );
  };

  const handleDeny = (reason: string) => {
    if (!denyTarget || rejectMutation.isPending || !reason.trim()) return;
    rejectMutation.mutate(
      { approvalRef: denyTarget.approvalRef, reason: reason.trim() },
      {
        onSuccess: () => {
          if (selected?.approvalRef === denyTarget.approvalRef) setSelected(null);
          setDenyTarget(null);
          setDenyReason("");
        },
        onError: (err) => {
          const friendly = friendlyError(err, "deny edge action");
          toast.error(friendly.title, { description: friendly.description });
        },
      },
    );
  };

  return (
    <div className="space-y-5">
      {fullPage && (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3 }}
          className="grid gap-4 md:grid-cols-4"
        >
          {isLoading ? (
            Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)
          ) : (
            <>
              <StatTile
                accent={pending.length > 0 ? "warning" : "muted"}
                label="Pending"
                value={pending.length}
                helperText={pending.length > 0 ? "Needs operator review" : "Queue clear"}
                icon={
                  <Clock
                    className={cn(
                      "h-4 w-4",
                      pending.length > 0 ? "text-warning" : "text-muted-foreground",
                    )}
                  />
                }
              />
              <StatTile
                accent="healthy"
                label="Approved"
                value={approved.length}
                helperText="Resolved — action allowed"
                icon={<CheckCircle2 className="h-4 w-4 text-success" />}
              />
              <StatTile
                accent={denied.length > 0 ? "governance" : "muted"}
                label="Denied"
                value={denied.length}
                helperText={denied.length > 0 ? "Action blocked" : "No denied items"}
                icon={
                  <XCircle
                    className={cn(
                      "h-4 w-4",
                      denied.length > 0 ? "text-governance" : "text-muted-foreground",
                    )}
                  />
                }
              />
              <StatTile
                accent="muted"
                label="Expired / Invalidated"
                value={expired.length}
                helperText="Timed out or policy changed"
                icon={<Timer className="h-4 w-4 text-muted-foreground" />}
              />
            </>
          )}
        </motion.div>
      )}

      <div className="flex items-center justify-between gap-4 flex-wrap">
        {fullPage && (
          <Tabs
            ariaLabel="Edge approval status filters"
            variant="segmented"
            className="w-full lg:w-auto"
            activeTab={activeTab}
            onChange={setActiveTab}
            tabs={[
              { id: "pending", label: "Pending", count: pending.length },
              { id: "approved", label: "Approved", count: approved.length },
              { id: "rejected", label: "Denied", count: denied.length },
              { id: "expired", label: "Expired", count: expired.length },
              { id: "all", label: "All", count: allItems.length },
            ]}
          />
        )}
        <Button
          variant="outline"
          size="sm"
          className={fullPage ? "ml-auto" : ""}
          onClick={() => void refetch()}
        >
          <RefreshCw className="mr-1 h-3 w-3" />
          Refresh
        </Button>
      </div>

      {isLoading ? (
        <SkeletonTable rows={3} />
      ) : isError ? (
        <EmptyState
          icon={<XCircle className="h-5 w-5" />}
          title="Edge approvals unavailable"
          description={
            error instanceof Error ? error.message : "Could not load edge approvals."
          }
          action={
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              Try again
            </Button>
          }
        />
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<ShieldAlert className="h-5 w-5" />}
          title={activeTab === "pending" ? "No pending edge approvals" : "No edge approvals found"}
          description={
            activeTab === "pending"
              ? "Edge approvals appear when Claude Code tries a governed action (e.g. editing a file). Start a governed session to trigger one."
              : "Try a different status filter."
          }
        />
      ) : (
        <div className="space-y-3">
          <AnimatePresence mode="popLayout">
            {filtered.map((approval) => {
              const actionable = isEdgeApprovalActionable(approval);
              const title = edgeApprovalTitle(approval);
              const expired = isExpired(approval);

              return (
                <motion.article
                  key={approval.approvalRef}
                  layout
                  initial={{ opacity: 0, y: 8 }}
                  animate={{ opacity: 1, y: 0 }}
                  exit={{ opacity: 0, x: -100, height: 0, marginBottom: 0, overflow: "hidden" }}
                  transition={{ duration: 0.3 }}
                  className={cn(
                    "instrument-card group cursor-pointer overflow-hidden border-border/70 bg-surface-1/95 focus:outline-none focus:ring-1 focus:ring-cordum",
                    approval.status === "pending" && !expired && statusToneBorderClasses.warning,
                    approval.status === "rejected" && statusToneBorderClasses.governance,
                    approval.status === "invalidated" && "border-destructive/30",
                  )}
                  role="button"
                  tabIndex={0}
                  aria-label={`Open edge approval detail for ${title}`}
                  onClick={() => setSelected(approval)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      setSelected(approval);
                    }
                  }}
                >
                  <div className="flex flex-col gap-4 p-4 md:p-5">
                    <div className="flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
                      <div className="min-w-0 flex-1 space-y-2">
                        <div className="flex flex-wrap items-center gap-2">
                          <StatusBadge
                            variant={approvalStatusVariant(approval.status) as BadgeVariant}
                            dot
                            pulse={actionable}
                          >
                            {formatEdgeStatusLabel(approval.status)}
                          </StatusBadge>
                          <StatusBadge variant="info">Edge · Claude Code</StatusBadge>
                          {expired && approval.status === "pending" && (
                            <StatusBadge variant="danger">Expired</StatusBadge>
                          )}
                        </div>
                        <p className="text-sm font-medium text-foreground">{title}</p>
                        <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
                          {(approval.requester || approval.principalId) && (
                            <span>
                              Requester:{" "}
                              <span className="font-mono text-foreground">
                                {approval.requester || approval.principalId}
                              </span>
                            </span>
                          )}
                          {approval.ruleId && (
                            <span>
                              Rule:{" "}
                              <span className="font-mono text-foreground">{approval.ruleId}</span>
                            </span>
                          )}
                          {approval.createdAt && (
                            <span>{formatRelativeTime(approval.createdAt)}</span>
                          )}
                          {approval.expiresAt && approval.status === "pending" && (
                            <span className={cn(expired && "text-destructive")}>
                              Expires {formatRelativeTime(approval.expiresAt)}
                            </span>
                          )}
                        </div>
                      </div>

                      {actionable ? (
                        <div className="flex shrink-0 flex-wrap gap-2 xl:flex-col xl:items-stretch">
                          <Button
                            size="sm"
                            variant="danger"
                            aria-label={`Deny ${title}`}
                            disabled={rejectMutation.isPending || approveMutation.isPending}
                            loading={rejectMutation.isPending}
                            onClick={(e) => {
                              e.stopPropagation();
                              setDenyTarget(approval);
                              setDenyReason("");
                            }}
                          >
                            <XCircle className="mr-1 h-3.5 w-3.5" />
                            Deny
                          </Button>
                          <Button
                            size="sm"
                            variant="primary"
                            aria-label={`Approve ${title}`}
                            disabled={approveMutation.isPending || rejectMutation.isPending}
                            loading={approveMutation.isPending}
                            onClick={(e) => {
                              e.stopPropagation();
                              handleApprove(approval);
                            }}
                          >
                            <CheckCircle2 className="mr-1 h-3.5 w-3.5" />
                            Approve
                          </Button>
                        </div>
                      ) : (
                        <div className="shrink-0 pt-1 text-muted-foreground transition-colors group-hover:text-cordum">
                          <ArrowRight className="h-4 w-4" />
                        </div>
                      )}
                    </div>
                  </div>
                </motion.article>
              );
            })}
          </AnimatePresence>
        </div>
      )}

      <ConfirmDialog
        open={!!denyTarget}
        onClose={() => setDenyTarget(null)}
        onConfirm={() => handleDeny(denyReason)}
        title="Deny edge action"
        description={
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              Explain why this governed action should be denied. The reason becomes part of the audit trail.
            </p>
            <div>
              <label className="mb-1 block text-xs font-mono uppercase tracking-wide text-muted-foreground">
                Reason <span className="text-destructive">*</span>
              </label>
              <Textarea
                value={denyReason}
                onChange={(e) => {
                  if (e.target.value.length <= 500) setDenyReason(e.target.value);
                }}
                placeholder="Why should this action be denied?"
                rows={3}
                maxLength={500}
                aria-required="true"
                aria-label="Denial reason"
                className="resize-none bg-surface-2 px-3 py-2 shadow-none focus:ring-cordum/30"
              />
              <p
                className={cn(
                  "mt-1 text-right text-xs",
                  denyReason.length > 400 ? "text-warning" : "text-muted-foreground",
                  denyReason.length >= 500 && "text-destructive",
                )}
              >
                {denyReason.length} / 500
              </p>
            </div>
          </div>
        }
        confirmLabel={denyReason.trim() ? "Deny" : "Enter reason to deny"}
        variant="destructive"
        loading={rejectMutation.isPending}
        initialFocusSelector='textarea[aria-label="Denial reason"]'
      />

      {selected && (
        <DetailDrawer
          approval={selected}
          onClose={() => setSelected(null)}
          drawerRef={drawerRef}
          onApprove={handleApprove}
          onDeny={(a) => {
            setDenyTarget(a);
            setDenyReason("");
          }}
          approving={approveMutation.isPending}
          denying={rejectMutation.isPending}
        />
      )}
    </div>
  );
}
