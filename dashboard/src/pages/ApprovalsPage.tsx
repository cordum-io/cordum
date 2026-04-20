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
import { Input } from "@/components/ui/Input";
import { Textarea } from "@/components/ui/Textarea";
import { Tabs } from "@/components/ui/Tabs";
import { StatTile } from "@/components/ui/StatTile";
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
    <ul
      className={cn("flex flex-wrap gap-2", compact ? "pt-1" : "pt-2")}
      aria-label="Decision facts"
    >
      {facts.map((fact) => (
        <li
          key={`${fact.label}:${fact.value}`}
          className="inline-flex items-center gap-2 rounded-full border border-border bg-surface-2/80 px-3 py-1.5 text-xs"
        >
          <span className="font-mono uppercase tracking-wide text-muted-foreground">
            {fact.label}
          </span>
          <span className="font-medium text-foreground">{fact.value}</span>
        </li>
      ))}
    </ul>
  );
}

function DecisionSummaryBlock({
  approval,
  compact = false,
}: {
  approval: Approval;
  compact?: boolean;
}) {
  const title = getApprovalPrimaryTitle(approval);
  const reason = getApprovalPrimaryReason(approval);
  const escalationReason = getApprovalEscalationReason(approval);
  const contextStatus = approval.decisionSummary?.contextStatus;
  const missingFields = approval.decisionSummary?.missingFields ?? [];
  const TitleTag = compact ? "h3" : "h2";

  return (
    <div className="space-y-3">
      <div className="space-y-1.5">
        <TitleTag
          className={cn(
            "font-display font-semibold leading-tight text-foreground",
            compact ? "text-base" : "text-xl",
          )}
        >
          {title}
        </TitleTag>
        {reason ? (
          <p className="text-sm leading-relaxed text-muted-foreground">
            {reason}
          </p>
        ) : (
          <p className="text-sm text-muted-foreground">
            Review the request details before deciding.
          </p>
        )}
      </div>

      {escalationReason && (
        <div className="flex items-start gap-2 rounded-2xl border border-warning/20 bg-warning/10 px-3 py-2 text-sm text-foreground">
          <Info className="mt-0.5 h-4 w-4 shrink-0 text-warning" />
          <div>
            <p className="text-xs font-mono uppercase tracking-wide text-warning">
              Escalation
            </p>
            <p>{escalationReason}</p>
          </div>
        </div>
      )}

      {contextStatus && contextStatus !== "available" && (
        <div className="rounded-2xl border border-border bg-surface-2/70 px-3 py-2 text-xs text-muted-foreground">
          Approval context is{" "}
          <span className="font-medium text-foreground">
            {contextStatus.replace(/_/g, " ")}
          </span>
          {missingFields.length > 0 && (
            <> — missing {missingFields.join(", ")}.</>
          )}
        </div>
      )}

      <DecisionFacts approval={approval} compact={compact} />
    </div>
  );
}

function SecondaryMetadata({
  approval,
  compact = false,
}: {
  approval: Approval;
  compact?: boolean;
}) {
  const rows = compact
    ? [
        { label: "Approval", value: compactValue(approval.id) },
        { label: "Job", value: compactValue(approval.jobId) },
        { label: "Topic", value: approval.topic },
        { label: "Run", value: compactValue(approval.workflowContext?.runId) },
      ].filter((row) => !!row.value)
    : getApprovalAuditRows(approval);

  if (!rows.length) return null;

  if (compact) {
    return (
      <div className="border-t border-border/70 pt-3">
        <ul className="flex flex-wrap gap-x-4 gap-y-1 text-[11px] font-mono text-muted-foreground">
          {rows.map((row) => (
            <li key={row.label} className="inline-flex items-center gap-1">
              <span className="uppercase tracking-wide">{row.label}</span>
              <span className="text-foreground/80">{row.value}</span>
            </li>
          ))}
        </ul>
      </div>
    );
  }

  return (
    <dl className="grid gap-3 sm:grid-cols-2">
      {rows.map((row) => (
        <div key={row.label} className="space-y-1">
          <dt className="text-[11px] font-mono uppercase tracking-wide text-muted-foreground">
            {row.label}
          </dt>
          <dd className="break-all text-sm text-foreground">{row.value}</dd>
        </div>
      ))}
    </dl>
  );
}

