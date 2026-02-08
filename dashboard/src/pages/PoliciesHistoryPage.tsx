import { useState, useMemo, useCallback } from "react";
import {
  RotateCcw,
  Loader,
  ChevronDown,
  ChevronUp,
  Filter,
  X,
  Download,
} from "lucide-react";
import { usePolicyBundleContext } from "../components/policy/PolicyBundleContext";
import { PublishControls } from "../components/policy/PublishControls";
import {
  usePolicySnapshots,
  usePolicySnapshot,
  useRollbackPolicy,
  usePolicyAudit,
  type PolicySnapshotSummary,
} from "../hooks/usePolicies";
import { PolicyTimeline } from "../components/policy/PolicyTimeline";
import { Badge } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { Textarea } from "../components/ui/Textarea";
import { Select } from "../components/ui/Select";
import { cn } from "../lib/utils";
import { exportPdf, type PdfSection } from "../lib/pdfExport";
import { useAuth } from "../hooks/useAuth";
import type { PolicyRule } from "../api/types";

// ---------------------------------------------------------------------------
// Helpers
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
      diffs.push({ kind: "removed", ruleA: a });
    } else if (!a && b) {
      diffs.push({ kind: "added", ruleB: b });
    } else if (a && b) {
      const changed =
        a.decisionType !== b.decisionType ||
        a.reason !== b.reason ||
        a.priority !== b.priority ||
        JSON.stringify(a.matchCriteria) !== JSON.stringify(b.matchCriteria);
      diffs.push({ kind: changed ? "changed" : "unchanged", ruleA: a, ruleB: b });
    }
  }
  const order: Record<DiffKind, number> = { changed: 0, added: 1, removed: 2, unchanged: 3 };
  diffs.sort((a, b) => order[a.kind] - order[b.kind]);
  return diffs;
}

type DateRange = "24h" | "7d" | "30d" | "all";

function matchesDateRange(createdAt: string, range: DateRange): boolean {
  if (range === "all") return true;
  const ms = Date.now() - new Date(createdAt).getTime();
  const limits: Record<string, number> = {
    "24h": 24 * 60 * 60 * 1000,
    "7d": 7 * 24 * 60 * 60 * 1000,
    "30d": 30 * 24 * 60 * 60 * 1000,
  };
  return ms <= (limits[range] ?? Infinity);
}

// ---------------------------------------------------------------------------
// Inline diff viewer (rule summary cards)
// ---------------------------------------------------------------------------

const decisionVariant: Record<string, "success" | "danger" | "warning" | "info"> = {
  allow: "success",
  deny: "danger",
  require_approval: "warning",
  throttle: "info",
};

