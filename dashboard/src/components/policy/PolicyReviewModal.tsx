import { useState, useMemo, useCallback } from "react";
import { X, ThumbsUp, ThumbsDown, Loader } from "lucide-react";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { Textarea } from "../ui/Textarea";
import { usePublishPolicy, usePolicySnapshot, usePolicySnapshots } from "../../hooks/usePolicies";
import { cn } from "../../lib/utils";
import type { PolicyBundle, PolicyRule } from "../../api/types";

// ---------------------------------------------------------------------------
// Diff types (mirrors SnapshotComparison logic)
// ---------------------------------------------------------------------------

type DiffKind = "added" | "removed" | "changed" | "unchanged";

interface RuleDiff {
  kind: DiffKind;
  ruleA?: PolicyRule;
  ruleB?: PolicyRule;
}

function diffRules(rulesA: PolicyRule[], rulesB: PolicyRule[]): RuleDiff[] {
  const mapA = new Map(rulesA.map((r) => [r.id, r]));
  const mapB = new Map(rulesB.map((r) => [r.id, r]));
  const allIds = new Set([...mapA.keys(), ...mapB.keys()]);

  const diffs: RuleDiff[] = [];

  for (const id of allIds) {
    const a = mapA.get(id);
    const b = mapB.get(id);

    if (a && !b) {
      diffs.push({ kind: "added", ruleB: undefined, ruleA: a });
    } else if (!a && b) {
      diffs.push({ kind: "added", ruleA: undefined, ruleB: b });
    } else if (a && b) {
      const changed =
        a.decisionType !== b.decisionType ||
        a.reason !== b.reason ||
        a.priority !== b.priority ||
        JSON.stringify(a.matchCriteria) !== JSON.stringify(b.matchCriteria);
      diffs.push({
        kind: changed ? "changed" : "unchanged",
        ruleA: a,
        ruleB: b,
      });
    }
  }

  const order: Record<DiffKind, number> = { changed: 0, added: 1, removed: 2, unchanged: 3 };
  diffs.sort((a, b) => order[a.kind] - order[b.kind]);
  return diffs;
}

// ---------------------------------------------------------------------------
// Decision badge mapping
// ---------------------------------------------------------------------------

const decisionVariant: Record<string, "success" | "danger" | "warning" | "info" | "governance"> = {
  allow: "success",
  deny: "governance",
  require_approval: "warning",
  throttle: "info",
};

// ---------------------------------------------------------------------------
// Compact rule card
// ---------------------------------------------------------------------------

