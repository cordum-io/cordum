/*
 * DESIGN: "Decision Briefing" — Full-context approval detail page.
 * Six structured sections presenting everything an approver needs to make a
 * confident decision: What, Why Blocked, Blast Radius, Risk, History, Rollback.
 * Action bar pinned at bottom with keyboard shortcuts (a/r).
 */
import { useState, useCallback, useMemo, useEffect } from "react";
import { useParams, useNavigate, Link } from "react-router-dom";
import { motion } from "framer-motion";
import {
  ArrowLeft,
  Shield,
  AlertTriangle,
  Target,
  Clock,
  History,
  RotateCcw,
  CheckCircle2,
  XCircle,
  ChevronDown,
  ChevronRight,
  FileText,
  Server,
  Layers,
  Box,
  Info,
} from "lucide-react";
import { toast } from "sonner";
import { cn, formatRelativeTime } from "@/lib/utils";
import {
  useApproval,
  useApprovalContext,
  useApproveJob,
  useRejectJob,
} from "@/hooks/useApprovals";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { CodeBlock } from "@/components/ui/CodeBlock";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Skeleton } from "@/components/ui/Skeleton";
import { friendlyError } from "@/lib/friendlyError";
import type {
  ApprovalContext,
  BlastRadius,
  PriorApproval,
  ApprovalPolicySnapshot,
} from "@/api/types";

// ---------------------------------------------------------------------------
// Section wrapper — consistent card styling with independent states
// ---------------------------------------------------------------------------

interface SectionProps {
  icon: React.ElementType;
  title: string;
  badge?: string;
  badgeVariant?: "default" | "warning" | "destructive" | "muted";
  children: React.ReactNode;
  className?: string;
  defaultOpen?: boolean;
}

function Section({
  icon: Icon,
  title,
  badge,
  badgeVariant = "default",
  children,
  className,
  defaultOpen = true,
}: SectionProps) {
  const [open, setOpen] = useState(defaultOpen);
  const badgeColors: Record<string, string> = {
    default: "bg-muted text-muted-foreground",
    warning:
      "bg-amber-500/10 text-amber-600 dark:text-amber-400 border border-amber-500/20",
    destructive:
      "bg-red-500/10 text-red-600 dark:text-red-400 border border-red-500/20",
    muted: "bg-muted text-muted-foreground",
  };

  return (
    <div
      className={cn(
        "rounded-lg border border-border/60 bg-card/80 backdrop-blur-sm overflow-hidden",
        className,
      )}
    >
      <button
        type="button"
        className="flex w-full items-center gap-3 px-5 py-4 text-left hover:bg-muted/30 transition-colors"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
      >
        <Icon className="h-4 w-4 text-muted-foreground shrink-0" />
        <span className="text-sm font-semibold tracking-tight flex-1">
          {title}
        </span>
        {badge && (
          <span
            className={cn(
              "text-xs px-2 py-0.5 rounded-full font-medium",
              badgeColors[badgeVariant],
            )}
          >
            {badge}
          </span>
        )}
        {open ? (
          <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
        ) : (
          <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
        )}
      </button>
      {open && (
        <div className="border-t border-border/40 px-5 py-4">{children}</div>
      )}
    </div>
  );
}

function SectionSkeleton() {
  return (
    <div className="rounded-lg border border-border/60 bg-card/80 p-5 space-y-3">
      <Skeleton className="h-4 w-32" />
      <Skeleton className="h-3 w-full" />
      <Skeleton className="h-3 w-3/4" />
    </div>
  );
}

function SectionEmpty({ message }: { message: string }) {
  return (
    <p className="text-sm text-muted-foreground italic py-1">{message}</p>
  );
}

// ---------------------------------------------------------------------------
// Section: What
// ---------------------------------------------------------------------------

