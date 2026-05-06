// MCPApprovalQueue — table-style approval queue for the MCP
// governance page. Distinct from the card-style McpApprovalCard used
// inline on ApprovalsPage so operators reviewing many requests at
// once can scan denser rows; the underlying mutations and query are
// the shared hooks from useMcp.
//
// Key behaviours:
// - Self-approval guard: a user cannot resolve a request they
//   originated. The action buttons are disabled with an explanatory
//   tooltip when approval.requester (or agent_id fallback) matches
//   the session principal id.
// - Confirmation modal: approve/reject open a small modal with a
//   reason textarea. Submitting fires the mutation and closes the
//   modal; React Query invalidation keeps the list current.
// - Empty / error states are first-class: clear copy + a retry
//   button on error.
import { useMemo, useState } from "react";
import { useConfigStore } from "../../state/config";
import { useDialogA11y } from "../../hooks/useDialogA11y";
import {
  isMcpApprovalsUnavailableError,
  shortArgsHash,
  useApproveMcp,
  useMcpPendingApprovals,
  useRejectMcp,
  type McpApproval,
  type McpApprovalStatus,
} from "../../hooks/useMcp";
import { cn } from "../../lib/utils";

export type MCPApprovalQueueStatus = Extract<
  McpApprovalStatus,
  "pending" | "approved" | "rejected" | "expired"
>;

export interface MCPApprovalQueueProps {
  status: MCPApprovalQueueStatus;
  onDetail?: (id: string) => void;
  className?: string;
}

const StatusClass: Record<MCPApprovalQueueStatus, string> = {
  pending:
    "bg-amber-500/15 text-amber-700 dark:text-amber-300 ring-1 ring-amber-500/30",
  approved:
    "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 ring-1 ring-emerald-500/30",
  rejected:
    "bg-red-500/15 text-red-700 dark:text-red-300 ring-1 ring-red-500/30",
  expired:
    "bg-gray-500/15 text-gray-700 dark:text-gray-300 ring-1 ring-gray-500/30",
};

