// McpApprovalCard — single-approval list row + args-review modal for
// the MCP per-tool approval queue.
//
// Mirrors the existing job-approval card layout so operators see one
// consistent visual language across both queues. The one behavioural
// difference: MCP approvals surface the tool name (not the topic), the
// requesting agent, and a truncated args hash. Clicking the row opens
// a modal that fetches the full args JSON for review.
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  type McpApproval,
  shortArgsHash,
  useApproveMcp,
  useMcpApproval,
  useRejectMcp,
} from "../../hooks/useMcpApprovals";
import { useDialogA11y } from "../../hooks/useDialogA11y";

interface Props {
  approval: McpApproval;
}

const StatusClass: Record<McpApproval["status"], string> = {
  pending: "bg-yellow-500/10 text-yellow-500 border-yellow-500/20",
  approved: "bg-green-500/10 text-green-500 border-green-500/20",
  rejected: "bg-red-500/10 text-red-500 border-red-500/20",
  expired: "bg-gray-500/10 text-gray-500 border-gray-500/20",
  invalidated: "bg-gray-500/10 text-gray-500 border-gray-500/20",
};

export function McpApprovalCard({ approval }: Props) {
  const [modalOpen, setModalOpen] = useState(false);
  const approveM = useApproveMcp();
  const rejectM = useRejectMcp();

  const disabled = approval.status !== "pending";

  return (
    <div
      className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm hover:shadow transition-shadow dark:border-gray-800 dark:bg-gray-900"
      data-testid={`mcp-approval-${approval.id}`}
    >
      <div className="flex items-start justify-between gap-4">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 mb-1">
            <span className="text-xs font-mono rounded bg-slate-900 px-2 py-0.5 text-white">
              MCP
            </span>
            <span className="font-semibold truncate">{approval.tool_name}</span>
            <span
              className={`text-xs rounded border px-2 py-0.5 ${
                StatusClass[approval.status] ?? StatusClass.pending
              }`}
            >
              {approval.status}
            </span>
          </div>
          <dl className="grid grid-cols-2 gap-x-6 gap-y-1 text-sm text-gray-600 dark:text-gray-400">
            <div>
              <dt className="text-xs uppercase tracking-wide">Requester</dt>
              <dd className="font-mono truncate">{approval.requester || approval.agent_id}</dd>
            </div>
            <div>
              <dt className="text-xs uppercase tracking-wide">Tenant</dt>
              <dd className="font-mono">{approval.tenant}</dd>
            </div>
            <div>
              <dt className="text-xs uppercase tracking-wide">Args hash</dt>
              <dd className="font-mono">{shortArgsHash(approval.args_hash)}</dd>
            </div>
            <div>
              <dt className="text-xs uppercase tracking-wide">Expires</dt>
              <dd>{formatExpiry(approval.expires_at)}</dd>
            </div>
          </dl>
          {approval.reason && (
            <p className="mt-2 text-sm text-gray-700 dark:text-gray-300">
              {approval.reason}
            </p>
          )}
        </div>
        <div className="flex flex-col gap-2 items-end">
          <button
            type="button"
            className="text-xs text-blue-600 hover:underline dark:text-blue-400"
            onClick={() => setModalOpen(true)}
            data-testid={`mcp-approval-${approval.id}-review`}
          >
            Review args
          </button>
          <div className="flex gap-2">
            <button
              type="button"
              className="rounded bg-green-600 px-3 py-1.5 text-sm text-white disabled:opacity-50"
              disabled={disabled || approveM.isPending}
              onClick={() => approveM.mutate({ id: approval.id })}
              data-testid={`mcp-approval-${approval.id}-approve`}
            >
              Approve
            </button>
            <button
              type="button"
              className="rounded bg-red-600 px-3 py-1.5 text-sm text-white disabled:opacity-50"
              disabled={disabled || rejectM.isPending}
              onClick={() => rejectM.mutate({ id: approval.id })}
              data-testid={`mcp-approval-${approval.id}-reject`}
            >
              Reject
            </button>
          </div>
        </div>
      </div>
      {modalOpen && (
        <ArgsReviewModal approvalId={approval.id} onClose={() => setModalOpen(false)} />
      )}
    </div>
  );
}

// ArgsReviewModal fetches the full approval record (which carries the
// un-truncated args blob) so the approver can read exactly what the
// agent intends to execute before approving.
//
// Accessibility: useDialogA11y installs Escape-to-close and a focus
// trap, plus restores focus to the trigger on dismissal. The dialog
// has explicit aria-modal + aria-labelledby so screen readers announce
// it correctly.
function ArgsReviewModal({
  approvalId,
  onClose,
}: {
  approvalId: string;
  onClose: () => void;
}) {
  const { data, isLoading, error } = useMcpApproval(approvalId);
  const queryClient = useQueryClient();
  const dialogRef = useDialogA11y(onClose, {
    initialFocusSelector: 'button[data-a11y="modal-close"]',
  });
  const titleId = `mcp-args-review-${approvalId}-title`;
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      onClick={onClose}
    >
      <div
        ref={dialogRef}
        className="max-w-2xl w-full max-h-[80vh] overflow-auto rounded-xl bg-white p-6 shadow-xl dark:bg-gray-900"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-4 mb-4">
          <div>
            <h2 id={titleId} className="text-lg font-semibold">
              {data?.tool_name ?? "Loading…"}
            </h2>
            {data && (
              <p className="text-xs font-mono text-gray-500">{data.args_hash}</p>
            )}
          </div>
          <button
            type="button"
            className="text-gray-500 hover:text-gray-700"
            onClick={onClose}
            aria-label="Close dialog"
            data-a11y="modal-close"
          >
            ✕
          </button>
        </div>
        {isLoading && <p className="text-sm text-gray-500">Loading args…</p>}
        {error && <p className="text-sm text-red-600">Failed to load args.</p>}
        {data && (
          <pre
            className="text-xs bg-gray-100 dark:bg-gray-800 p-3 rounded overflow-auto"
            data-testid="mcp-args-payload"
          >
            {renderArgs(data.args)}
          </pre>
        )}
        <div className="flex justify-end gap-2 mt-4">
          <button
            type="button"
            className="rounded border border-gray-300 px-3 py-1.5 text-sm"
            onClick={() => {
              queryClient.invalidateQueries({ queryKey: ["mcp-approval", approvalId] });
            }}
          >
            Refresh
          </button>
          <button
            type="button"
            className="rounded bg-gray-800 px-3 py-1.5 text-sm text-white"
            onClick={onClose}
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}

// renderArgs pretty-prints the persisted args payload. The gateway
// stores the canonical JSON (post-normalization) so the approver sees
// a stable, sorted-keys form — matches the args_hash they are
// approving. Oversize payloads are replaced server-side with a
// {_truncated,_original_bytes,_hash} placeholder; the placeholder is
// still valid JSON so it renders the same way.
function renderArgs(args: unknown): string {
  if (args === undefined || args === null) {
    return "(args not captured on this approval)";
  }
  try {
    return JSON.stringify(args, null, 2);
  } catch {
    return String(args);
  }
}

function formatExpiry(microSec: number): string {
  if (!microSec) return "—";
  const deltaMs = microSec / 1000 - Date.now();
  if (deltaMs <= 0) return "expired";
  const seconds = Math.floor(deltaMs / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h`;
}