function JsonDisclosure({
  title,
  data,
}: {
  title: string;
  data?: Record<string, unknown>;
}) {
  if (!data || Object.keys(data).length === 0) return null;

  return (
    <details className="rounded-2xl border border-border bg-surface-2/60 p-3">
      <summary className="cursor-pointer list-none text-xs font-mono uppercase tracking-wide text-muted-foreground">
        {title}
      </summary>
      <div className="mt-3">
        <CodeBlock language="json" maxHeight={300}>
          {renderJson(data)}
        </CodeBlock>
      </div>
    </details>
  );
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
    mutate: (input: { jobId: string; reason: string }) => void;
    clearTarget: () => void;
  },
): void {
  if (!target) return;
  deps.mutate({ jobId: target.jobId, reason: resolveDenyReason(rawReason) });
  deps.clearTarget();
}

export default function ApprovalsPage() {
  const navigate = useNavigate();
  const [search, setSearch] = useState("");
  const [activeTab, setActiveTab] = useState("pending");
  const [selectedApproval, setSelectedApproval] = useState<Approval | null>(
    null,
  );
  const [denyTarget, setDenyTarget] = useState<Approval | null>(null);
  const [denyReason, setDenyReason] = useState("");

  const drawerRef = useDialogA11y(() => setSelectedApproval(null), {
    enabled: !!selectedApproval,
    initialFocusSelector: 'button[aria-label="Close approval detail"]',
  });
  const {
    data: approvalsData,
    isLoading,
    isError,
    error,
    refetch,
  } = useApprovals();
  const approvals = approvalsData?.items ?? [];
  const approveMutation = useApproveJob();
  const rejectMutation = useRejectJob();

  const handleApprove = (approval: Approval) => {
    if (approveMutation.isPending) return;
    approveMutation.mutate(
      { jobId: approval.jobId },
      {
        onError: (err) => {
          const errBody = (err as any)?.body ?? (err as any)?.data;
          if (errBody?.code === "self_approval_denied") {
            toast.error("Self-approval not permitted", {
              description:
                "You cannot approve a job you submitted. A different administrator must approve this request.",
            });
          } else {
            const friendly = friendlyError(err, "approve approval");
            toast.error(friendly.title, { description: friendly.description });
          }
        },
      },
    );
  };

  const handleDeny = (approval: Approval, reason: string) => {
    if (rejectMutation.isPending) return;
    rejectMutation.mutate(
      { jobId: approval.jobId, reason },
      {
        onSuccess: () => {
          setDenyTarget(null);
          setDenyReason("");
        },
        onError: (err) => {
          const friendly = friendlyError(err, "reject approval");
          toast.error(friendly.title, { description: friendly.description });
        },
      },
    );
  };

  const all = approvals ?? [];
  const pending = all.filter((a) => a.status === "pending");
  const approved = all.filter((a) => a.status === "approved");
  const denied = all.filter((a) => a.status === "rejected");
  const expired = all.filter((a) => a.status === "expired");
  const invalidated = all.filter((a) => a.status === "invalidated");
  const repaired = all.filter((a) => a.status === "repaired");

  const filtered = useMemo(
    () =>
      all
        .filter((approval) => {
          if (activeTab !== "all" && approval.status !== activeTab)
            return false;
          if (!search.trim()) return true;
          return getApprovalSearchText(approval).includes(
            search.trim().toLowerCase(),
          );
        })
        .sort((a, b) => {
          const aActionable = isApprovalActionable(a);
          const bActionable = isApprovalActionable(b);
          if (aActionable && !bActionable) return -1;
          if (bActionable && !aActionable) return 1;
          if (a.status === "pending" && b.status !== "pending") return -1;
          if (b.status === "pending" && a.status !== "pending") return 1;
          return (
            new Date(b.requestedAt ?? 0).getTime() -
            new Date(a.requestedAt ?? 0).getTime()
          );
        }),
    [activeTab, all, search],
  );

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

      {/* MCP tool-call approvals — surfaced above job approvals so
          operators see the high-privilege agent-driven calls before
          the routine job queue. Each card shows tool_name + requester
          + args-preview (modal) + approve/reject. */}
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
            <StatTile
              accent={pending.length > 0 ? "warning" : "muted"}
              label="Pending"
              value={pending.length}
              helperText={
                pending.length > 0 ? "Needs operator review" : "Queue clear"
              }
              icon={
                <Clock
                  className={cn(
                    "h-4 w-4",
                    pending.length > 0
                      ? "text-warning"
                      : "text-muted-foreground",
                  )}
                />
              }
            />

            <StatTile
              accent="healthy"
              label="Approved"
              value={approved.length}
              helperText="Resolved successfully"
              icon={
                <CheckCircle2 className="h-4 w-4 text-success" />
              }
            />

            <StatTile
              accent={denied.length > 0 ? "governance" : "muted"}
              label="Denied"
              value={denied.length}
              helperText={
                denied.length > 0 ? "Review denial reasons" : "No denied items"
              }
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

            <StatTile
              accent="muted"
              label="Expired"
              value={expired.length}
              helperText="Timed out before decision"
              icon={<Timer className="h-4 w-4 text-muted-foreground" />}
            />

            <StatTile
              accent={invalidated.length > 0 ? "danger" : "muted"}
              label="Invalidated"
              value={invalidated.length}
              helperText={
                invalidated.length > 0
                  ? "Requests drifted after creation"
                  : "No invalidated requests"
              }
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

            <StatTile
              accent={repaired.length > 0 ? "cordum" : "muted"}
              label="Repaired"
              value={repaired.length}
              helperText={
                repaired.length > 0 ? "Legacy rows normalized" : "No repairs"
              }
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
          </>
        )}
      </motion.div>

      <InstrumentCard className="p-4">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <div className="w-full max-w-md">
            <Input
              type="text"
              aria-label="Search approvals"
              icon={<Search className="h-3.5 w-3.5" />}
              placeholder="Search decision summaries, vendors, workflow steps, or IDs"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="h-10 bg-surface-1"
            />
          </div>
          <Tabs
            ariaLabel="Approval status filters"
            variant="segmented"
            className="w-full lg:w-auto"
            activeTab={activeTab}
            onChange={setActiveTab}
            tabs={[
              { id: "pending", label: "Pending", count: pending.length },
              { id: "approved", label: "Approved", count: approved.length },
              { id: "rejected", label: "Denied", count: denied.length },
              { id: "expired", label: "Expired", count: expired.length },
              {
                id: "invalidated",
                label: "Invalidated",
                count: invalidated.length,
              },
              { id: "repaired", label: "Repaired", count: repaired.length },
              { id: "all", label: "All", count: all.length },
            ]}
          />
        </div>
      </InstrumentCard>

      {isLoading ? (
        <SkeletonTable rows={5} />
      ) : isError ? (
        <EmptyState
          icon={<XCircle className="h-5 w-5" />}
          title="Approval queue unavailable"
          description={
            error instanceof Error
              ? error.message
              : "The approval queue could not be loaded."
          }
          action={
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              Try again
            </Button>
          }
        />
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<UserCheck className="h-5 w-5" />}
          title={
            activeTab === "pending"
              ? "No pending approvals"
              : "No approvals found"
          }
          description={
            activeTab === "pending"
              ? "Approvals are triggered when a job matches a require_approval rule in your input policy."
              : "Try adjusting your search terms or status filter."
          }
          action={
            activeTab === "pending" ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => navigate("/govern/overview?tab=input-rules")}
              >
                View input rules
              </Button>
            ) : undefined
          }
        />
      ) : (
        <div className="space-y-3">
          <AnimatePresence mode="popLayout">
            {filtered.map((approval) => {
              const source = getApprovalSourceMeta(approval);
              const title = getApprovalPrimaryTitle(approval);
              const impact = getApprovalImpactText(approval);
              const actionable = isApprovalActionable(approval);
              const lifecycleNote = getApprovalLifecycleNote(approval);

              return (
                <motion.article
                  key={approval.id}
                  layout
                  initial={{ opacity: 0, y: 8 }}
                  animate={{ opacity: 1, y: 0 }}
                  exit={{
                    opacity: 0,
                    x: -100,
                    height: 0,
                    marginBottom: 0,
                    overflow: "hidden",
                  }}
                  transition={{ duration: 0.3 }}
                  className={cn(
                    "instrument-card group cursor-pointer overflow-hidden border-border/70 bg-surface-1/95 focus:outline-none focus:ring-1 focus:ring-cordum",
                    approval.status === "pending" &&
                      "border-[var(--color-warning)]/30",
                    approval.status === "rejected" &&
                      "border-[var(--color-governance)]/30",
                    approval.status === "invalidated" &&
                      "border-destructive/30",
                    approval.status === "repaired" &&
                      "border-cordum/30",
                  )}
                  role="button"
                  tabIndex={0}
                  aria-label={`Open approval detail for ${title}`}
                  onClick={() => navigate(`/approvals/${approval.jobId}`)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ") {
                      event.preventDefault();
                      navigate(`/approvals/${approval.jobId}`);
                    }
                  }}
                >
                  <div className="flex flex-col gap-4 p-4 md:p-5">
                    <div className="flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
                      <div className="min-w-0 flex-1 space-y-3">
                        <div className="flex flex-wrap items-center gap-2">
                          <StatusBadge
                            variant={approvalStatusVariant(approval.status)}
                            dot
                            pulse={actionable}
                          >
                            {formatApprovalStatusLabel(approval.status)}
                          </StatusBadge>
                          <StatusBadge variant={source.variant}>
                            {source.label}
                          </StatusBadge>
                          {approval.actionability && !actionable && (
                            <StatusBadge variant="muted">
                              {approval.actionability.replace(/_/g, " ")}
                            </StatusBadge>
                          )}
                          <span className="text-xs text-muted-foreground">
                            {approval.requestedAt
                              ? formatRelativeTime(approval.requestedAt)
                              : "—"}
                          </span>
                        </div>

                        <DecisionSummaryBlock approval={approval} compact />
                        <p className="text-xs text-muted-foreground">
                          {impact}
                        </p>
                        {lifecycleNote && (
                          <p className="text-xs text-muted-foreground">
                            {lifecycleNote}
                          </p>
                        )}
                      </div>

                      {actionable ? (
                        <div className="flex shrink-0 flex-wrap gap-2 xl:flex-col xl:items-stretch">
                          <Button
                            size="sm"
                            variant="danger"
                            aria-label={`Deny ${title}`}
                            disabled={
                              rejectMutation.isPending ||
                              approveMutation.isPending
                            }
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
                            disabled={
                              approveMutation.isPending ||
                              rejectMutation.isPending
                            }
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

                    <SecondaryMetadata approval={approval} compact />
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
        onConfirm={() => {
          if (!denyTarget) return;
          handleDeny(denyTarget, resolveDenyReason(denyReason));
        }}
        title="Deny approval"
        description={
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              Explain why this approval should be denied. The note becomes part
              of the audit trail.
            </p>
            <div>
              <label className="mb-1 block text-xs font-mono uppercase tracking-wide text-muted-foreground">
                Reason <span className="text-destructive">*</span>
              </label>
              <Textarea
                value={denyReason}
                onChange={(e) => {
                  if (e.target.value.length <= 500)
                    setDenyReason(e.target.value);
                }}
                placeholder="Why should this request be denied?"
                rows={3}
                maxLength={500}
                aria-required="true"
                aria-label="Denial reason"
                className="resize-none bg-surface-2 px-3 py-2 shadow-none focus:ring-cordum/30"
              />
              <p
                className={cn(
                  "text-xs mt-1 text-right",
                  denyReason.length > 400
                    ? "text-warning"
                    : "text-muted-foreground",
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

      {selectedApproval && (
        <>
          <div
            className="fixed inset-0 z-40 bg-black/40"
            onClick={() => setSelectedApproval(null)}
          />
          <motion.div
            ref={drawerRef}
            role="dialog"
            aria-modal="true"
            aria-labelledby="approval-drawer-title"
            initial={{ x: 440 }}
            animate={{ x: 0 }}
            transition={{ type: "spring", stiffness: 300, damping: 30 }}
            className="fixed inset-y-0 right-0 z-50 w-full max-w-[560px] overflow-y-auto border-l border-border bg-surface-1 shadow-2xl"
          >
            <div className="flex items-start justify-between gap-4 border-b border-border p-5">
              <div className="space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  <StatusBadge
                    variant={approvalStatusVariant(selectedApproval.status)}
                    dot
                    pulse={isApprovalActionable(selectedApproval)}
                  >
                    {formatApprovalStatusLabel(selectedApproval.status)}
                  </StatusBadge>
                  <StatusBadge
                    variant={getApprovalSourceMeta(selectedApproval).variant}
                  >
                    {getApprovalSourceMeta(selectedApproval).label}
                  </StatusBadge>
                  {selectedApproval.actionability &&
                    !isApprovalActionable(selectedApproval) && (
                      <StatusBadge variant="muted">
                        {selectedApproval.actionability.replace(/_/g, " ")}
                      </StatusBadge>
                    )}
                </div>
                <div>
                  <h2
                    id="approval-drawer-title"
                    className="font-display text-lg font-semibold text-foreground"
                  >
                    {getApprovalPrimaryTitle(selectedApproval)}
                  </h2>
                  <p className="mt-0.5 text-xs font-mono text-muted-foreground">
                    {selectedApproval.id}
                  </p>
                </div>
              </div>
              <button
                type="button"
                aria-label="Close approval detail"
                onClick={() => setSelectedApproval(null)}
                className="flex items-center justify-center min-w-[44px] min-h-[44px] -mr-2 rounded-full text-muted-foreground transition-colors hover:bg-surface-2 hover:text-foreground"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="space-y-6 p-5">
              <section
                aria-labelledby="approval-decision-section"
                className="space-y-4"
              >
                <p
                  id="approval-decision-section"
                  className="text-xs font-mono uppercase tracking-wide text-muted-foreground"
                >
                  Decision summary
                </p>
                <DecisionSummaryBlock approval={selectedApproval} />
                {getApprovalLifecycleNote(selectedApproval) && (
                  <div className="rounded-2xl border border-border bg-surface-2/70 px-3 py-2 text-xs text-muted-foreground">
                    {getApprovalLifecycleNote(selectedApproval)}
                  </div>
                )}
              </section>

              <section className="rounded-3xl border border-border bg-surface-2/60 p-4">
                <p className="text-xs font-mono uppercase tracking-wide text-muted-foreground">
                  Impact
                </p>
                <div className="mt-3 space-y-3 text-sm">
                  <div>
                    <p className="font-medium text-foreground">If approved</p>
                    <p className="mt-1 text-muted-foreground">
                      {getApprovalImpactText(selectedApproval)}
                    </p>
                  </div>
                  <div>
                    <p className="font-medium text-foreground">If denied</p>
                    <p className="mt-1 text-muted-foreground">
                      {getApprovalRejectImpactText(selectedApproval)}
                    </p>
                  </div>
                </div>
              </section>

              <section
                aria-labelledby="approval-workflow-section"
                className="space-y-3 rounded-3xl border border-border bg-surface-2/40 p-4"
              >
                <p
                  id="approval-workflow-section"
                  className="text-xs font-mono uppercase tracking-wide text-muted-foreground"
                >
                  Workflow & context
                </p>
                <WorkflowContext
                  workflowContext={selectedApproval.workflowContext}
                  nextEffect={getApprovalImpactText(selectedApproval)}
                  rejectEffect={getApprovalRejectImpactText(selectedApproval)}
                />
              </section>

              <section
                aria-labelledby="approval-payload-section"
                className="space-y-3 rounded-3xl border border-border bg-surface-2/30 p-4"
              >
                <p
                  id="approval-payload-section"
                  className="text-xs font-mono uppercase tracking-wide text-muted-foreground"
                >
                  Structured payload
                </p>
                {selectedApproval.decisionSummary?.itemsPreview?.length ? (
                  <div className="space-y-2">
                    <p className="text-sm font-medium text-foreground">
                      Item preview
                    </p>
                    <ul className="space-y-1 text-sm text-muted-foreground">
                      {selectedApproval.decisionSummary.itemsPreview.map(
                        (item) => (
                          <li key={item} className="flex items-start gap-2">
                            <span className="mt-1 h-1.5 w-1.5 rounded-full bg-cordum" />
                            <span>{item}</span>
                          </li>
                        ),
                      )}
                    </ul>
                  </div>
                ) : (
                  <p className="text-sm text-muted-foreground">
                    No additional line-item preview is available for this
                    request.
                  </p>
                )}
                <JsonDisclosure
                  title="Raw request payload"
                  data={selectedApproval.jobInput}
                />
              </section>
              {isApprovalActionable(selectedApproval) && (
                <section
                  aria-label="Approval actions"
                  className="rounded-3xl border border-border bg-surface-2/40 p-4"
                >
                  <div className="grid gap-2 sm:grid-cols-2">
                    <Button
                      variant="primary"
                      className="w-full"
                      aria-label={`Approve ${getApprovalPrimaryTitle(selectedApproval)}`}
                      disabled={
                        approveMutation.isPending || rejectMutation.isPending
                      }
                      loading={approveMutation.isPending}
                      onClick={() => handleApprove(selectedApproval)}
                    >
                      <CheckCircle2 className="mr-1 h-3.5 w-3.5" />
                      Approve
                    </Button>
                    <Button
                      variant="danger"
                      className="w-full"
                      aria-label={`Deny ${getApprovalPrimaryTitle(selectedApproval)}`}
                      disabled={
                        rejectMutation.isPending || approveMutation.isPending
                      }
                      loading={rejectMutation.isPending}
                      onClick={() => {
                        setDenyTarget(selectedApproval);
                        setDenyReason("");
                      }}
                    >
                      <XCircle className="mr-1 h-3.5 w-3.5" />
                      Deny
                    </Button>
                  </div>
                </section>
              )}

              <section
                aria-labelledby="approval-audit-section"
                className="space-y-4 rounded-3xl border border-border bg-surface-2/20 p-4"
              >
                <p
                  id="approval-audit-section"
                  className="text-xs font-mono uppercase tracking-wide text-muted-foreground"
                >
                  Audit & debug detail
                </p>
                <SecondaryMetadata approval={selectedApproval} />
                <JsonDisclosure
                  title="Fallback context payload"
                  data={selectedApproval.jobContext}
                />
              </section>
            </div>
          </motion.div>
        </>
      )}
    </div>
  );
}
