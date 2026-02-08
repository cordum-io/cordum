import { useState, useCallback } from "react";
import { Upload, RotateCcw, Loader } from "lucide-react";
import {
  usePublishPolicy,
  useRollbackPolicy,
  usePolicySnapshots,
} from "../../hooks/usePolicies";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { logger } from "../../lib/logger";
import type { PolicySnapshotSummary } from "../../api/types";

// ---------------------------------------------------------------------------
// Relative time
// ---------------------------------------------------------------------------

function timeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

// ---------------------------------------------------------------------------
// Confirm dialog
// ---------------------------------------------------------------------------

function ConfirmDialog({
  title,
  message,
  confirmLabel,
  variant,
  isPending,
  onConfirm,
  onCancel,
}: {
  title: string;
  message: string;
  confirmLabel: string;
  variant: "primary" | "danger";
  isPending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <Card className="relative z-10 w-full max-w-sm">
        <div className="space-y-4">
          <h3 className="font-display text-lg font-semibold text-ink">{title}</h3>
          <p className="text-sm text-muted">{message}</p>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={onCancel} disabled={isPending}>
              Cancel
            </Button>
            <Button variant={variant} size="sm" onClick={onConfirm} disabled={isPending}>
              {isPending ? "Processing\u2026" : confirmLabel}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}

// ---------------------------------------------------------------------------
// PublishControls
// ---------------------------------------------------------------------------

export function PublishControls({
  bundleId,
  ruleCount,
}: {
  bundleId: string;
  ruleCount: number;
}) {
  const [showPublishConfirm, setShowPublishConfirm] = useState(false);
  const [rollbackTarget, setRollbackTarget] = useState<PolicySnapshotSummary | null>(null);
  const [publishSuccess, setPublishSuccess] = useState<string | null>(null);

  const publishPolicy = usePublishPolicy();
  const rollbackPolicy = useRollbackPolicy();
  const { data: snapshotsData, isLoading: snapshotsLoading } = usePolicySnapshots();
  const snapshots = snapshotsData?.items ?? [];

  const handlePublish = useCallback(() => {
    logger.info("publish-controls", "Publish clicked", { bundleId });
    publishPolicy.mutate({ bundleId }, {
      onSuccess: () => {
        setShowPublishConfirm(false);
        setPublishSuccess("Published");
        setTimeout(() => setPublishSuccess(null), 3000);
      },
    });
  }, [bundleId, publishPolicy]);

  const handleRollback = useCallback(() => {
    if (!rollbackTarget) return;
    logger.info("publish-controls", "Rollback clicked", { snapshotId: rollbackTarget.id });
    rollbackPolicy.mutate({ snapshotId: rollbackTarget.id }, {
      onSuccess: () => setRollbackTarget(null),
    });
  }, [rollbackTarget, rollbackPolicy]);

  return (
    <div className="space-y-4">
      {/* Publish section */}
      <Card>
        <div className="flex items-center justify-between">
          <div>
            <h3 className="font-display text-base font-semibold text-ink">
              Publish Policy
            </h3>
            <p className="text-xs text-muted">
              Current draft &middot; {ruleCount} rule{ruleCount !== 1 ? "s" : ""}
            </p>
          </div>
          <div className="flex items-center gap-2">
            {publishSuccess && (
              <span className="text-xs font-semibold text-success">{publishSuccess}</span>
            )}
            <Button
              onClick={() => setShowPublishConfirm(true)}
              disabled={publishPolicy.isPending}
            >
              <Upload className="h-4 w-4" />
              Publish
            </Button>
          </div>
        </div>
      </Card>

      {/* Snapshots / rollback */}
      <Card>
        <div className="space-y-3">
          <h3 className="font-display text-base font-semibold text-ink">
            Snapshots
          </h3>

          {snapshotsLoading && (
            <div className="flex items-center gap-2 py-4 text-xs text-muted">
              <Loader className="h-3.5 w-3.5 animate-spin" />
              Loading snapshots...
            </div>
          )}

          {!snapshotsLoading && snapshots.length === 0 && (
            <p className="py-4 text-xs text-muted">No snapshots yet. Publish to create one.</p>
          )}

          {!snapshotsLoading && snapshots.length > 0 && (
            <div className="divide-y divide-border rounded-xl border border-border">
              {snapshots.map((snap: PolicySnapshotSummary) => (
                <div
                  key={snap.id}
                  className="flex items-center justify-between px-4 py-3"
                >
                  <div className="flex items-center gap-3">
                    <Badge variant="info">v{snap.version ?? "?"}</Badge>
                    <div className="text-xs">
                      <span className="text-ink">{timeAgo(snap.createdAt)}</span>
                      {snap.createdBy && (
                        <span className="ml-2 text-muted">&middot; by {snap.createdBy}</span>
                      )}
                      {snap.note && (
                        <span className="ml-2 text-muted">&middot; {snap.note}</span>
                      )}
                    </div>
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setRollbackTarget(snap)}
                    disabled={rollbackPolicy.isPending}
                  >
                    <RotateCcw className="h-3.5 w-3.5" />
                    Rollback
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>
      </Card>

      {/* Publish confirm */}
      {showPublishConfirm && (
        <ConfirmDialog
          title="Publish Policy"
          message={`Publish current draft? This will apply ${ruleCount} rule${ruleCount !== 1 ? "s" : ""} to all active evaluations.`}
          confirmLabel="Publish"
          variant="primary"
          isPending={publishPolicy.isPending}
          onConfirm={handlePublish}
          onCancel={() => setShowPublishConfirm(false)}
        />
      )}

      {/* Rollback confirm */}
      {rollbackTarget && (
        <ConfirmDialog
          title="Rollback Policy"
          message={`Rollback to snapshot created ${timeAgo(rollbackTarget.createdAt)}? Current draft will be replaced.`}
          confirmLabel="Rollback"
          variant="danger"
          isPending={rollbackPolicy.isPending}
          onConfirm={handleRollback}
          onCancel={() => setRollbackTarget(null)}
        />
      )}
    </div>
  );
}