function RuleCard({ rule, highlight }: { rule: PolicyRule; highlight?: string }) {
  const capabilities = (rule.matchCriteria?.capabilities as string[] | undefined) ?? [];
  const riskTags = (rule.matchCriteria?.riskTags as string[] | undefined) ?? [];

  return (
    <div
      className={cn(
        "rounded-lg border px-3 py-2 text-xs",
        highlight === "green" && "border-success/40 bg-success/5",
        highlight === "red" && "border-danger/40 bg-danger/5",
        highlight === "yellow" && "border-warning/40 bg-warning/5",
        !highlight && "border-border",
      )}
    >
      <div className="flex items-center justify-between">
        <span className="font-mono text-muted-foreground">{rule.id.slice(0, 12)}</span>
        <Badge variant={decisionVariant[rule.decisionType ?? ""] ?? "default"}>
          {rule.decisionType}
        </Badge>
      </div>
      {rule.reason && <p className="mt-1 text-muted-foreground italic">{rule.reason}</p>}
      {(capabilities.length > 0 || riskTags.length > 0) && (
        <div className="mt-1 flex flex-wrap gap-1">
          {capabilities.map((c) => (
            <Badge key={c} variant="info" className="text-xs">{c}</Badge>
          ))}
          {riskTags.map((t) => (
            <Badge key={t} variant="danger" className="text-xs">{t}</Badge>
          ))}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// PolicyReviewModal
// ---------------------------------------------------------------------------

export interface PolicyReviewModalProps {
  bundle: PolicyBundle;
  onClose: () => void;
  onApproved?: () => void;
}

export function PolicyReviewModal({ bundle, onClose, onApproved }: PolicyReviewModalProps) {
  const [rejectReason, setRejectReason] = useState("");
  const [showRejectForm, setShowRejectForm] = useState(false);
  const [rejected, setRejected] = useState(false);

  const publishPolicy = usePublishPolicy();

  // Get the latest snapshot for this bundle to diff against
  const { data: snapshotsData } = usePolicySnapshots();
  const latestSnapshotId = useMemo(() => {
    const snaps = snapshotsData?.items ?? [];
    // Find the most recent snapshot (first in list, usually sorted newest-first)
    return snaps.length > 0 ? snaps[0].id : null;
  }, [snapshotsData]);

  const { data: snapshot, isLoading: snapLoading } = usePolicySnapshot(latestSnapshotId);

  // Diff current bundle rules vs. published snapshot rules
  const diffs = useMemo(() => {
    const publishedRules = snapshot?.rules ?? [];
    return diffRules(publishedRules, bundle.rules);
  }, [snapshot, bundle.rules]);

  const changedCount = diffs.filter((d) => d.kind !== "unchanged").length;

  const handleApprove = useCallback(() => {
    publishPolicy.mutate(
      { bundleId: bundle.id },
      { onSuccess: () => { onApproved?.(); onClose(); } },
    );
  }, [bundle.id, publishPolicy, onApproved, onClose]);

  const handleReject = useCallback(() => {
    // No backend reject endpoint — just close and mark as rejected locally
    setRejected(true);
    setTimeout(onClose, 1500);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <Card className="relative z-10 w-full max-w-2xl max-h-[80vh] overflow-hidden flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-5 py-4">
          <div>
            <h3 className="font-display text-lg font-semibold text-ink">
              Review: {bundle.name}
            </h3>
            <p className="text-xs text-muted-foreground">
              v{bundle.version ?? "draft"}
              {bundle.author && <> &middot; by {bundle.author}</>}
              {changedCount > 0 && <> &middot; {changedCount} change{changedCount !== 1 ? "s" : ""}</>}
            </p>
          </div>
          <button type="button" onClick={onClose} className="rounded-lg p-1 text-muted-foreground hover:bg-surface2 hover:text-ink transition-colors">
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto px-5 py-4 space-y-4">
          {/* Diff summary */}
          <div className="flex items-center gap-4 text-xs">
            {diffs.filter((d) => d.kind === "added").length > 0 && (
              <span className="font-semibold text-success">
                +{diffs.filter((d) => d.kind === "added").length} added
              </span>
            )}
            {diffs.filter((d) => d.kind === "removed").length > 0 && (
              <span className="font-semibold text-danger">
                -{diffs.filter((d) => d.kind === "removed").length} removed
              </span>
            )}
            {diffs.filter((d) => d.kind === "changed").length > 0 && (
              <span className="font-semibold text-warning">
                {diffs.filter((d) => d.kind === "changed").length} changed
              </span>
            )}
            {diffs.filter((d) => d.kind === "unchanged").length > 0 && (
              <span className="text-muted-foreground">
                {diffs.filter((d) => d.kind === "unchanged").length} unchanged
              </span>
            )}
          </div>

          {snapLoading && (
            <div className="flex items-center justify-center py-8 text-xs text-muted-foreground">
              <Loader className="mr-2 h-3.5 w-3.5 animate-spin" />
              Loading snapshot for comparison...
            </div>
          )}

          {!snapLoading && !latestSnapshotId && (
            <div className="rounded-lg border border-dashed border-border px-4 py-6 text-center text-xs text-muted-foreground">
              No previous snapshot to compare against. This will be the first publish.
            </div>
          )}

          {/* Diff list */}
          {!snapLoading && diffs.length > 0 && (
            <div className="space-y-2">
              {diffs
                .filter((d) => d.kind !== "unchanged")
                .map((diff, i) => {
                  const key = diff.ruleA?.id ?? diff.ruleB?.id ?? String(i);
                  const highlightColor: Record<DiffKind, string | undefined> = {
                    added: "green",
                    removed: "red",
                    changed: "yellow",
                    unchanged: undefined,
                  };
                  const rule = diff.ruleB ?? diff.ruleA;
                  if (!rule) return null;
                  return (
                    <div key={key} className="flex items-start gap-2">
                      <Badge
                        variant={
                          diff.kind === "added" ? "success" :
                          diff.kind === "removed" ? "danger" : "warning"
                        }
                        className="mt-1 text-xs shrink-0"
                      >
                        {diff.kind}
                      </Badge>
                      <div className="flex-1">
                        <RuleCard rule={rule} highlight={highlightColor[diff.kind]} />
                      </div>
                    </div>
                  );
                })}
            </div>
          )}

          {/* Reject form */}
          {showRejectForm && (
            <div className="space-y-2 rounded-lg border border-danger/30 bg-danger/5 p-3">
              <label className="text-xs font-medium text-danger">Rejection reason</label>
              <Textarea
                value={rejectReason}
                onChange={(e) => setRejectReason(e.target.value)}
                placeholder="Explain why this policy change is rejected..."
                rows={3}
              />
              <div className="flex justify-end gap-2">
                <Button variant="ghost" size="sm" onClick={() => setShowRejectForm(false)}>
                  Cancel
                </Button>
                <Button variant="danger" size="sm" onClick={handleReject} disabled={!rejectReason.trim()}>
                  Confirm Reject
                </Button>
              </div>
            </div>
          )}

          {rejected && (
            <div className="rounded-lg bg-danger/10 px-4 py-3 text-sm font-semibold text-danger">
              Policy change rejected.
            </div>
          )}
        </div>

        {/* Footer actions */}
        {!rejected && (
          <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
            <Button variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            {!showRejectForm && (
              <Button
                variant="danger"
                onClick={() => setShowRejectForm(true)}
              >
                <ThumbsDown className="h-4 w-4" />
                Reject
              </Button>
            )}
            <Button onClick={handleApprove} disabled={publishPolicy.isPending}>
              <ThumbsUp className="h-4 w-4" />
              {publishPolicy.isPending ? "Publishing..." : "Approve & Publish"}
            </Button>
          </div>
        )}
      </Card>
    </div>
  );
}