function RuleSummary({ rule, highlight }: { rule: PolicyRule; highlight?: string }) {
  const capabilities = (rule.matchCriteria?.capabilities as string[] | undefined) ?? [];
  const riskTags = (rule.matchCriteria?.riskTags as string[] | undefined) ?? [];
  return (
    <div
      className={cn(
        "rounded-xl border px-3 py-2.5 text-xs",
        highlight === "green" && "border-success/40 bg-success/5",
        highlight === "red" && "border-danger/40 bg-danger/5",
        highlight === "yellow" && "border-warning/40 bg-warning/5",
        !highlight && "border-border",
      )}
    >
      <div className="flex items-center justify-between">
        <span className="font-mono text-muted">{rule.id.slice(0, 10)}</span>
        <Badge variant={decisionVariant[rule.decisionType] ?? "default"}>{rule.decisionType}</Badge>
      </div>
      {rule.reason && <p className="mt-1 text-muted italic">{rule.reason}</p>}
      {(capabilities.length > 0 || riskTags.length > 0) && (
        <div className="mt-1.5 flex flex-wrap gap-1">
          {capabilities.map((c) => (
            <Badge key={c} variant="info" className="text-[10px]">{c}</Badge>
          ))}
          {riskTags.map((t) => (
            <Badge key={t} variant="danger" className="text-[10px]">{t}</Badge>
          ))}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Snapshot diff expanded section
// ---------------------------------------------------------------------------

function ExpandedDiff({ snapshotId, prevSnapshotId }: { snapshotId: string; prevSnapshotId: string | null }) {
  const { data: snapB, isLoading: loadingB } = usePolicySnapshot(snapshotId);
  const { data: snapA, isLoading: loadingA } = usePolicySnapshot(prevSnapshotId);

  const diffs = useMemo(() => {
    const rulesA = snapA?.rules ?? [];
    const rulesB = snapB?.rules ?? [];
    return diffRules(rulesA, rulesB);
  }, [snapA, snapB]);

  if (loadingA || loadingB) {
    return (
      <div className="flex items-center gap-2 py-6 text-xs text-muted">
        <Loader className="h-3.5 w-3.5 animate-spin" />
        Loading diff...
      </div>
    );
  }

  if (diffs.length === 0) {
    return (
      <div className="rounded-xl border-2 border-success/40 bg-success/5 px-4 py-3 text-sm font-semibold text-success">
        Snapshots are identical — no rule differences.
      </div>
    );
  }

  const added = diffs.filter((d) => d.kind === "added").length;
  const removed = diffs.filter((d) => d.kind === "removed").length;
  const changed = diffs.filter((d) => d.kind === "changed").length;
  const unchanged = diffs.filter((d) => d.kind === "unchanged").length;

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-4 text-xs">
        {added > 0 && <span className="font-semibold text-success">+{added} added</span>}
        {removed > 0 && <span className="font-semibold text-danger">-{removed} removed</span>}
        {changed > 0 && <span className="font-semibold text-warning">{changed} changed</span>}
        {unchanged > 0 && <span className="text-muted">{unchanged} unchanged</span>}
      </div>
      <div className="space-y-2">
        {diffs.map((diff, i) => {
          const key = diff.ruleA?.id ?? diff.ruleB?.id ?? String(i);
          return (
            <div key={key} className="grid grid-cols-2 gap-3">
              <div>
                {diff.ruleA ? (
                  <RuleSummary
                    rule={diff.ruleA}
                    highlight={diff.kind === "removed" ? "red" : diff.kind === "changed" ? "yellow" : undefined}
                  />
                ) : (
                  <div className="rounded-xl border border-dashed border-success/40 bg-success/5 px-3 py-4 text-center text-[10px] text-muted">
                    Not present in previous
                  </div>
                )}
              </div>
              <div>
                {diff.ruleB ? (
                  <RuleSummary
                    rule={diff.ruleB}
                    highlight={diff.kind === "added" ? "green" : diff.kind === "changed" ? "yellow" : undefined}
                  />
                ) : (
                  <div className="rounded-xl border border-dashed border-danger/40 bg-danger/5 px-3 py-4 text-center text-[10px] text-muted">
                    Not present in this version
                  </div>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Rollback confirmation dialog
// ---------------------------------------------------------------------------

function RollbackDialog({
  snapshot,
  isPending,
  onConfirm,
  onCancel,
}: {
  snapshot: PolicySnapshotSummary;
  isPending: boolean;
  onConfirm: (note: string) => void;
  onCancel: () => void;
}) {
  const [note, setNote] = useState("");

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <Card className="relative z-10 w-full max-w-md">
        <div className="space-y-4">
          <h3 className="font-display text-lg font-semibold text-ink">
            Rollback to v{snapshot.version ?? "?"}
          </h3>
          <p className="text-sm text-muted">
            This will replace the current active policy with the snapshot from{" "}
            <strong>{timeAgo(snapshot.createdAt)}</strong>
            {snapshot.createdBy && <> by {snapshot.createdBy}</>}.
            A new snapshot will be created for safety.
          </p>
          <div>
            <label htmlFor="rollback-note" className="mb-1 block text-xs font-semibold text-muted">
              Reason (optional)
            </label>
            <Textarea
              id="rollback-note"
              rows={2}
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="Why are you rolling back?"
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={onCancel} disabled={isPending}>
              Cancel
            </Button>
            <Button variant="danger" size="sm" onClick={() => onConfirm(note)} disabled={isPending}>
              {isPending ? "Rolling back\u2026" : "Rollback"}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main page
// ---------------------------------------------------------------------------

export default function PoliciesHistoryPage() {
  const { bundleId, bundles } = usePolicyBundleContext();
  const ruleCount = bundles.find((b) => b.id === bundleId)?.rules.length ?? 0;
  const { tenantId } = useAuth();

  const { data: snapshotsData, isLoading } = usePolicySnapshots();
  const { data: auditData } = usePolicyAudit();
  const auditEntries = auditData?.items ?? [];
  const snapshots = useMemo(
    () =>
      [...(snapshotsData?.items ?? [])].sort(
        (a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime(),
      ),
    [snapshotsData],
  );

  // Filters
  const [dateRange, setDateRange] = useState<DateRange>("all");
  const [authorFilter, setAuthorFilter] = useState("");

  const authors = useMemo(() => {
    const set = new Set<string>();
    for (const s of snapshots) {
      if (s.createdBy) set.add(s.createdBy);
    }
    return Array.from(set).sort();
  }, [snapshots]);

  const filtered = useMemo(
    () =>
      snapshots.filter((s) => {
        if (!matchesDateRange(s.createdAt, dateRange)) return false;
        if (authorFilter && s.createdBy !== authorFilter) return false;
        return true;
      }),
    [snapshots, dateRange, authorFilter],
  );

  const hasActiveFilter = dateRange !== "all" || authorFilter !== "";

  // Expanded diff
  const [expandedId, setExpandedId] = useState<string | null>(null);

  // Rollback
  const [rollbackTarget, setRollbackTarget] = useState<PolicySnapshotSummary | null>(null);
  const [rollbackSuccess, setRollbackSuccess] = useState(false);
  const rollbackPolicy = useRollbackPolicy();

  const handleRollback = useCallback(
    (note: string) => {
      if (!rollbackTarget) return;
      rollbackPolicy.mutate(
        { snapshotId: rollbackTarget.id, note: note || undefined },
        {
          onSuccess: () => {
            setRollbackTarget(null);
            setRollbackSuccess(true);
            setTimeout(() => setRollbackSuccess(false), 3000);
          },
        },
      );
    },
    [rollbackTarget, rollbackPolicy],
  );

  // PDF export
  const [pdfExporting, setPdfExporting] = useState(false);
  const handleExportPdf = useCallback(async () => {
    setPdfExporting(true);
    try {
      const sections: PdfSection[] = [];
      sections.push({ type: "heading", content: "Policy History Report" });

      // Snapshot table
      if (filtered.length > 0) {
        const tableRows: string[][] = [["Version", "Date", "Author", "Note"]];
        for (const snap of filtered) {
          tableRows.push([
            `v${snap.version ?? "?"}`,
            new Date(snap.createdAt).toLocaleString(),
            snap.createdBy ?? "—",
            snap.note ?? "—",
          ]);
        }
        sections.push({ type: "table", content: tableRows, label: "Snapshots" });
      } else {
        sections.push({ type: "text", content: "No snapshots match the current filters." });
      }

      sections.push({
        type: "text",
        content: `Total snapshots: ${snapshots.length}. Filtered: ${filtered.length}.`,
      });

      await exportPdf({
        title: "Policy History",
        tenantName: tenantId ?? undefined,
        sections,
      });
    } finally {
      setPdfExporting(false);
    }
  }, [filtered, snapshots, tenantId]);

  return (
    <div className="space-y-6">
      {/* Publish controls (compact) */}
      {bundleId && <PublishControls bundleId={bundleId} ruleCount={ruleCount} />}

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-3">
        <Filter className="h-4 w-4 text-muted" />

        {/* Date range pills */}
        <div className="flex rounded-full border border-border">
          {(["24h", "7d", "30d", "all"] as DateRange[]).map((range) => (
            <button
              key={range}
              type="button"
              onClick={() => setDateRange(range)}
              className={cn(
                "px-3 py-1 text-xs font-semibold transition first:rounded-l-full last:rounded-r-full",
                dateRange === range
                  ? "bg-accent/15 text-accent"
                  : "text-muted hover:text-ink",
              )}
            >
              {range === "all" ? "All time" : `Last ${range}`}
            </button>
          ))}
        </div>

        {/* Author filter */}
        {authors.length > 0 && (
          <Select
            value={authorFilter}
            onChange={(e) => setAuthorFilter(e.target.value)}
            className="!w-auto !py-1 text-xs"
          >
            <option value="">All authors</option>
            {authors.map((a) => (
              <option key={a} value={a}>{a}</option>
            ))}
          </Select>
        )}

        {hasActiveFilter && (
          <button
            type="button"
            onClick={() => { setDateRange("all"); setAuthorFilter(""); }}
            className="flex items-center gap-1 text-xs text-muted hover:text-ink"
          >
            <X className="h-3 w-3" />
            Reset
          </button>
        )}

        <span className="ml-auto text-xs text-muted">
          Showing {filtered.length} of {snapshots.length} snapshots
        </span>
        <Button variant="ghost" size="sm" onClick={handleExportPdf} disabled={pdfExporting}>
          <Download className="h-3.5 w-3.5" />
          {pdfExporting ? "Exporting…" : "Export PDF"}
        </Button>
      </div>

      {/* Rollback success */}
      {rollbackSuccess && (
        <div className="rounded-xl border-2 border-success/40 bg-success/5 px-4 py-3 text-sm font-semibold text-success">
          Rollback successful. Policy has been restored.
        </div>
      )}

      {/* Snapshot list */}
      {isLoading ? (
        <div className="flex items-center justify-center py-16 text-sm text-muted">
          <Loader className="mr-2 h-4 w-4 animate-spin" />
          Loading snapshots...
        </div>
      ) : filtered.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-border px-6 py-12 text-center text-sm text-muted">
          {snapshots.length === 0
            ? "No snapshots yet. Publish a policy to create the first snapshot."
            : "No snapshots match the current filters."}
        </div>
      ) : (
        <div className="divide-y divide-border rounded-2xl border border-border">
          {filtered.map((snap, i) => {
            const isExpanded = expandedId === snap.id;
            const prevSnap = filtered[i + 1] ?? null;
            return (
              <div key={snap.id}>
                <div className="flex items-center gap-4 px-4 py-3">
                  {/* Version */}
                  <Badge variant="info" className="flex-shrink-0">
                    v{snap.version ?? "?"}
                  </Badge>

                  {/* Timestamp */}
                  <div className="min-w-0 flex-1">
                    <span
                      className="text-sm font-medium text-ink"
                      title={new Date(snap.createdAt).toLocaleString()}
                    >
                      {timeAgo(snap.createdAt)}
                    </span>
                    {snap.createdBy && (
                      <span className="ml-2 text-xs text-muted">by {snap.createdBy}</span>
                    )}
                    {snap.note && (
                      <span className="ml-2 text-xs italic text-muted">{snap.note}</span>
                    )}
                  </div>

                  {/* Actions */}
                  <div className="flex flex-shrink-0 items-center gap-2">
                    <Button
                      variant="ghost"
                      size="sm"
                      type="button"
                      onClick={() => setExpandedId(isExpanded ? null : snap.id)}
                    >
                      {isExpanded ? (
                        <ChevronUp className="h-3.5 w-3.5" />
                      ) : (
                        <ChevronDown className="h-3.5 w-3.5" />
                      )}
                      Diff
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      type="button"
                      onClick={() => setRollbackTarget(snap)}
                      disabled={rollbackPolicy.isPending}
                    >
                      <RotateCcw className="h-3.5 w-3.5" />
                      Rollback
                    </Button>
                  </div>
                </div>

                {/* Expanded diff */}
                {isExpanded && (
                  <div className="border-t border-border bg-surface2/30 px-4 py-4">
                    {prevSnap ? (
                      <ExpandedDiff snapshotId={snap.id} prevSnapshotId={prevSnap.id} />
                    ) : (
                      <p className="text-xs text-muted">
                        This is the earliest snapshot — no previous version to compare against.
                      </p>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      {/* Activity Timeline */}
      {auditEntries.length > 0 && (
        <div className="space-y-3">
          <h3 className="font-display text-lg font-semibold text-ink">
            Activity Timeline
          </h3>
          <PolicyTimeline entries={auditEntries} />
        </div>
      )}

      {/* Rollback confirmation */}
      {rollbackTarget && (
        <RollbackDialog
          snapshot={rollbackTarget}
          isPending={rollbackPolicy.isPending}
          onConfirm={handleRollback}
          onCancel={() => setRollbackTarget(null)}
        />
      )}
    </div>
  );
}
