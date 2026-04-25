import { useState, useMemo } from "react";
import { Badge } from "../ui/Badge";
import { Card } from "../ui/Card";
import { Loader } from "lucide-react";
import {
  usePolicySnapshots,
  usePolicySnapshot,
  type PolicySnapshotSummary,
} from "../../hooks/usePolicies";
import { cn } from "../../lib/utils";
import type { PolicyRule } from "../../api/types";

// ---------------------------------------------------------------------------
// Diff types
// ---------------------------------------------------------------------------

type DiffKind = "added" | "removed" | "changed" | "unchanged";

interface RuleDiff {
  kind: DiffKind;
  ruleA?: PolicyRule;
  ruleB?: PolicyRule;
}

// ---------------------------------------------------------------------------
// Compute diff between two rule sets
// ---------------------------------------------------------------------------

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
      // Compare both legacy (decisionType / matchCriteria) and canonical
      // (decision / match) shapes; a canonical-only change must not be
      // reported as unchanged.
      const decisionA = a.decisionType ?? a.decision;
      const decisionB = b.decisionType ?? b.decision;
      const matchA = a.matchCriteria ?? a.match;
      const matchB = b.matchCriteria ?? b.match;
      const changed =
        decisionA !== decisionB ||
        a.reason !== b.reason ||
        a.priority !== b.priority ||
        JSON.stringify(matchA) !== JSON.stringify(matchB);
      diffs.push({
        kind: changed ? "changed" : "unchanged",
        ruleA: a,
        ruleB: b,
      });
    }
  }

  // Sort: changed first, then added, removed, unchanged
  const order: Record<DiffKind, number> = { changed: 0, added: 1, removed: 2, unchanged: 3 };
  diffs.sort((a, b) => order[a.kind] - order[b.kind]);

  return diffs;
}

// ---------------------------------------------------------------------------
// Decision badge
// ---------------------------------------------------------------------------

const decisionVariant: Record<string, "success" | "danger" | "warning" | "info" | "governance"> = {
  allow: "success",
  deny: "governance",
  require_approval: "warning",
  throttle: "info",
};

