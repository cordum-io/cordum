import { useState, useMemo, useCallback } from "react";
import { ChevronRight, ChevronDown, Layers, Search } from "lucide-react";
import { useEffectiveConfig, type EffectiveConfigParams } from "../../hooks/useSettings";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { Badge } from "../ui/Badge";
import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Scope selector
// ---------------------------------------------------------------------------

function ScopeSelector({
  params,
  onApply,
}: {
  params: EffectiveConfigParams;
  onApply: (params: EffectiveConfigParams) => void;
}) {
  // Seed scope inputs from the parent's current scope exactly once. This is
  // a draft-form: the user edits locally and then commits via "Apply Scope"
  // which updates the parent params. We intentionally ignore subsequent
  // prop updates so a background query refresh can't overwrite an in-flight
  // edit. If a caller ever needs to force-reset the form, that requires an
  // explicit key-remount at the call site.
  const [orgId, setOrgId] = useState(params.orgId ?? "");
  const [teamId, setTeamId] = useState(params.teamId ?? "");
  const [workflowId, setWorkflowId] = useState(params.workflowId ?? "");
  const [stepId, setStepId] = useState(params.stepId ?? "");

  const handleApply = () => {
    onApply({
      orgId: orgId || undefined,
      teamId: teamId || undefined,
      workflowId: workflowId || undefined,
      stepId: stepId || undefined,
    });
  };

  return (
    <div className="space-y-3">
      <div className="flex items-start gap-3 mb-2">
        <Layers className="mt-0.5 h-5 w-5 shrink-0 text-accent" />
        <div>
          <h3 className="font-display text-base font-semibold text-ink">Scope Selector</h3>
          <p className="text-xs text-muted-foreground">
            Narrow the scope to see which config values apply at a specific level.
            Leave empty for global defaults.
          </p>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div className="space-y-1">
          <label className="text-xs font-semibold text-muted-foreground">Org ID</label>
          <Input
            placeholder="(global)"
            value={orgId}
            onChange={(e) => setOrgId(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <label className="text-xs font-semibold text-muted-foreground">Team ID</label>
          <Input
            placeholder="(org default)"
            value={teamId}
            onChange={(e) => setTeamId(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <label className="text-xs font-semibold text-muted-foreground">Workflow ID</label>
          <Input
            placeholder="(team default)"
            value={workflowId}
            onChange={(e) => setWorkflowId(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <label className="text-xs font-semibold text-muted-foreground">Step ID</label>
          <Input
            placeholder="(workflow default)"
            value={stepId}
            onChange={(e) => setStepId(e.target.value)}
            disabled={!workflowId}
          />
        </div>
      </div>

      <Button variant="secondary" size="sm" onClick={handleApply}>
        <Search className="h-3.5 w-3.5" />
        Apply Scope
      </Button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// JSON tree node
// ---------------------------------------------------------------------------

const SCOPE_COLORS: Record<string, string> = {
  global: "bg-muted text-muted-foreground",
  org: "bg-[var(--color-info)]/10 text-[var(--color-info)]",
  team: "bg-[var(--color-success)]/10 text-[var(--color-success)]",
  workflow: "bg-primary/10 text-primary",
  step: "bg-[var(--color-warning)]/10 text-[var(--color-warning)]",
};

function inferScope(params: EffectiveConfigParams): string {
  if (params.stepId) return "step";
  if (params.workflowId) return "workflow";
  if (params.teamId) return "team";
  if (params.orgId) return "org";
  return "global";
}

function ScopeBadge({ scope }: { scope: string }) {
  return (
    <span
      className={cn(
        "ml-2 inline-flex items-center rounded-full px-1.5 py-0.5 text-xs font-semibold",
        SCOPE_COLORS[scope] ?? SCOPE_COLORS.global,
      )}
      title={`Set at ${scope} level`}
    >
      {scope}
    </span>
  );
}

function formatValue(value: unknown): string {
  if (value === null) return "null";
  if (value === undefined) return "undefined";
  if (typeof value === "string") return `"${value}"`;
  if (typeof value === "boolean" || typeof value === "number") return String(value);
  return JSON.stringify(value);
}

function valueColor(value: unknown): string {
  if (typeof value === "string") return "text-[var(--color-success)]";
  if (typeof value === "number") return "text-[var(--color-info)]";
  if (typeof value === "boolean") return "text-primary";
  if (value === null) return "text-muted-foreground";
  return "text-ink";
}

function TreeNode({
  name,
  value,
  scope,
  depth = 0,
  defaultOpen = false,
}: {
  name: string;
  value: unknown;
  scope: string;
  depth?: number;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen || depth < 1);
  const isObject = value !== null && typeof value === "object" && !Array.isArray(value);
  const isArray = Array.isArray(value);
  const isExpandable = isObject || isArray;

  if (isExpandable) {
    const entries = isArray
      ? (value as unknown[]).map((v, i) => [String(i), v] as const)
      : Object.entries(value as Record<string, unknown>);

    return (
      <div className="font-mono text-sm">
        <button
          type="button"
          className="flex items-center gap-1 hover:bg-surface2 rounded px-1 -ml-1 w-full text-left"
          onClick={() => setOpen(!open)}
        >
          {open ? (
            <ChevronDown className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
          )}
          <span className="text-ink font-semibold">{name}</span>
          <span className="text-muted-foreground ml-1">
            {isArray ? `[${entries.length}]` : `{${entries.length}}`}
          </span>
        </button>
        {open && (
          <div className="ml-4 border-l border-border pl-3 space-y-0.5 mt-0.5">
            {entries.map(([key, val]) => (
              <TreeNode
                key={key}
                name={key}
                value={val}
                scope={scope}
                depth={depth + 1}
              />
            ))}
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="flex items-center gap-1 font-mono text-sm px-1 py-0.5">
      <span className="text-ink/60 w-3.5 shrink-0" />
      <span className="text-ink">{name}:</span>
      <span className={valueColor(value)}>{formatValue(value)}</span>
      <ScopeBadge scope={scope} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Panel
// ---------------------------------------------------------------------------

export default function EffectiveConfigPanel() {
  const [params, setParams] = useState<EffectiveConfigParams>({});
  const { data, isLoading, isError, error } = useEffectiveConfig(params);
  const scope = useMemo(() => inferScope(params), [params]);

  const handleApply = useCallback((newParams: EffectiveConfigParams) => {
    setParams(newParams);
  }, []);

  return (
    <div className="space-y-6">
      <Card>
        <ScopeSelector params={params} onApply={handleApply} />
      </Card>

      <Card>
        <div className="flex items-start gap-3 mb-4">
          <Layers className="mt-0.5 h-5 w-5 shrink-0 text-accent" />
          <div>
            <h3 className="font-display text-base font-semibold text-ink">
              Effective Configuration
            </h3>
            <p className="text-xs text-muted-foreground">
              Merged configuration that applies at the{" "}
              <Badge className="text-xs px-1.5 py-0">{scope}</Badge> level.
              Values cascade: global → org → team → workflow → step.
            </p>
          </div>
        </div>

        {isLoading && (
          <div className="space-y-2">
            {Array.from({ length: 6 }, (_, i) => (
              <div key={i} className="h-6 animate-pulse rounded bg-surface2" />
            ))}
          </div>
        )}

        {isError && (
          <p className="text-sm text-danger">
            Failed to load effective config: {error?.message ?? "Unknown error"}
          </p>
        )}

        {data && !isLoading && (
          <div className="space-y-0.5">
            {Object.keys(data).length === 0 ? (
              <p className="text-sm text-muted-foreground">No configuration values at this scope.</p>
            ) : (
              Object.entries(data).map(([key, val]) => (
                <TreeNode
                  key={key}
                  name={key}
                  value={val}
                  scope={scope}
                  defaultOpen
                />
              ))
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
