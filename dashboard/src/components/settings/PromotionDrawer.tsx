import { useMemo, useState } from "react";
import { ArrowRight, Plus, Minus, RefreshCw, X } from "lucide-react";
import { Drawer } from "../ui/Drawer";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";
import { ConfirmDialog } from "../ui/ConfirmDialog";
import { cn } from "../../lib/utils";
import type { Environment } from "../../api/types";

type DiffKind = "added" | "changed" | "removed" | "same";

interface DiffEntry {
  key: string;
  kind: DiffKind;
  sourceVal: string;
  targetVal: string;
}

function stringify(v: unknown): string {
  return typeof v === "string" ? v : JSON.stringify(v ?? "");
}

function computeDiff(source: Record<string, unknown>, target: Record<string, unknown>): DiffEntry[] {
  const allKeys = new Set([...Object.keys(source), ...Object.keys(target)]);
  const entries: DiffEntry[] = [];

  for (const key of allKeys) {
    const inSource = key in source;
    const inTarget = key in target;
    const sourceVal = stringify(source[key]);
    const targetVal = stringify(target[key]);

    if (inSource && !inTarget) {
      entries.push({ key, kind: "added", sourceVal, targetVal: "" });
    } else if (!inSource && inTarget) {
      entries.push({ key, kind: "removed", sourceVal: "", targetVal });
    } else if (sourceVal !== targetVal) {
      entries.push({ key, kind: "changed", sourceVal, targetVal });
    } else {
      entries.push({ key, kind: "same", sourceVal, targetVal });
    }
  }

  const order: Record<DiffKind, number> = { added: 0, changed: 1, removed: 2, same: 3 };
  entries.sort((a, b) => order[a.kind] - order[b.kind]);
  return entries;
}

const DIFF_STYLE: Record<DiffKind, { bg: string; icon: typeof Plus; label: string }> = {
  added: { bg: "bg-success/10", icon: Plus, label: "New" },
  changed: { bg: "bg-warning/10", icon: RefreshCw, label: "Changed" },
  removed: { bg: "bg-danger/10", icon: Minus, label: "Removed" },
  same: { bg: "", icon: RefreshCw, label: "" },
};

interface PromotionDrawerProps {
  source: Environment;
  target: Environment;
  onConfirm: () => void;
  onClose: () => void;
  isPending?: boolean;
}

export function PromotionDrawer({ source, target, onConfirm, onClose, isPending }: PromotionDrawerProps) {
  const [confirmOpen, setConfirmOpen] = useState(false);

  const diff = useMemo(
    () => computeDiff(source.config ?? {}, target.config ?? {}),
    [source.config, target.config],
  );

  const changes = diff.filter((d) => d.kind !== "same");

  return (
    <Drawer open onClose={onClose} size="lg">
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <h3 className="font-display text-lg font-semibold text-ink">Promote Environment</h3>
          <button type="button" onClick={onClose} className="rounded-full p-1 hover:bg-surface2">
            <X className="h-5 w-5 text-muted-foreground" />
          </button>
        </div>

        {/* Source → Target header */}
        <div className="flex items-center justify-center gap-4 rounded-2xl border border-border bg-surface2/50 px-6 py-4">
          <div className="text-center">
            <p className="text-xs text-muted-foreground">Source</p>
            <p className="font-display text-sm font-semibold text-ink capitalize">{source.name}</p>
          </div>
          <ArrowRight className="h-5 w-5 text-accent" />
          <div className="text-center">
            <p className="text-xs text-muted-foreground">Target</p>
            <p className="font-display text-sm font-semibold text-ink capitalize">{target.name}</p>
          </div>
        </div>

        {/* Diff summary */}
        <div className="flex items-center gap-3">
          <Badge variant="info">{changes.length} {changes.length === 1 ? "change" : "changes"}</Badge>
          {changes.length === 0 && (
            <span className="text-xs text-muted-foreground">Configurations are identical</span>
          )}
        </div>

        {/* Diff table */}
        <div className="overflow-hidden rounded-xl border border-border">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-border bg-surface2/50">
                <th className="px-3 py-2 text-left font-semibold text-muted-foreground">Key</th>
                <th className="px-3 py-2 text-left font-semibold text-muted-foreground">Source</th>
                <th className="px-3 py-2 text-left font-semibold text-muted-foreground">Target</th>
                <th className="px-3 py-2 w-20" />
              </tr>
            </thead>
            <tbody>
              {diff.map((entry) => {
                const style = DIFF_STYLE[entry.kind];
                const Icon = style.icon;
                return (
                  <tr key={entry.key} className={cn("border-b border-border last:border-0", style.bg)}>
                    <td className="px-3 py-2 font-mono font-medium text-ink">{entry.key}</td>
                    <td className="px-3 py-2 font-mono text-muted-foreground truncate max-w-[160px]">
                      {entry.sourceVal || <span className="italic text-muted/50">-</span>}
                    </td>
                    <td className="px-3 py-2 font-mono text-muted-foreground truncate max-w-[160px]">
                      {entry.targetVal || <span className="italic text-muted/50">-</span>}
                    </td>
                    <td className="px-3 py-2 text-right">
                      {entry.kind !== "same" && (
                        <span className="inline-flex items-center gap-1 text-xs font-semibold uppercase">
                          <Icon className="h-3 w-3" />
                          {style.label}
                        </span>
                      )}
                    </td>
                  </tr>
                );
              })}
              {diff.length === 0 && (
                <tr>
                  <td colSpan={4} className="px-3 py-6 text-center text-muted-foreground">
                    No configuration keys in either environment
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {/* Action */}
        <div className="flex items-center justify-end gap-2 border-t border-border pt-4">
          <Button variant="ghost" size="sm" type="button" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            size="sm"
            type="button"
            onClick={() => setConfirmOpen(true)}
            disabled={changes.length === 0 || isPending}
          >
            <ArrowRight className="h-3.5 w-3.5" />
            Promote to {target.name}
          </Button>
        </div>
      </div>

      <ConfirmDialog
        open={confirmOpen}
        title="Confirm Promotion"
        message={`This will overwrite ${target.name} configuration with values from ${source.name}. ${changes.length} key(s) will be affected. This action cannot be undone.`}
        confirmLabel="Promote"
        confirmVariant="danger"
        isPending={isPending}
        onConfirm={() => {
          setConfirmOpen(false);
          onConfirm();
        }}
        onCancel={() => setConfirmOpen(false)}
      />
    </Drawer>
  );
}