// ---------------------------------------------------------------------------
// Rule summary (compact)
// ---------------------------------------------------------------------------

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
        <span className="font-mono text-muted-foreground">{rule.id.slice(0, 10)}</span>
        <Badge variant={decisionVariant[rule.decisionType ?? ""] ?? "default"}>
          {rule.decisionType}
        </Badge>
      </div>
      {rule.reason && (
        <p className="mt-1 text-muted-foreground italic">{rule.reason}</p>
      )}
      {(capabilities.length > 0 || riskTags.length > 0) && (
        <div className="mt-1.5 flex flex-wrap gap-1">
          {capabilities.map((c) => (
            <Badge key={c} variant="info" className="text-xs">
              {c}
            </Badge>
          ))}
          {riskTags.map((t) => (
            <Badge key={t} variant="danger" className="text-xs">
              {t}
            </Badge>
          ))}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Snapshot selector
// ---------------------------------------------------------------------------

function SnapshotSelect({
  label,
  value,
  snapshots,
  onChange,
}: {
  label: string;
  value: string;
  snapshots: PolicySnapshotSummary[];
  onChange: (id: string) => void;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs font-semibold text-muted-foreground">{label}</label>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded-xl border border-border bg-transparent px-3 py-2 text-sm text-ink"
      >
        <option value="">Select snapshot...</option>
        {snapshots.map((s) => (
          <option key={s.id} value={s.id}>
            v{s.version ?? "?"} — {new Date(s.createdAt).toLocaleDateString()} by {s.createdBy ?? "system"}
          </option>
        ))}
      </select>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Diff summary
// ---------------------------------------------------------------------------

function DiffSummary({ diffs }: { diffs: RuleDiff[] }) {
  const added = diffs.filter((d) => d.kind === "added").length;
  const removed = diffs.filter((d) => d.kind === "removed").length;
  const changed = diffs.filter((d) => d.kind === "changed").length;
  const unchanged = diffs.filter((d) => d.kind === "unchanged").length;

  return (
    <div className="flex items-center gap-4 text-xs">
      {added > 0 && <span className="font-semibold text-success">+{added} added</span>}
      {removed > 0 && <span className="font-semibold text-danger">-{removed} removed</span>}
      {changed > 0 && <span className="font-semibold text-warning">{changed} changed</span>}
      {unchanged > 0 && <span className="text-muted-foreground">{unchanged} unchanged</span>}
    </div>
  );
}

// ---------------------------------------------------------------------------
// SnapshotComparison
// ---------------------------------------------------------------------------

export function SnapshotComparison() {
  const [leftId, setLeftId] = useState("");
  const [rightId, setRightId] = useState("");

  const { data: snapshotsData, isLoading: snapshotsLoading } = usePolicySnapshots();
  const snapshots = snapshotsData?.items ?? [];

  const { data: leftSnap, isLoading: leftLoading } = usePolicySnapshot(leftId || null);
  const { data: rightSnap, isLoading: rightLoading } = usePolicySnapshot(rightId || null);

  const diffs = useMemo(() => {
    if (!leftSnap?.rules || !rightSnap?.rules) return [];
    return diffRules(leftSnap.rules, rightSnap.rules);
  }, [leftSnap, rightSnap]);

  const isLoading = leftLoading || rightLoading;
  const bothSelected = !!leftId && !!rightId;

  return (
    <div className="space-y-4">
      <Card>
        <div className="space-y-3">
          <h3 className="font-display text-base font-semibold text-ink">
            Compare Snapshots
          </h3>

          {snapshotsLoading ? (
            <div className="flex items-center gap-2 py-4 text-xs text-muted-foreground">
              <Loader className="h-3.5 w-3.5 animate-spin" />
              Loading snapshots...
            </div>
          ) : snapshots.length < 2 ? (
            <p className="py-4 text-xs text-muted-foreground">
              Need at least 2 snapshots to compare. Publish more versions.
            </p>
          ) : (
            <div className="grid grid-cols-2 gap-4">
              <SnapshotSelect
                label="Snapshot A"
                value={leftId}
                snapshots={snapshots}
                onChange={setLeftId}
              />
              <SnapshotSelect
                label="Snapshot B"
                value={rightId}
                snapshots={snapshots}
                onChange={setRightId}
              />
            </div>
          )}
        </div>
      </Card>

      {/* Loading */}
      {bothSelected && isLoading && (
        <div className="flex items-center justify-center py-8 text-xs text-muted-foreground">
          <Loader className="mr-2 h-3.5 w-3.5 animate-spin" />
          Loading snapshot rules...
        </div>
      )}

      {/* Diff summary */}
      {bothSelected && !isLoading && diffs.length > 0 && <DiffSummary diffs={diffs} />}

      {/* Side-by-side comparison */}
      {bothSelected && !isLoading && diffs.length > 0 && (
        <div className="space-y-3">
          {diffs.map((diff, i) => {
            const key = diff.ruleA?.id ?? diff.ruleB?.id ?? String(i);
            return (
              <div key={key} className="grid grid-cols-2 gap-3">
                {/* Left (A) */}
                <div>
                  {diff.ruleA ? (
                    <RuleSummary
                      rule={diff.ruleA}
                      highlight={
                        diff.kind === "removed"
                          ? "red"
                          : diff.kind === "changed"
                            ? "yellow"
                            : undefined
                      }
                    />
                  ) : (
                    <div className="rounded-xl border border-dashed border-success/40 bg-success/5 px-3 py-4 text-center text-xs text-muted-foreground">
                      Not present in A
                    </div>
                  )}
                </div>

                {/* Right (B) */}
                <div>
                  {diff.ruleB ? (
                    <RuleSummary
                      rule={diff.ruleB}
                      highlight={
                        diff.kind === "added"
                          ? "green"
                          : diff.kind === "changed"
                            ? "yellow"
                            : undefined
                      }
                    />
                  ) : (
                    <div className="rounded-xl border border-dashed border-danger/40 bg-danger/5 px-3 py-4 text-center text-xs text-muted-foreground">
                      Not present in B
                    </div>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* No diffs */}
      {bothSelected && !isLoading && diffs.length === 0 && leftSnap && rightSnap && (
        <Card className="border-2 border-success/40 bg-success/5">
          <p className="text-sm font-semibold text-success">
            Snapshots are identical — no rule differences.
          </p>
        </Card>
      )}
    </div>
  );
}

