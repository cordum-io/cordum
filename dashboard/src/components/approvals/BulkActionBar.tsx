import { useState, useMemo, useCallback, useRef } from "react";
import { Button } from "../ui/Button";
import { Textarea } from "../ui/Textarea";
import { cn } from "../../lib/utils";
import type { Approval } from "../../api/types";

// ---------------------------------------------------------------------------
// High-risk check (same as ApprovalsPage)
// ---------------------------------------------------------------------------

const HIGH_RISK_TAGS = new Set(["financial", "destructive", "compliance", "production"]);

function isHighRisk(approval: Approval): boolean {
  return (approval.riskTags ?? []).some((t) => HIGH_RISK_TAGS.has(t));
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type BarMode = "idle" | "confirm-approve" | "confirm-reject" | "executing";

interface BulkActionBarProps {
  selectedIds: Set<string>;
  approvals: Approval[];
  onApprove: (id: string, comment?: string) => Promise<void>;
  onReject: (id: string, reason: string) => Promise<void>;
  onClear: () => void;
  onDone: () => void;
}

// ---------------------------------------------------------------------------
// BulkActionBar
// ---------------------------------------------------------------------------

export function BulkActionBar({
  selectedIds,
  approvals,
  onApprove,
  onReject,
  onClear,
  onDone,
}: BulkActionBarProps) {
  const [mode, setMode] = useState<BarMode>("idle");
  const [comment, setComment] = useState("");
  const [reason, setReason] = useState("");
  const [progress, setProgress] = useState({ done: 0, total: 0, failed: 0 });
  const [resultMsg, setResultMsg] = useState<string | null>(null);

  const selectedApprovals = useMemo(
    () => approvals.filter((a) => selectedIds.has(a.id)),
    [approvals, selectedIds],
  );

  const highRiskCount = useMemo(
    () => selectedApprovals.filter(isHighRisk).length,
    [selectedApprovals],
  );

  const eligibleForApprove = selectedApprovals.filter((a) => !isHighRisk(a));
  const eligibleCount = eligibleForApprove.length;
  const totalSelected = selectedIds.size;

  // Synchronous guard prevents double-submit even when React state hasn't re-rendered yet
  const executingRef = useRef(false);

  const reset = useCallback(() => {
    setMode("idle");
    setComment("");
    setReason("");
    setProgress({ done: 0, total: 0, failed: 0 });
    setResultMsg(null);
  }, []);

  const executeBulk = useCallback(
    async (action: "approve" | "reject", items: Approval[], payload: string) => {
      if (executingRef.current) return;
      executingRef.current = true;
      try {
        setMode("executing");
        setProgress({ done: 0, total: items.length, failed: 0 });
        let failed = 0;

        for (let i = 0; i < items.length; i++) {
          try {
            if (action === "approve") {
              await onApprove(items[i].id, payload || undefined);
            } else {
              await onReject(items[i].id, payload);
            }
          } catch {
            failed++;
          }
          setProgress({ done: i + 1, total: items.length, failed });
          // Small delay to avoid flooding
          if (i < items.length - 1) {
            await new Promise((r) => setTimeout(r, 200));
          }
        }

        const verb = action === "approve" ? "approved" : "rejected";
        if (failed === 0) {
          setResultMsg(`${items.length} ${verb}`);
        } else {
          setResultMsg(`${items.length - failed} ${verb}, ${failed} failed`);
        }

        setTimeout(() => {
          reset();
          if (failed === 0) onDone();
        }, 1500);
      } finally {
        executingRef.current = false;
      }
    },
    [onApprove, onReject, onDone, reset],
  );

  if (totalSelected === 0) return null;

  return (
    <div className="fixed bottom-0 left-0 z-30 w-full border-t border-border bg-surface shadow-lg">
      <div className="mx-auto flex max-w-7xl items-center justify-between px-4 py-3">
        {/* Left: selection info */}
        <div className="text-sm text-ink">
          <span className="font-semibold">{totalSelected}</span> selected
          {highRiskCount > 0 && (
            <span className="ml-2 text-xs text-warning">
              ({highRiskCount} require{highRiskCount === 1 ? "s" : ""} individual review)
            </span>
          )}
        </div>

        {/* Right: actions */}
        <div className="flex items-center gap-2">
          {mode === "idle" && (
            <>
              <Button
                size="sm"
                className="bg-[var(--color-success)] hover:bg-[var(--color-success)]/90 text-primary-foreground"
                disabled={eligibleCount === 0}
                onClick={() => setMode("confirm-approve")}
                title={eligibleCount === 0 ? "All selected items are high-risk" : undefined}
              >
                Approve {eligibleCount}{eligibleCount < totalSelected ? ` of ${totalSelected}` : ""}
              </Button>
              <Button
                variant="danger"
                size="sm"
                onClick={() => setMode("confirm-reject")}
              >
                Reject {totalSelected}
              </Button>
              <Button variant="ghost" size="sm" onClick={onClear}>
                Clear
              </Button>
            </>
          )}

          {mode === "confirm-approve" && (
            <div className="flex items-center gap-2">
              <Textarea
                rows={1}
                className="h-8 w-64 text-xs"
                placeholder="Shared comment (optional)"
                value={comment}
                onChange={(e) => setComment(e.target.value)}
              />
              <Button
                size="sm"
                className="bg-[var(--color-success)] hover:bg-[var(--color-success)]/90 text-primary-foreground"
                disabled={mode !== "confirm-approve"}
                onClick={() => executeBulk("approve", eligibleForApprove, comment)}
              >
                Confirm ({eligibleCount})
              </Button>
              <Button variant="ghost" size="sm" onClick={reset}>
                Cancel
              </Button>
            </div>
          )}

          {mode === "confirm-reject" && (
            <div className="flex items-center gap-2">
              <Textarea
                rows={1}
                className={cn("h-8 w-64 text-xs", !reason.trim() && "border-danger")}
                placeholder="Shared reason (required)"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
              <Button
                variant="danger"
                size="sm"
                disabled={!reason.trim() || mode !== "confirm-reject"}
                onClick={() => executeBulk("reject", selectedApprovals, reason)}
              >
                Confirm ({totalSelected})
              </Button>
              <Button variant="ghost" size="sm" onClick={reset}>
                Cancel
              </Button>
            </div>
          )}

          {mode === "executing" && (
            <div className="flex items-center gap-3">
              <div className="h-1.5 w-40 rounded-full bg-surface2 overflow-hidden">
                <div
                  className="h-full rounded-full bg-accent transition-all duration-200"
                  style={{ width: `${progress.total > 0 ? (progress.done / progress.total) * 100 : 0}%` }}
                />
              </div>
              <span className="text-xs text-muted-foreground">
                {progress.done}/{progress.total}
                {progress.failed > 0 && ` (${progress.failed} failed)`}
              </span>
            </div>
          )}

          {resultMsg && mode === "idle" && (
            <span className="text-sm font-medium text-success">{resultMsg}</span>
          )}
        </div>
      </div>
    </div>
  );
}
