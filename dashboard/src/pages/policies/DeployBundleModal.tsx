import { useEffect, useMemo, useState } from "react";
import { Drawer } from "@/components/ui/Drawer";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { Select } from "@/components/ui/Select";
import { Input } from "@/components/ui/Input";
import { Button } from "@/components/ui/Button";
import { useBundle } from "@/hooks/useBundle";
import { useDeployBundle } from "@/hooks/useDeployBundle";
import type { EdgeMode } from "@/api/generated/model/edgeMode";

interface DeployBundleModalProps {
  bundleId: string;
  version: string;
  open: boolean;
  onClose: () => void;
  onSuccess?: () => void;
  /**
   * Pre-fill the scope picker. Used when the modal is opened from a
   * BundleDeploymentsTab matrix cell that already represents a scope —
   * the operator's intent is "promote THIS version to THIS scope" rather
   * than "pick a scope". Defaults to "global" when omitted.
   */
  initialScopeKind?: ScopeKind;
  initialScopeValue?: string;
}

type ScopeKind = "global" | "tenant" | "workflow" | "edge_fleet" | "edge_user";

const SCOPE_OPTIONS: { value: ScopeKind; label: string; placeholder: string }[] = [
  { value: "global", label: "Global", placeholder: "" },
  { value: "tenant", label: "Tenant", placeholder: "acme" },
  { value: "workflow", label: "Workflow", placeholder: "demo-pipeline" },
  { value: "edge_fleet", label: "Edge fleet", placeholder: "fleet-a" },
  { value: "edge_user", label: "Edge user", placeholder: "user-id" },
];

const EDGE_MODE_OPTIONS: { value: EdgeMode; label: string }[] = [
  { value: "observe", label: "Observe (log-only)" },
  { value: "enforce", label: "Enforce" },
  { value: "enterprise-strict", label: "Enterprise strict" },
];

function scopeLabelFor(kind: ScopeKind, value: string): string {
  return kind === "global" || !value ? kind : `${kind}:${value}`;
}

function isEdgeScope(kind: ScopeKind): boolean {
  return kind === "edge_fleet" || kind === "edge_user";
}

/**
 * Dashboard 7 — Deploy modal + scope picker (epic-d9a6c0a1 task-758788ea).
 * Authors a deploy intent (scope + version + optional edge-mode override
 * for edge scopes) and gates the mutation behind ConfirmDialog. Reuses
 * Drawer/Select/Input/Button/ConfirmDialog primitives — NO new shared
 * primitive introduced.
 */