function WhatSection({
  context,
  approval,
}: {
  context: ApprovalContext;
  approval: Record<string, unknown>;
}) {
  const [jsonOpen, setJsonOpen] = useState(false);
  const summary = approval.decision_summary as
    | Record<string, unknown>
    | undefined;
  const title =
    (summary?.title as string) || (approval.topic as string) || "Unnamed action";
  const topic = (approval.topic as string) || "unknown";
  const tenant = (approval.tenant as string) || "";
  const jobInput = approval.job_input as Record<string, unknown> | undefined;

  return (
    <Section icon={FileText} title="What" badge={topic}>
      <div className="space-y-3">
        <div>
          <h3 className="text-base font-semibold text-foreground">{title}</h3>
          {(summary?.subject as string) &&
            (summary?.subject as string) !== title && (
              <p className="text-sm text-muted-foreground mt-0.5">
                {summary?.subject as string}
              </p>
            )}
        </div>
        <div className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-muted-foreground">
          <span>
            <span className="font-medium text-foreground/70">Topic:</span>{" "}
            {topic}
          </span>
          {tenant && (
            <span>
              <span className="font-medium text-foreground/70">Tenant:</span>{" "}
              {tenant}
            </span>
          )}
        </div>
        {jobInput && Object.keys(jobInput).length > 0 && (
          <div>
            <button
              type="button"
              className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
              onClick={() => setJsonOpen(!jsonOpen)}
            >
              {jsonOpen ? (
                <ChevronDown className="h-3 w-3" />
              ) : (
                <ChevronRight className="h-3 w-3" />
              )}
              Job payload
            </button>
            {jsonOpen && (
              <div className="mt-2">
                <CodeBlock language="json">
                  {JSON.stringify(jobInput, null, 2)}
                </CodeBlock>
              </div>
            )}
          </div>
        )}
      </div>
    </Section>
  );
}

// ---------------------------------------------------------------------------
// Section: Why Blocked
// ---------------------------------------------------------------------------

function WhyBlockedSection({
  context,
}: {
  context: ApprovalContext;
}) {
  const pss = context.policySnapshotSummary;
  const rule = pss.matchedRule;

  return (
    <Section
      icon={Shield}
      title="Why blocked"
      badge={rule.id || "policy"}
      badgeVariant="warning"
    >
      <div className="space-y-3">
        {rule.id ? (
          <>
            <div className="flex items-start gap-3">
              <div className="mt-0.5 h-2 w-2 rounded-full bg-amber-500 shrink-0" />
              <div>
                <p className="text-sm font-medium text-foreground">
                  Rule: {rule.id}
                </p>
                <p className="text-sm text-muted-foreground mt-0.5">
                  {rule.description || "No description available"}
                </p>
              </div>
            </div>
            <div className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-muted-foreground">
              <span>
                <span className="font-medium text-foreground/70">
                  Decision:
                </span>{" "}
                {rule.decision || "require_approval"}
              </span>
              {pss.policyVersion && (
                <span>
                  <span className="font-medium text-foreground/70">
                    Policy version:
                  </span>{" "}
                  <code className="font-mono text-[11px]">
                    {pss.policyVersion}
                  </code>
                </span>
              )}
              {rule.constraintsSummary && (
                <span>
                  <span className="font-medium text-foreground/70">
                    Constraints:
                  </span>{" "}
                  {rule.constraintsSummary}
                </span>
              )}
            </div>
          </>
        ) : (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Info className="h-4 w-4" />
            <span>
              Approval required by policy (v{pss.policyVersion || "unknown"})
            </span>
          </div>
        )}
      </div>
    </Section>
  );
}

// ---------------------------------------------------------------------------
// Section: Blast Radius
// ---------------------------------------------------------------------------