export function MCPApprovalQueue(props: MCPApprovalQueueProps) {
  const { status, onDetail, className } = props;
  const principalId = useConfigStore((s) => s.principalId ?? "");
  const query = useMcpPendingApprovals(status);
  const items = query.data ?? [];
  const approvalsUnavailable = isMcpApprovalsUnavailableError(query.error);

  const [pendingDecision, setPendingDecision] = useState<
    | { id: string; tool: string; verb: "approve" | "reject" }
    | null
  >(null);

  const isLoading = query.isLoading;
  const isError = query.isError;

  const sorted = useMemo(() => {
    const copy = [...items];
    copy.sort((a, b) => b.created_at - a.created_at);
    return copy;
  }, [items]);

  return (
    <section
      aria-labelledby="mcp-approval-queue-heading"
      data-testid="mcp-approval-queue"
      className={cn("flex flex-col gap-3", className)}
    >
      <header className="flex items-baseline justify-between">
        <h3
          id="mcp-approval-queue-heading"
          className="text-sm font-semibold uppercase tracking-wider text-[color:var(--text-muted,#a1a1aa)]"
        >
          {status} approvals
        </h3>
        <span className="text-xs text-[color:var(--text-muted,#a1a1aa)]">
          {isLoading ? "Loading…" : `${sorted.length} item${sorted.length === 1 ? "" : "s"}`}
        </span>
      </header>

      {isLoading && (
        <div
          role="status"
          aria-busy="true"
          className="rounded-xl border border-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)] p-4 text-sm text-[color:var(--text-muted,#a1a1aa)]"
          data-testid="mcp-approval-queue-loading"
        >
          Loading {status} approvals…
        </div>
      )}

      {isError && approvalsUnavailable && (
        <div
          role="status"
          className="rounded-xl border border-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)] px-4 py-3 text-sm text-[color:var(--text-muted,#a1a1aa)]"
          data-testid="mcp-approval-queue-unavailable"
        >
          MCP approval engine is disabled for this deployment. No MCP tool approvals are queued.
        </div>
      )}

      {isError && !approvalsUnavailable && (
        <div
          role="alert"
          className="flex items-center justify-between rounded-xl border border-red-500/40 bg-red-500/10 px-4 py-3 text-sm text-red-700 dark:text-red-300"
          data-testid="mcp-approval-queue-error"
        >
          <span>Failed to load MCP approvals.</span>
          <button
            type="button"
            className="rounded bg-red-600 px-3 py-1 text-xs text-white hover:bg-red-500"
            onClick={() => query.refetch()}
          >
            Retry
          </button>
        </div>
      )}

      {!isLoading && !isError && sorted.length === 0 && (
        <p
          className="rounded-xl border border-dashed border-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)] p-6 text-center text-sm text-[color:var(--text-muted,#a1a1aa)]"
          data-testid="mcp-approval-queue-empty"
        >
          No MCP approvals match the current filter.
        </p>
      )}

      {!isLoading && !isError && sorted.length > 0 && (
        <div className="overflow-x-auto rounded-xl border border-[color:var(--border-color,#27272a)]">
          <table
            className="min-w-full divide-y divide-[color:var(--border-color,#27272a)] text-sm"
            data-testid="mcp-approval-queue-table"
          >
            <thead className="bg-[color:var(--surface-elevated,#111114)] text-[10px] uppercase tracking-wider text-[color:var(--text-muted,#a1a1aa)]">
              <tr>
                <Th>Tool</Th>
                <Th>Agent</Th>
                <Th>Requested at</Th>
                <Th>Args hash</Th>
                <Th>Status</Th>
                <Th className="text-right">Actions</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)]">
              {sorted.map((row) => (
                <ApprovalRow
                  key={row.id}
                  row={row}
                  status={status}
                  principalId={principalId}
                  onApprove={() => setPendingDecision({ id: row.id, tool: row.tool_name, verb: "approve" })}
                  onReject={() => setPendingDecision({ id: row.id, tool: row.tool_name, verb: "reject" })}
                  onDetail={onDetail}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}

      {pendingDecision && (
        <ConfirmDecisionModal
          decision={pendingDecision}
          onClose={() => setPendingDecision(null)}
        />
      )}
    </section>
  );
}

function Th(props: { children: React.ReactNode; className?: string }) {
  return (
    <th
      scope="col"
      className={cn("px-3 py-2 text-left font-mono", props.className)}
    >
      {props.children}
    </th>
  );
}

interface ApprovalRowProps {
  row: McpApproval;
  status: MCPApprovalQueueStatus;
  principalId: string;
  onApprove: () => void;
  onReject: () => void;
  onDetail?: (id: string) => void;
}

function ApprovalRow({ row, status, principalId, onApprove, onReject, onDetail }: ApprovalRowProps) {
  const requesterId = row.requester || row.agent_id;
  const isSelf =
    principalId !== "" && requesterId !== "" && principalId === requesterId;

  return (
    <tr data-testid={`mcp-approval-row-${row.id}`}>
      <td className="px-3 py-2 font-mono">
        <button
          type="button"
          className="text-left text-[color:var(--text,#e4e4e7)] hover:underline focus:underline"
          onClick={() => onDetail?.(row.id)}
        >
          {row.tool_name}
        </button>
      </td>
      <td className="px-3 py-2 font-mono text-xs">{requesterId}</td>
      <td className="px-3 py-2 text-xs text-[color:var(--text-muted,#a1a1aa)]">
        {formatRelative(row.created_at)}
      </td>
      <td className="px-3 py-2 font-mono text-xs">{shortArgsHash(row.args_hash)}</td>
      <td className="px-3 py-2">
        <span
          className={cn("inline-flex rounded-sm px-2 py-0.5 text-xs font-medium", StatusClass[status])}
        >
          {row.status}
        </span>
      </td>
      <td className="px-3 py-2">
        <div className="flex justify-end gap-2">
          {status === "pending" ? (
            <ApproveRejectButtons
              isSelf={isSelf}
              onApprove={onApprove}
              onReject={onReject}
            />
          ) : (
            <span className="text-xs text-[color:var(--text-muted,#a1a1aa)]">
              {row.resolved_by ? `by ${row.resolved_by}` : "—"}
            </span>
          )}
        </div>
      </td>
    </tr>
  );
}

interface ApproveRejectButtonsProps {
  isSelf: boolean;
  onApprove: () => void;
  onReject: () => void;
}

function ApproveRejectButtons({ isSelf, onApprove, onReject }: ApproveRejectButtonsProps) {
  const tooltip = isSelf
    ? "You cannot approve a request you originated"
    : undefined;
  return (
    <>
      <button
        type="button"
        disabled={isSelf}
        title={tooltip}
        aria-label={isSelf ? "Approve disabled (self-approval)" : "Approve"}
        className={cn(
          "rounded bg-emerald-600 px-3 py-1 text-xs font-medium text-white",
          "hover:bg-emerald-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400",
          "disabled:cursor-not-allowed disabled:bg-emerald-600/40 disabled:text-white/60",
        )}
        onClick={onApprove}
        data-testid="mcp-approval-row-approve"
      >
        Approve
      </button>
      <button
        type="button"
        disabled={isSelf}
        title={tooltip}
        aria-label={isSelf ? "Reject disabled (self-approval)" : "Reject"}
        className={cn(
          "rounded bg-red-600 px-3 py-1 text-xs font-medium text-white",
          "hover:bg-red-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-red-400",
          "disabled:cursor-not-allowed disabled:bg-red-600/40 disabled:text-white/60",
        )}
        onClick={onReject}
        data-testid="mcp-approval-row-reject"
      >
        Reject
      </button>
    </>
  );
}

interface ConfirmDecisionModalProps {
  decision: { id: string; tool: string; verb: "approve" | "reject" };
  onClose: () => void;
}

function ConfirmDecisionModal({ decision, onClose }: ConfirmDecisionModalProps) {
  const approveM = useApproveMcp();
  const rejectM = useRejectMcp();
  const [reason, setReason] = useState("");
  const dialogRef = useDialogA11y(onClose);
  const isApprove = decision.verb === "approve";
  const mutation = isApprove ? approveM : rejectM;
  const submit = () => {
    mutation.mutate(
      { id: decision.id, reason: reason.trim() || undefined },
      { onSuccess: onClose },
    );
  };
  return (
    <div
      ref={dialogRef}
      role="dialog"
      aria-modal="true"
      aria-labelledby="mcp-approval-confirm-heading"
      data-testid="mcp-approval-confirm-modal"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 px-4"
    >
      <div className="w-full max-w-md rounded-xl border border-[color:var(--border-color,#27272a)] bg-[color:var(--surface-elevated,#111114)] p-5 shadow-xl">
        <h4
          id="mcp-approval-confirm-heading"
          className="text-base font-semibold text-[color:var(--text,#e4e4e7)]"
        >
          {isApprove ? "Approve" : "Reject"} {decision.tool}
        </h4>
        <p className="mt-1 text-xs text-[color:var(--text-muted,#a1a1aa)]">
          {isApprove
            ? "The requesting agent will receive the green light to invoke this tool."
            : "The requesting agent will receive a denied response. Add a reason so the agent can correct course."}
        </p>
        <label className="mt-4 block text-xs text-[color:var(--text-muted,#a1a1aa)]">
          Reason (optional)
          <textarea
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            rows={3}
            className="mt-1 block w-full rounded border border-[color:var(--border-color,#27272a)] bg-[color:var(--surface,#0b0b0e)] p-2 text-sm text-[color:var(--text,#e4e4e7)] focus:outline-none focus:ring-1 focus:ring-[color:var(--accent,#a78bfa)]"
            data-testid="mcp-approval-confirm-reason"
          />
        </label>
        <div className="mt-4 flex justify-end gap-2">
          <button
            type="button"
            className="rounded border border-[color:var(--border-color,#27272a)] px-3 py-1.5 text-xs text-[color:var(--text-muted,#a1a1aa)] hover:bg-white/5"
            onClick={onClose}
            data-testid="mcp-approval-confirm-cancel"
          >
            Cancel
          </button>
          <button
            type="button"
            className={cn(
              "rounded px-3 py-1.5 text-xs font-medium text-white",
              isApprove ? "bg-emerald-600 hover:bg-emerald-500" : "bg-red-600 hover:bg-red-500",
              "disabled:opacity-50",
            )}
            onClick={submit}
            disabled={mutation.isPending}
            data-testid="mcp-approval-confirm-submit"
          >
            {mutation.isPending ? "Submitting…" : isApprove ? "Approve" : "Reject"}
          </button>
        </div>
      </div>
    </div>
  );
}

function formatRelative(unixSeconds: number): string {
  if (!unixSeconds) return "—";
  const ms = unixSeconds < 1e12 ? unixSeconds * 1000 : unixSeconds;
  const diff = Date.now() - ms;
  if (diff < 0) return "just now";
  const sec = Math.round(diff / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.round(hr / 24);
  return `${day}d ago`;
}

export default MCPApprovalQueue;
