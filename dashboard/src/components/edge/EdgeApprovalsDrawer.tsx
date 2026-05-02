import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, CheckCircle2, Clock3, RefreshCw, ShieldAlert, XCircle } from "lucide-react";
import type { AgentActionEvent, EdgeApproval } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { Drawer } from "@/components/ui/Drawer";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { Textarea } from "@/components/ui/Textarea";
import {
  useApproveEdgeApproval,
  useEdgeApprovals,
  useRejectEdgeApproval,
} from "@/hooks/useEdgeSessions";
import { useConfigStore } from "@/state/config";
import { cn } from "@/lib/utils";
import {
  actionSummary,
  approvalStatusVariant,
  compactHash,
  formatLabel,
  isExpired,
  isNotVisibleError,
  isSelfApproval,
  isTerminal,
  matchingEvent,
  redactedInputPreview,
} from "./edgeApprovalUtils";
interface EdgeApprovalsDrawerProps {
  open: boolean;
  onClose: () => void;
  sessionId: string;
  events?: AgentActionEvent[];
  currentPrincipalId?: string;
}
type DecisionKind = "approve" | "reject";
interface PendingDecision {
  kind: DecisionKind;
  approval: EdgeApproval;
}
function Fact({ label, value }: { label: string; value?: string | null }) {
  if (!value) return null;
  return (
    <div className="min-w-0">
      <div className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">{label}</div>
      <div className="mt-1 break-all font-mono text-xs text-foreground">{value}</div>
    </div>
  );
}
export function EdgeApprovalsDrawer({
  open,
  onClose,
  sessionId,
  events = [],
  currentPrincipalId,
}: EdgeApprovalsDrawerProps) {
  const configuredPrincipal = useConfigStore((state) => state.principalId || state.user?.id || "");
  const principalId = currentPrincipalId ?? configuredPrincipal;
  const approvalsQuery = useEdgeApprovals({ sessionId, limit: 50 });
  const approveMutation = useApproveEdgeApproval();
  const rejectMutation = useRejectEdgeApproval();
  const approvals = approvalsQuery.data?.items ?? [];
  const [selectedRef, setSelectedRef] = useState<string>("");
  const [note, setNote] = useState("");
  const [decision, setDecision] = useState<PendingDecision | null>(null);
  useEffect(() => {
    if (!approvals.length) {
      setSelectedRef("");
      return;
    }
    if (!approvals.some((approval) => approval.approvalRef === selectedRef)) {
      setSelectedRef(approvals[0].approvalRef);
    }
  }, [approvals, selectedRef]);
  const selected = approvals.find((approval) => approval.approvalRef === selectedRef) ?? approvals[0];
  const selectedEvent = useMemo(
    () => (selected ? matchingEvent(selected, events) : undefined),
    [events, selected],
  );
  const pendingCount = approvals.filter((approval) => approval.status === "pending").length;
  const mutationError = approveMutation.error ?? rejectMutation.error;
  const mutationPending = approveMutation.isPending || rejectMutation.isPending;
  const selectedExpired = selected ? isExpired(selected) : false;
  const selectedTerminal = selected ? isTerminal(selected) : false;
  const selectedSelf = selected ? isSelfApproval(selected, principalId) : false;
  const canResolve = Boolean(selected) && !selectedTerminal && !selectedExpired && !selectedSelf;
  const submitDecision = () => {
    if (!decision || mutationPending) return;
    const input = { approvalRef: decision.approval.approvalRef, reason: note };
    const callbacks = { onSuccess: () => setDecision(null) };
    if (decision.kind === "approve") {
      approveMutation.mutate(input, callbacks);
    } else {
      rejectMutation.mutate(input, callbacks);
    }
  };
  return (
    <Drawer open={open} onClose={onClose} size="xl" label="Edge approvals">
      <div className="space-y-5">
        <header className="flex items-start justify-between gap-3">
          <div>
            <p className="text-xs font-medium uppercase tracking-[0.2em] text-cordum">Edge approvals</p>
            <h2 className="mt-1 text-xl font-display font-semibold text-foreground">Governed action review</h2>
            <p className="mt-2 text-sm text-muted-foreground">
              Approve or reject the action hash for this policy snapshot. Approval does not edit command content.
            </p>
          </div>
          <Button variant="outline" size="sm" onClick={() => approvalsQuery.refetch()} loading={approvalsQuery.isFetching}>
            <RefreshCw className="h-3.5 w-3.5" />
            Refresh
          </Button>
        </header>
        {isNotVisibleError(approvalsQuery.error) ? (
          <WarningBlock title="Approval not visible" tone="warning">
            This approval may belong to a different requester, tenant, or terminal action. Refresh the session evidence.
          </WarningBlock>
        ) : approvalsQuery.error ? (
          <WarningBlock title="Unable to load approvals" tone="danger">{approvalsQuery.error.message}</WarningBlock>
        ) : null}
        {approvalsQuery.isLoading ? (
          <SkeletonCard />
        ) : approvals.length === 0 ? (
          <EmptyState title="No approvals" description="This session has no Edge approval requests yet." />
        ) : (
          <div className="grid gap-4 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.2fr)]">
            <ApprovalList approvals={approvals} selectedRef={selected?.approvalRef} onSelect={setSelectedRef} />
            {selected && (
              <ApprovalDetail
                approval={selected}
                event={selectedEvent}
                pendingCount={pendingCount}
                principalId={principalId}
                note={note}
                onNoteChange={setNote}
                canResolve={canResolve}
                mutationError={mutationError}
                onDecision={setDecision}
              />
            )}
          </div>
        )}
      </div>
      <ConfirmDialog
        open={Boolean(decision)}
        onClose={() => setDecision(null)}
        onConfirm={submitDecision}
        title={decision?.kind === "approve" ? "Approve Edge action?" : "Reject Edge action?"}
        description="This records a governance decision for the action hash and policy snapshot only."
        confirmLabel={decision?.kind === "approve" ? "Approve" : "Reject"}
        variant={decision?.kind === "reject" ? "destructive" : "default"}
        loading={mutationPending}
      />
    </Drawer>
  );
}
function ApprovalDetail({
  approval,
  event,
  pendingCount,
  principalId,
  note,
  onNoteChange,
  canResolve,
  mutationError,
  onDecision,
}: {
  approval: EdgeApproval;
  event?: AgentActionEvent;
  pendingCount: number;
  principalId?: string;
  note: string;
  onNoteChange: (value: string) => void;
  canResolve: boolean;
  mutationError?: Error | null;
  onDecision: (decision: PendingDecision) => void;
}) {
  return (
    <section className="rounded-3xl border border-border bg-surface-1/70 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <StatusBadge variant={approvalStatusVariant(approval.status)} dot>{formatLabel(approval.status)}</StatusBadge>
        <StatusBadge variant={pendingCount > 0 ? "warning" : "muted"}>{pendingCount} pending</StatusBadge>
      </div>
      <h3 className="mt-4 text-lg font-semibold text-foreground">{actionSummary(approval, event)}</h3>
      <p className="mt-2 text-sm text-muted-foreground">{approval.reason || "Approval required by Edge policy."}</p>
      <ApprovalFacts approval={approval} />
      <RiskTags tags={event?.riskTags} />
      <WarningStack approval={approval} principalId={principalId} />
      <div className="mt-4 rounded-2xl border border-border bg-surface-0 p-3">
        <div className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">Redacted input</div>
        <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words text-xs text-foreground">
          {redactedInputPreview(event)}
        </pre>
      </div>
      <Textarea
        className="mt-4 min-h-20"
        value={note}
        onChange={(event) => onNoteChange(event.target.value)}
        placeholder="Optional reviewer note. This records rationale only; it does not alter the command."
      />
      {mutationError && <DecisionError error={mutationError} />}
      <div className="mt-4 flex flex-wrap justify-end gap-2">
        <Button variant="danger" disabled={!canResolve} onClick={() => onDecision({ kind: "reject", approval })}>
          <XCircle className="h-4 w-4" />
          Reject
        </Button>
        <Button disabled={!canResolve} onClick={() => onDecision({ kind: "approve", approval })}>
          <CheckCircle2 className="h-4 w-4" />
          Approve
        </Button>
      </div>
    </section>
  );
}
function ApprovalFacts({ approval }: { approval: EdgeApproval }) {
  return (
    <div className="mt-4 grid gap-3 sm:grid-cols-2">
      <Fact label="Requester" value={approval.requester || approval.principalId} />
      <Fact label="Approval ref" value={approval.approvalRef} />
      <Fact label="Policy snapshot" value={approval.policySnapshot} />
      <Fact label="Rule" value={approval.ruleId} />
      <Fact label="Action hash" value={compactHash(approval.actionHash)} />
      <Fact label="Input hash" value={compactHash(approval.inputHash)} />
      <Fact label="Expires" value={approval.expiresAt} />
      <Fact label="Event" value={approval.eventId} />
    </div>
  );
}
function DecisionError({ error }: { error: Error }) {
  return (
    <WarningBlock title={isNotVisibleError(error) ? "Approval not visible" : "Decision failed"} tone="danger">
      {isNotVisibleError(error) ? "This approval is no longer visible to your principal." : error.message}
    </WarningBlock>
  );
}
function ApprovalList({ approvals, selectedRef, onSelect }: { approvals: EdgeApproval[]; selectedRef?: string; onSelect: (ref: string) => void }) {
  return (
    <div className="space-y-2">
      {approvals.map((approval) => (
        <button
          key={approval.approvalRef}
          type="button"
          onClick={() => onSelect(approval.approvalRef)}
          className={cn(
            "w-full rounded-2xl border p-3 text-left transition-colors",
            selectedRef === approval.approvalRef ? "border-cordum bg-cordum/10" : "border-border bg-surface-1/60 hover:bg-surface-2",
          )}
        >
          <div className="flex items-center justify-between gap-2">
            <span className="font-mono text-xs text-foreground">{approval.approvalRef}</span>
            <StatusBadge variant={approvalStatusVariant(approval.status)}>{formatLabel(approval.status)}</StatusBadge>
          </div>
          <p className="mt-2 line-clamp-2 text-sm text-muted-foreground">{approval.reason || "Edge approval requested."}</p>
        </button>
      ))}
    </div>
  );
}
function RiskTags({ tags }: { tags?: string[] }) {
  if (!tags?.length) return null;
  return (
    <div className="mt-4 flex flex-wrap gap-2">
      {tags.map((tag) => <StatusBadge key={tag} variant="warning">{tag}</StatusBadge>)}
    </div>
  );
}
function WarningStack({ approval, principalId }: { approval: EdgeApproval; principalId?: string }) {
  return (
    <div className="mt-4 space-y-2">
      {isSelfApproval(approval, principalId) && <WarningBlock title="Self-approval warning" tone="warning">The requester matches your current principal; ask another reviewer to decide.</WarningBlock>}
      {isExpired(approval) && <WarningBlock title="Approval expired" tone="danger">This approval is stale. Refresh and retry the tool action to create a fresh request.</WarningBlock>}
      {isTerminal(approval) && <WarningBlock title="Terminal approval" tone="muted">This approval has already left pending state.</WarningBlock>}
      {!isTerminal(approval) && !isExpired(approval) && !isSelfApproval(approval, principalId) && (
        <WarningBlock title="Retry guidance" tone="info">After approval, the agent must retry the same action hash; changed input requires a new approval.</WarningBlock>
      )}
    </div>
  );
}
function WarningBlock({ title, tone, children }: { title: string; tone: BadgeVariant; children: React.ReactNode }) {
  const Icon = tone === "danger" ? ShieldAlert : tone === "info" ? Clock3 : AlertTriangle;
  return (
    <div className={cn("rounded-2xl border p-3", tone === "danger" ? "border-destructive/20 bg-destructive/10" : "border-border bg-surface-1")}>
      <div className="flex gap-2">
        <Icon className="mt-0.5 h-4 w-4 text-muted-foreground" />
        <div>
          <div className="text-sm font-semibold text-foreground">{title}</div>
          <div className="text-xs leading-relaxed text-muted-foreground">{children}</div>
        </div>
      </div>
    </div>
  );
}
