import { useMemo, useState } from "react";
import { Layers } from "lucide-react";
import { EmptyState } from "@/components/ui/EmptyState";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import {
  useBundleVersions,
  useBundleDeployments,
  type BundleDeployment,
} from "@/hooks/useBundle";
import { useDeployBundle } from "@/hooks/useDeployBundle";
import { useRollbackBundle } from "@/hooks/useRollbackBundle";
import { formatRelativeTime } from "@/lib/utils";
import { cn } from "@/lib/utils";

interface BundleDeploymentsTabProps {
  bundleId: string;
}

interface ScopeRow {
  key: string;
  label: string;
  kind: string;
  value?: string;
}

interface PendingMutation {
  action: "promote" | "rollback";
  bundleId: string;
  version: string;
  scope: { kind: string; value?: string };
  scopeLabel: string;
}

function scopeLabel(d: BundleDeployment): string {
  if (d.scope_kind && d.scope_value) return `${d.scope_kind}:${d.scope_value}`;
  return d.scope_kind ?? d.scope ?? "global";
}

function uniqueScopes(deployments: BundleDeployment[]): ScopeRow[] {
  const seen = new Map<string, ScopeRow>();
  for (const d of deployments) {
    const key = scopeLabel(d);
    if (!seen.has(key)) {
      seen.set(key, {
        key,
        label: key,
        kind: d.scope_kind ?? d.scope ?? "global",
        value: d.scope_value,
      });
    }
  }
  return Array.from(seen.values()).sort((a, b) => a.label.localeCompare(b.label));
}

/**
 * Bundle detail — Deployments tab (Dashboard 5 step 7).
 * Renders a scope×version matrix consuming Backend 2's GetActiveDeployment
 * grouping. Cells: active period if a binding exists; click → ConfirmDialog
 * → Promote (if cell empty) or Rollback (if active). Gantt-style timeline
 * is a separate task (Dashboard 6).
 */
export default function BundleDeploymentsTab({ bundleId }: BundleDeploymentsTabProps) {
  const versionsQ = useBundleVersions(bundleId);
  const deploymentsQ = useBundleDeployments(bundleId);
  const deploy = useDeployBundle();
  const rollback = useRollbackBundle();
  const [pending, setPending] = useState<PendingMutation | null>(null);

  const versions = versionsQ.data?.items ?? [];
  const deployments = deploymentsQ.data?.items ?? [];
  const scopes = useMemo(() => uniqueScopes(deployments), [deployments]);

  // Index: "{scopeKey}|{version}" → BundleDeployment for fast cell lookup.
  const cellIndex = useMemo(() => {
    const m = new Map<string, BundleDeployment>();
    for (const d of deployments) {
      m.set(`${scopeLabel(d)}|${d.version}`, d);
    }
    return m;
  }, [deployments]);

  if (versionsQ.isLoading || deploymentsQ.isLoading) {
    return (
      <div className="text-sm text-muted-foreground">Loading deployments…</div>
    );
  }

  if (versions.length === 0 || scopes.length === 0) {
    return (
      <EmptyState
        icon={<Layers className="h-5 w-5" />}
        title="No deployments yet"
        description="Promote a version to a scope from this matrix once the bundle has versions and the scope is targeted."
      />
    );
  }

  function handleCellClick(scope: ScopeRow, version: string) {
    const existing = cellIndex.get(`${scope.key}|${version}`);
    setPending({
      action: existing?.active ? "rollback" : "promote",
      bundleId,
      version,
      scope: { kind: scope.kind, value: scope.value },
      scopeLabel: scope.label,
    });
  }

  function handleConfirm() {
    if (!pending) return;
    if (pending.action === "promote") {
      deploy.mutate(
        { bundleId: pending.bundleId, version: pending.version, scope: pending.scope },
        { onSettled: () => setPending(null) },
      );
    } else {
      rollback.mutate(
        { bundleId: pending.bundleId, scope: pending.scope },
        { onSettled: () => setPending(null) },
      );
    }
  }

  const isPending = deploy.isPending || rollback.isPending;

  return (
    <div className="space-y-4">
      <div className="overflow-x-auto rounded-2xl border border-border bg-surface-1">
        <table className="min-w-full text-sm" aria-label="Bundle deployments matrix (rows: scopes, columns: versions)">
          <thead>
            <tr className="border-b border-border bg-surface-2/40">
              <th scope="col" className="px-3 py-2 text-left font-medium text-muted-foreground">
                Scope
              </th>
              {versions.map((v) => (
                <th
                  key={v.version}
                  scope="col"
                  className="px-3 py-2 text-left font-medium text-foreground"
                >
                  {v.version}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {scopes.map((s) => (
              <tr key={s.key} className="border-b border-border/60 last:border-b-0">
                <th
                  scope="row"
                  className="px-3 py-2 text-left font-medium text-foreground"
                >
                  {s.label}
                </th>
                {versions.map((v) => {
                  const cell = cellIndex.get(`${s.key}|${v.version}`);
                  const active = Boolean(cell?.active);
                  return (
                    <td key={v.version} className="px-3 py-2">
                      <button
                        type="button"
                        onClick={() => handleCellClick(s, v.version)}
                        aria-label={
                          active
                            ? `Rollback ${s.label} from ${v.version}`
                            : `Promote ${v.version} to ${s.label}`
                        }
                        className={cn(
                          "inline-flex w-full min-h-7 items-center rounded-lg px-2 py-1 text-left text-xs transition-colors",
                          active
                            ? "bg-cordum/10 text-cordum hover:bg-cordum/15"
                            : "text-muted-foreground hover:bg-surface-2/70 hover:text-foreground",
                        )}
                      >
                        {active && cell ? (
                          <span>Active · {formatRelativeTime(cell.deployed_at)}</span>
                        ) : (
                          <span aria-hidden="true">—</span>
                        )}
                      </button>
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <ConfirmDialog
        open={pending !== null}
        onCancel={() => setPending(null)}
        onConfirm={handleConfirm}
        title={
          pending?.action === "rollback"
            ? `Rollback ${pending?.scopeLabel}?`
            : `Promote ${pending?.version} to ${pending?.scopeLabel}?`
        }
        description={
          pending?.action === "rollback"
            ? "Re-activates the previous bundle version for this scope. Audit-logged."
            : "Activates this bundle version for this scope. Existing active version is moved to history."
        }
        confirmLabel={pending?.action === "rollback" ? "Rollback" : "Promote"}
        variant={pending?.action === "rollback" ? "destructive" : "default"}
        isPending={isPending}
      />
    </div>
  );
}