function BlastRadiusSection({ blastRadius }: { blastRadius: BlastRadius }) {
  const empty =
    blastRadius.systems.length === 0 &&
    blastRadius.namespaces.length === 0 &&
    blastRadius.resources.length === 0;

  const groups = [
    { label: "Systems", icon: Server, items: blastRadius.systems },
    { label: "Namespaces", icon: Layers, items: blastRadius.namespaces },
    { label: "Resources", icon: Box, items: blastRadius.resources },
  ].filter((g) => g.items.length > 0);

  return (
    <Section
      icon={Target}
      title="Blast radius"
      badge={
        empty
          ? undefined
          : `${blastRadius.systems.length + blastRadius.namespaces.length + blastRadius.resources.length} affected`
      }
      badgeVariant={empty ? "muted" : "destructive"}
    >
      {empty ? (
        <SectionEmpty message="No blast radius data available from pack." />
      ) : (
        <div className="space-y-3">
          {blastRadius.scopeDescription && (
            <p className="text-sm text-muted-foreground">
              {blastRadius.scopeDescription}
            </p>
          )}
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
            {groups.map((group) => (
              <div key={group.label} className="space-y-1.5">
                <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                  <group.icon className="h-3 w-3" />
                  {group.label}
                </div>
                <ul className="space-y-0.5">
                  {group.items.map((item, i) => (
                    <li
                      key={i}
                      className="text-sm font-mono text-foreground/90 pl-4 border-l-2 border-border/60"
                    >
                      {item}
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </div>
      )}
    </Section>
  );
}

// ---------------------------------------------------------------------------
// Section: Risk
// ---------------------------------------------------------------------------

function RiskSection({ context }: { context: ApprovalContext }) {
  const constraints = context.constraints;
  const timeRemainingMs = context.timeRemainingMs;
  const hasConstraints =
    constraints != null && Object.keys(constraints).length > 0;
  const isTimeLimited = timeRemainingMs != null && timeRemainingMs >= 0;
  const isCritical = isTimeLimited && timeRemainingMs < 60_000;
  const isExpired = isTimeLimited && timeRemainingMs === 0;

  return (
    <Section
      icon={AlertTriangle}
      title="Risk"
      badge={
        isExpired
          ? "Expired"
          : isCritical
            ? "Critical"
            : isTimeLimited
              ? "Time-limited"
              : undefined
      }
      badgeVariant={
        isExpired || isCritical ? "destructive" : isTimeLimited ? "warning" : "muted"
      }
    >
      <div className="space-y-3">
        {isTimeLimited && (
          <div
            className={cn(
              "flex items-center gap-2 text-sm px-3 py-2 rounded-md",
              isCritical || isExpired
                ? "bg-red-500/10 text-red-600 dark:text-red-400"
                : "bg-amber-500/10 text-amber-600 dark:text-amber-400",
            )}
          >
            <Clock className="h-4 w-4 shrink-0" />
            {isExpired ? (
              <span className="font-medium">Approval window has expired</span>
            ) : (
              <span>
                <span className="font-medium">
                  {formatTimeRemaining(timeRemainingMs)}
                </span>{" "}
                remaining
              </span>
            )}
          </div>
        )}
        {hasConstraints ? (
          <div>
            <p className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-1.5">
              Active constraints
            </p>
            <CodeBlock language="json">
              {JSON.stringify(constraints, null, 2)}
            </CodeBlock>
          </div>
        ) : (
          !isTimeLimited && (
            <SectionEmpty message="No additional risk constraints." />
          )
        )}
      </div>
    </Section>
  );
}

export function formatTimeRemaining(ms: number): string {
  if (ms <= 0) return "0s";
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
}

// ---------------------------------------------------------------------------
// Section: History (Prior Approvals)
// ---------------------------------------------------------------------------

function HistorySection({
  priorApprovals,
}: {
  priorApprovals: PriorApproval[];
}) {
  return (
    <Section
      icon={History}
      title="History"
      badge={
        priorApprovals.length > 0
          ? `${priorApprovals.length} prior`
          : undefined
      }
    >
      {priorApprovals.length === 0 ? (
        <SectionEmpty message="No prior approvals for this topic." />
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm" role="table">
            <thead>
              <tr className="text-xs text-muted-foreground uppercase tracking-wider border-b border-border/40">
                <th className="text-left py-2 pr-4 font-medium">Job</th>
                <th className="text-left py-2 pr-4 font-medium">Resolved by</th>
                <th className="text-left py-2 pr-4 font-medium">Outcome</th>
                <th className="text-left py-2 font-medium">When</th>
              </tr>
            </thead>
            <tbody>
              {priorApprovals.map((pa, i) => (
                <tr
                  key={pa.jobId || i}
                  className="border-b border-border/20 last:border-0"
                >
                  <td className="py-2 pr-4 font-mono text-xs text-foreground/80">
                    {pa.jobId ? (
                      <Link
                        to={`/jobs/${pa.jobId}`}
                        className="hover:underline"
                      >
                        {pa.jobId.slice(0, 12)}...
                      </Link>
                    ) : (
                      "—"
                    )}
                  </td>
                  <td className="py-2 pr-4 text-foreground/70">
                    {pa.resolvedBy || "—"}
                  </td>
                  <td className="py-2 pr-4">
                    {pa.wasApproved ? (
                      <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400">
                        <CheckCircle2 className="h-3 w-3" />
                        Approved
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-red-500">
                        <XCircle className="h-3 w-3" />
                        Rejected
                      </span>
                    )}
                  </td>
                  <td className="py-2 text-muted-foreground text-xs">
                    {pa.resolvedAt
                      ? formatRelativeTime(
                          new Date(pa.resolvedAt / 1000).toISOString(),
                        )
                      : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Section>
  );
}

// ---------------------------------------------------------------------------
// Section: Rollback
// ---------------------------------------------------------------------------

function RollbackSection({ rollbackHint }: { rollbackHint: string }) {
  return (
    <Section icon={RotateCcw} title="Rollback">
      {rollbackHint ? (
        <div className="text-sm text-foreground/90 whitespace-pre-wrap font-mono bg-muted/40 rounded-md px-3 py-2.5">
          {rollbackHint}
        </div>
      ) : (
        <SectionEmpty message="No rollback instructions provided by pack." />
      )}
    </Section>
  );
}

// ---------------------------------------------------------------------------
// Testable utilities
// ---------------------------------------------------------------------------

/** Returns true when the blast radius contains zero items in all categories. */
export function isBlastRadiusEmpty(br: BlastRadius): boolean {
  return (
    br.systems.length === 0 &&
    br.namespaces.length === 0 &&
    br.resources.length === 0
  );
}

/** Total affected count across all blast radius categories. */
export function blastRadiusCount(br: BlastRadius): number {
  return br.systems.length + br.namespaces.length + br.resources.length;
}

/** Returns true when the keyboard shortcut target should be suppressed (e.g. input focused). */
export function isShortcutSuppressed(target: EventTarget | null): boolean {
  if (!target || !(target instanceof HTMLElement)) return false;
  const tag = target.tagName?.toLowerCase();
  if (tag === "input" || tag === "textarea" || tag === "select") return true;
  return target.contentEditable === "true";
}

/** Keyboard shortcut mapping for the approval detail page. */
export const APPROVAL_SHORTCUTS = {
  approve: "a",
  reject: "r",
  close: "Escape",
} as const;

// ---------------------------------------------------------------------------
// Main page component
// ---------------------------------------------------------------------------

export default function ApprovalDetailPage() {
  const { jobId } = useParams<{ jobId: string }>();
  const navigate = useNavigate();

  const {
    data: approval,
    isLoading: approvalLoading,
    isError: approvalError,
  } = useApproval(jobId ?? "");
  const {
    data: context,
    isLoading: contextLoading,
    isError: contextError,
    refetch: refetchContext,
  } = useApprovalContext(jobId ?? "");

  const approveMutation = useApproveJob();
  const rejectMutation = useRejectJob();

  const [approveDialogOpen, setApproveDialogOpen] = useState(false);
  const [rejectDialogOpen, setRejectDialogOpen] = useState(false);
  const [rejectReason, setRejectReason] = useState("");

  const isPending = approval?.status === "pending";

  // Screen reader live region announcements.
  const [srAnnouncement, setSrAnnouncement] = useState("");

  // Keyboard shortcuts: a = approve, r = reject, Escape = close dialogs.
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      // Don't fire when typing in inputs, textareas, or contentEditable.
      if (isShortcutSuppressed(e.target)) {
        return;
      }

      if (e.key === "Escape") {
        setApproveDialogOpen(false);
        setRejectDialogOpen(false);
        return;
      }

      if (!isPending) return;

      if (e.key === "a" || e.key === "A") {
        e.preventDefault();
        setApproveDialogOpen(true);
      }
      if (e.key === "r" || e.key === "R") {
        e.preventDefault();
        setRejectDialogOpen(true);
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isPending]);

  const handleApprove = useCallback(() => {
    if (!jobId || approveMutation.isPending) return;
    approveMutation.mutate(
      { jobId },
      {
        onSuccess: () => {
          toast.success("Approval granted", {
            description: `Job ${jobId} has been approved.`,
          });
          setSrAnnouncement(`Job ${jobId} approved successfully.`);
          setApproveDialogOpen(false);
        },
        onError: (err) => {
          const friendly = friendlyError(err, "approve job");
          toast.error(friendly.title, { description: friendly.description });
        },
      },
    );
  }, [jobId, approveMutation]);

  const handleReject = useCallback(() => {
    if (!jobId || rejectMutation.isPending) return;
    rejectMutation.mutate(
      { jobId, reason: rejectReason },
      {
        onSuccess: () => {
          toast.success("Approval rejected", {
            description: `Job ${jobId} has been rejected.`,
          });
          setSrAnnouncement(`Job ${jobId} rejected.`);
          setRejectDialogOpen(false);
          setRejectReason("");
        },
        onError: (err) => {
          const friendly = friendlyError(err, "reject job");
          toast.error(friendly.title, { description: friendly.description });
        },
      },
    );
  }, [jobId, rejectReason, rejectMutation]);

  // Merge approval data with context.approval for the What section.
  const mergedApproval = useMemo(() => {
    const base = (context?.approval ?? {}) as Record<string, unknown>;
    if (approval) {
      return {
        ...base,
        topic: approval.topic ?? base.topic,
        tenant: approval.tenant ?? base.tenant,
        decision_summary: approval.decisionSummary ?? base.decision_summary,
        job_input: approval.jobInput ?? base.job_input,
      };
    }
    return base;
  }, [context, approval]);

  const title = approval?.humanSummary || approval?.decisionSummary?.title || "Approval Detail";

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title={title}
        breadcrumbs={[
          { label: "Approvals", href: "/approvals" },
          { label: jobId?.slice(0, 12) ?? "..." },
        ]}
        actions={
          <Button
            variant="ghost"
            size="sm"
            onClick={() => navigate("/approvals")}
            aria-label="Back to approvals"
          >
            <ArrowLeft className="h-4 w-4 mr-1.5" />
            Back
          </Button>
        }
      />

      <div className="flex-1 overflow-y-auto">
        <div className="max-w-4xl mx-auto px-4 py-6 space-y-4">
          {/* Status bar */}
          {approval && (
            <motion.div
              initial={{ opacity: 0, y: -8 }}
              animate={{ opacity: 1, y: 0 }}
              className="flex flex-wrap items-center gap-3"
            >
              <StatusBadge
                variant={
                  approval.status === "pending"
                    ? "warning"
                    : approval.status === "approved"
                      ? "healthy"
                      : approval.status === "rejected"
                        ? "danger"
                        : "muted"
                }
                dot
                pulse={approval.status === "pending"}
              >
                {approval.status}
              </StatusBadge>
              {approval.requestedAt && (
                <span className="text-xs text-muted-foreground">
                  Requested {formatRelativeTime(approval.requestedAt)}
                </span>
              )}
              {approval.actor && (
                <span className="text-xs text-muted-foreground">
                  Resolved by {approval.actor}
                </span>
              )}
              <span className="text-xs font-mono text-muted-foreground/60">
                {jobId}
              </span>
            </motion.div>
          )}

          {approvalLoading && (
            <div className="space-y-4">
              <SectionSkeleton />
              <SectionSkeleton />
              <SectionSkeleton />
            </div>
          )}

          {approvalError && (
            <ErrorBanner
              title="Failed to load approval"
              message="The approval could not be found or an error occurred."
            />
          )}

          {contextError && !contextLoading && (
            <ErrorBanner
              title="Failed to load approval context"
              message="Enriched context is unavailable. Core approval data may still be shown."
              onRetry={() => refetchContext()}
            />
          )}

          {/* 6 Context Sections */}
          {contextLoading && !context ? (
            <div className="space-y-4">
              {[...Array(6)].map((_, i) => (
                <SectionSkeleton key={i} />
              ))}
            </div>
          ) : context ? (
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              transition={{ duration: 0.2 }}
              className="space-y-3"
            >
              {/* a. What */}
              <WhatSection context={context} approval={mergedApproval} />

              {/* b. Why Blocked */}
              <WhyBlockedSection context={context} />

              {/* c. Blast Radius */}
              <BlastRadiusSection blastRadius={context.blastRadius} />

              {/* d. Risk */}
              <RiskSection context={context} />

              {/* e. History */}
              <HistorySection priorApprovals={context.priorApprovals} />

              {/* f. Rollback */}
              <RollbackSection rollbackHint={context.rollbackHint} />
            </motion.div>
          ) : null}
        </div>
      </div>

      {/* Pinned action bar */}
      {isPending && (
        <div
          className="sticky bottom-0 border-t border-border bg-background/95 backdrop-blur-sm px-4 py-3"
          role="toolbar"
          aria-label="Approval actions"
        >
          <div className="max-w-4xl mx-auto flex items-center justify-between gap-4">
            <p className="text-xs text-muted-foreground hidden sm:block">
              Keyboard:{" "}
              <kbd className="px-1.5 py-0.5 rounded bg-muted text-[10px] font-mono border border-border">
                A
              </kbd>{" "}
              approve{" "}
              <kbd className="px-1.5 py-0.5 rounded bg-muted text-[10px] font-mono border border-border">
                R
              </kbd>{" "}
              reject
            </p>
            <div className="flex items-center gap-3 ml-auto">
              <Button
                variant="outline"
                size="sm"
                onClick={() => setRejectDialogOpen(true)}
                disabled={rejectMutation.isPending}
                className="border-red-500/30 text-red-600 dark:text-red-400 hover:bg-red-500/10"
                aria-label="Reject this approval"
              >
                <XCircle className="h-4 w-4 mr-1.5" />
                Reject
                <span className="ml-2 px-1 py-0.5 rounded bg-muted text-[10px] font-mono border border-border text-muted-foreground">
                  R
                </span>
              </Button>
              <Button
                variant="default"
                size="sm"
                onClick={() => setApproveDialogOpen(true)}
                disabled={approveMutation.isPending}
                className="bg-emerald-600 hover:bg-emerald-700 text-white"
                aria-label="Approve this approval"
              >
                <CheckCircle2 className="h-4 w-4 mr-1.5" />
                Approve
                <span className="ml-2 px-1 py-0.5 rounded bg-emerald-700/50 text-[10px] font-mono border border-emerald-500/30">
                  A
                </span>
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* Approve confirmation dialog */}
      <ConfirmDialog
        open={approveDialogOpen}
        onClose={() => setApproveDialogOpen(false)}
        onConfirm={handleApprove}
        title="Approve this action?"
        description="This will allow the governed change to proceed. Ensure you have reviewed the blast radius and rollback instructions."
        confirmLabel="Approve"
        loading={approveMutation.isPending}
        icon={CheckCircle2}
      />

      {/* Reject dialog with reason */}
      <ConfirmDialog
        open={rejectDialogOpen}
        onClose={() => {
          setRejectDialogOpen(false);
          setRejectReason("");
        }}
        onConfirm={handleReject}
        title="Reject this action?"
        description={
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              This will deny the governed change. Please provide a reason.
            </p>
            <textarea
              className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring resize-none"
              rows={3}
              placeholder="Reason for rejection..."
              value={rejectReason}
              onChange={(e) => setRejectReason(e.target.value)}
              aria-label="Rejection reason"
              autoFocus
            />
          </div>
        }
        confirmLabel="Reject"
        variant="destructive"
        loading={rejectMutation.isPending}
        icon={XCircle}
      />

      {/* Screen reader live region for action results */}
      <div aria-live="polite" aria-atomic="true" className="sr-only">
        {srAnnouncement}
      </div>
    </div>
  );
}