export default function DeployBundleModal({
  bundleId,
  version,
  open,
  onClose,
  onSuccess,
  initialScopeKind,
  initialScopeValue,
}: DeployBundleModalProps) {
  const bundleQ = useBundle(bundleId);
  const deploy = useDeployBundle();

  const [scopeKind, setScopeKind] = useState<ScopeKind>(initialScopeKind ?? "global");
  const [scopeValue, setScopeValue] = useState<string>(initialScopeValue ?? "");
  const [edgeMode, setEdgeMode] = useState<EdgeMode | "">("");
  const [confirmOpen, setConfirmOpen] = useState(false);

  // Reset form whenever the modal re-opens for a different version or
  // initial scope. Pre-fills edgeMode from the bundle's current metadata
  // so an operator changing only the scope doesn't accidentally drop the
  // edge_mode. `initialScopeKind`/`initialScopeValue` come from the
  // BundleDeploymentsTab matrix-cell click path so the modal opens with
  // the cell's scope pre-selected.
  useEffect(() => {
    if (!open) return;
    setScopeKind(initialScopeKind ?? "global");
    setScopeValue(initialScopeValue ?? "");
    setEdgeMode(bundleQ.data?.metadata?.edge_mode ?? "");
    setConfirmOpen(false);
  }, [open, version, bundleQ.data?.metadata?.edge_mode, initialScopeKind, initialScopeValue]);

  const scopeOption = useMemo(
    () => SCOPE_OPTIONS.find((s) => s.value === scopeKind) ?? SCOPE_OPTIONS[0],
    [scopeKind],
  );

  const valueRequired = scopeKind !== "global";
  const valueValid = !valueRequired || scopeValue.trim().length > 0;
  const showEdgeMode = isEdgeScope(scopeKind);
  const edgeModeChanged =
    showEdgeMode &&
    edgeMode !== "" &&
    edgeMode !== (bundleQ.data?.metadata?.edge_mode ?? "");

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!valueValid) return;
    setConfirmOpen(true);
  }

  function handleConfirm() {
    deploy.mutate(
      {
        bundleId,
        version,
        scope: {
          kind: scopeKind,
          value: scopeKind === "global" ? undefined : scopeValue.trim(),
        },
        ...(showEdgeMode && edgeMode ? { edge_mode: edgeMode as EdgeMode } : {}),
      },
      {
        onSuccess: () => {
          setConfirmOpen(false);
          onSuccess?.();
          onClose();
        },
        onError: () => {
          // Toast surfaces the error; keep modal open so operator can retry.
          setConfirmOpen(false);
        },
      },
    );
  }

  const scopeLabel = scopeLabelFor(scopeKind, scopeValue);
  const bundleName = bundleQ.data?.name ?? bundleId;

  return (
    <>
      <Drawer open={open} onClose={onClose} size="md" label={`Deploy ${bundleName} ${version}`}>
        <form onSubmit={handleSubmit} className="flex h-full flex-col">
          <header className="border-b border-border px-5 py-4">
            <h2 className="text-base font-semibold text-foreground">
              Deploy {bundleName} <span className="text-muted-foreground">·</span> {version}
            </h2>
            <p className="mt-1 text-sm text-muted-foreground">
              Activate this bundle version for a scope. Affects production policy evaluation immediately.
            </p>
          </header>

          <div className="space-y-4 px-5 py-4">
            <label className="block text-sm">
              <span className="mb-1 block text-muted-foreground">Scope</span>
              <Select
                aria-label="Scope kind"
                value={scopeKind}
                onChange={(e) => setScopeKind(e.target.value as ScopeKind)}
                options={SCOPE_OPTIONS.map((s) => ({ value: s.value, label: s.label }))}
              />
            </label>

            <label className="block text-sm">
              <span className="mb-1 block text-muted-foreground">
                {scopeKind === "global" ? "Scope value (not applicable)" : "Scope value"}
              </span>
              <Input
                aria-label="Scope value"
                placeholder={scopeOption.placeholder}
                value={scopeValue}
                onChange={(e) => setScopeValue(e.target.value)}
                disabled={scopeKind === "global"}
              />
              {valueRequired && !valueValid && (
                <p className="mt-1 text-xs text-danger">Scope value is required for {scopeKind}.</p>
              )}
            </label>

            {showEdgeMode && (
              <label className="block text-sm">
                <span className="mb-1 block text-muted-foreground">Edge mode</span>
                <Select
                  aria-label="Edge mode"
                  value={edgeMode}
                  onChange={(e) => setEdgeMode(e.target.value as EdgeMode | "")}
                  options={EDGE_MODE_OPTIONS}
                  placeholder="Select edge mode"
                />
                {edgeModeChanged && (
                  <p className="mt-1 text-xs text-warning">
                    This will also update the bundle&rsquo;s edge mode to{" "}
                    <code className="font-medium">{edgeMode}</code>.
                  </p>
                )}
              </label>
            )}
          </div>

          <footer className="mt-auto flex items-center justify-end gap-2 border-t border-border px-5 py-3">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button
              type="submit"
              variant="default"
              disabled={!valueValid || deploy.isPending}
              aria-label={`Deploy ${version} to ${scopeLabel}`}
            >
              {deploy.isPending ? "Deploying…" : "Deploy"}
            </Button>
          </footer>
        </form>
      </Drawer>

      <ConfirmDialog
        open={confirmOpen}
        onCancel={() => setConfirmOpen(false)}
        onConfirm={handleConfirm}
        title={`Deploy ${bundleName} ${version} to ${scopeLabel}?`}
        description={
          <>
            This activates {bundleName} {version} for {scopeLabel}, replacing whatever is currently active there.
            Audit-logged.
            {edgeModeChanged && (
              <>
                {" "}
                <strong>Bundle edge mode</strong> will also change to <code>{edgeMode}</code>.
              </>
            )}
          </>
        }
        confirmLabel="Deploy"
        variant="destructive"
        isPending={deploy.isPending}
      />
    </>
  );
}
