import { useMemo, useState } from "react";
import { X, Save, Plus } from "lucide-react";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { cn } from "../../lib/utils";
import { useEventStore } from "../../state/events";
import { useConfigStore } from "../../state/config";
import type { Approval, UrgencyLevel } from "../../api/types";

// ---------------------------------------------------------------------------
// Filter state
// ---------------------------------------------------------------------------

export interface FilterState {
  urgency: "all" | "fresh" | "aging" | "critical";
  workflow: string;
  rule: string;
  risk: "all" | "low" | "medium" | "high";
  sortBy: "waitTime" | "risk" | "workflow" | "sla";
  assignment: "all" | "mine" | "unassigned";
}

export const DEFAULT_FILTERS: FilterState = {
  urgency: "all",
  workflow: "",
  rule: "",
  risk: "all",
  sortBy: "waitTime",
  assignment: "all",
};

export function isDefaultFilters(f: FilterState): boolean {
  return (
    f.urgency === "all" &&
    f.workflow === "" &&
    f.rule === "" &&
    f.risk === "all" &&
    f.sortBy === "waitTime" &&
    f.assignment === "all"
  );
}

// ---------------------------------------------------------------------------
// Saved presets
// ---------------------------------------------------------------------------

const PRESETS_KEY = "cordum:approval-filter-presets";

interface Preset {
  name: string;
  filters: FilterState;
  builtin?: boolean;
}

const BUILTIN_PRESETS: Preset[] = [
  { name: "Critical", filters: { ...DEFAULT_FILTERS, urgency: "critical" }, builtin: true },
  { name: "High Risk", filters: { ...DEFAULT_FILTERS, risk: "high" }, builtin: true },
];

function loadCustomPresets(): Preset[] {
  try {
    const raw = localStorage.getItem(PRESETS_KEY);
    if (!raw) return [];
    return JSON.parse(raw) as Preset[];
  } catch {
    return [];
  }
}

function saveCustomPresets(presets: Preset[]) {
  localStorage.setItem(PRESETS_KEY, JSON.stringify(presets));
}

// ---------------------------------------------------------------------------
// Risk helpers
// ---------------------------------------------------------------------------

const HIGH_RISK_TAGS = new Set(["financial", "destructive", "compliance", "production"]);

function riskLevel(approval: Approval): "high" | "medium" | "low" {
  const score = (approval.riskTags ?? []).filter((t) => HIGH_RISK_TAGS.has(t)).length;
  if (score >= 2) return "high";
  if (score >= 1) return "medium";
  return "low";
}

function urgencyMatch(approval: Approval, filter: FilterState["urgency"]): boolean {
  if (filter === "all") return true;
  const level = approval.urgencyLevel ?? "fresh";
  if (filter === "critical") return level === "critical" || level === "breach";
  return level === filter;
}

// ---------------------------------------------------------------------------
// Public: apply filters + sort
// ---------------------------------------------------------------------------

export function applyFilters(approvals: Approval[], filters: FilterState): Approval[] {
  const assignments = useEventStore.getState().approvalAssignments;
  const currentUser = useConfigStore.getState().principalId || "operator";

  let result = approvals.filter((a) => {
    if (!urgencyMatch(a, filters.urgency)) return false;
    if (filters.workflow && (a.workflowContext?.workflowId ?? "") !== filters.workflow) return false;
    if (filters.rule && (a.policyRule ?? "") !== filters.rule) return false;
    if (filters.risk !== "all" && riskLevel(a) !== filters.risk) return false;
    if (filters.assignment === "mine" && assignments.get(a.id) !== currentUser) return false;
    if (filters.assignment === "unassigned" && assignments.has(a.id)) return false;
    return true;
  });

  const slaMs = useConfigStore.getState().approvalSlaMs;
  result = [...result].sort((a, b) => {
    switch (filters.sortBy) {
      case "risk":
        return riskLevel(b).localeCompare(riskLevel(a)) || (b.waitMs ?? 0) - (a.waitMs ?? 0);
      case "workflow":
        return (a.workflowContext?.workflowId ?? "").localeCompare(b.workflowContext?.workflowId ?? "") || (b.waitMs ?? 0) - (a.waitMs ?? 0);
      case "sla": {
        const aBreached = (a.waitMs ?? 0) > slaMs ? 1 : 0;
        const bBreached = (b.waitMs ?? 0) > slaMs ? 1 : 0;
        if (bBreached !== aBreached) return bBreached - aBreached; // breached first
        // Within same breach status, sort by remaining SLA time (ascending = closest to breach first)
        return (slaMs - (a.waitMs ?? 0)) - (slaMs - (b.waitMs ?? 0));
      }
      case "waitTime":
      default:
        return (b.waitMs ?? 0) - (a.waitMs ?? 0);
    }
  });

  return result;
}

// ---------------------------------------------------------------------------
// Count helpers for chips
// ---------------------------------------------------------------------------

function urgencyCounts(approvals: Approval[]) {
  const counts = { all: approvals.length, fresh: 0, aging: 0, critical: 0 };
  for (const a of approvals) {
    const level = a.urgencyLevel ?? "fresh";
    if (level === "critical" || level === "breach") counts.critical++;
    else if (level === "aging") counts.aging++;
    else counts.fresh++;
  }
  return counts;
}

function riskCounts(approvals: Approval[]) {
  const counts = { all: approvals.length, low: 0, medium: 0, high: 0 };
  for (const a of approvals) {
    counts[riskLevel(a)]++;
  }
  return counts;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface ApprovalQueueFiltersProps {
  approvals: Approval[];
  filters: FilterState;
  onFiltersChange: (filters: FilterState) => void;
}

export function ApprovalQueueFilters({
  approvals,
  filters,
  onFiltersChange,
}: ApprovalQueueFiltersProps) {
  const [savingPreset, setSavingPreset] = useState(false);
  const [presetName, setPresetName] = useState("");
  const [customPresets, setCustomPresets] = useState(loadCustomPresets);

  const allPresets = useMemo(() => [...BUILTIN_PRESETS, ...customPresets], [customPresets]);
  const uCounts = useMemo(() => urgencyCounts(approvals), [approvals]);
  const rCounts = useMemo(() => riskCounts(approvals), [approvals]);

  const uniqueWorkflows = useMemo(() => {
    const set = new Set<string>();
    for (const a of approvals) {
      if (a.workflowContext?.workflowId) set.add(a.workflowContext.workflowId);
    }
    return [...set].sort();
  }, [approvals]);

  const uniqueRules = useMemo(() => {
    const set = new Set<string>();
    for (const a of approvals) {
      if (a.policyRule) set.add(a.policyRule);
    }
    return [...set].sort();
  }, [approvals]);

  const update = (patch: Partial<FilterState>) => {
    onFiltersChange({ ...filters, ...patch });
  };

  const handleSavePreset = () => {
    if (!presetName.trim()) return;
    const newPreset: Preset = { name: presetName.trim(), filters: { ...filters } };
    const updated = [...customPresets, newPreset];
    setCustomPresets(updated);
    saveCustomPresets(updated);
    setPresetName("");
    setSavingPreset(false);
  };

  const handleDeletePreset = (name: string) => {
    const updated = customPresets.filter((p) => p.name !== name);
    setCustomPresets(updated);
    saveCustomPresets(updated);
  };

  const nonDefault = !isDefaultFilters(filters);
  const activeFilterSummary = useMemo(() => {
    const parts: string[] = [];
    if (filters.urgency !== "all") parts.push(`urgency ${filters.urgency}`);
    if (filters.risk !== "all") parts.push(`risk ${filters.risk}`);
    if (filters.assignment !== "all") {
      parts.push(filters.assignment === "mine" ? "assigned to me" : "unassigned");
    }
    if (filters.workflow) parts.push(`workflow ${filters.workflow}`);
    if (filters.rule) parts.push(`rule ${filters.rule}`);
    if (filters.sortBy !== "waitTime") parts.push(`sorted by ${filters.sortBy}`);
    return parts.length > 0 ? `Active filters: ${parts.join(", ")}.` : "No active filters.";
  }, [filters]);

  return (
    <div className="space-y-2" role="region" aria-label="Approval queue filters">
      <p className="text-[11px] text-muted-foreground" role="status" aria-live="polite">
        {activeFilterSummary}
      </p>
      {/* Preset chips */}
      <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="Saved filter presets">
        {allPresets.map((preset) => {
          const selected = JSON.stringify(filters) === JSON.stringify(preset.filters);
          return (
            <div key={preset.name} className="group relative">
              <button
                type="button"
                aria-pressed={selected}
                className={cn(
                  "rounded-md border px-2.5 py-1 text-[11px] font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35 focus-visible:ring-offset-1 focus-visible:ring-offset-surface-0",
                  !preset.builtin && "pr-6",
                  selected
                    ? "border-status-info-border bg-status-info-bg text-accent"
                    : "border-border bg-surface-1/45 text-muted-foreground hover:border-border hover:text-foreground",
                )}
                onClick={() => onFiltersChange(preset.filters)}
              >
                {preset.name}
              </button>
              {!preset.builtin && (
                <button
                  type="button"
                  className="absolute right-1 top-1/2 hidden -translate-y-1/2 rounded-sm p-0.5 text-muted hover:text-danger focus-visible:inline-flex group-hover:inline-flex"
                  aria-label={`Delete ${preset.name} preset`}
                  onClick={() => handleDeletePreset(preset.name)}
                >
                  <X className="h-3 w-3" />
                </button>
              )}
            </div>
          );
        })}
        {savingPreset ? (
          <div className="flex items-center gap-1">
            <Input
              className="h-7 w-28 text-xs"
              placeholder="Preset name"
              aria-label="Preset name"
              value={presetName}
              onChange={(e) => setPresetName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && handleSavePreset()}
              autoFocus
            />
            <Button size="sm" onClick={handleSavePreset} disabled={!presetName.trim()}>
              <Save className="h-3 w-3" />
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setSavingPreset(false)}>
              <X className="h-3 w-3" />
            </Button>
          </div>
        ) : (
          nonDefault && (
            <button
              type="button"
              className="flex items-center gap-1 rounded-md border border-dashed border-border px-2.5 py-1 text-[11px] text-muted-foreground transition-colors hover:border-border hover:text-foreground"
              onClick={() => setSavingPreset(true)}
            >
              <Plus className="h-3 w-3" /> Save filter
            </button>
          )
        )}
      </div>

      {/* Filter controls */}
      <div className="flex flex-wrap items-center gap-2">
        {/* Urgency chips */}
        <div className="flex gap-0.5 rounded-md border border-border p-0.5" role="group" aria-label="Filter by urgency">
          {(["all", "fresh", "aging", "critical"] as const).map((u) => (
            <button
              key={u}
              type="button"
              aria-pressed={filters.urgency === u}
              className={cn(
                "rounded-sm px-2 py-1 text-[11px] font-medium capitalize transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35 focus-visible:ring-offset-1 focus-visible:ring-offset-surface-0",
                filters.urgency === u
                  ? "bg-status-info-bg text-accent"
                  : "text-muted-foreground hover:text-foreground",
              )}
              onClick={() => update({ urgency: u })}
            >
              {u} ({uCounts[u]})
            </button>
          ))}
        </div>

        {/* Risk chips */}
        <div className="flex gap-0.5 rounded-md border border-border p-0.5" role="group" aria-label="Filter by risk">
          {(["all", "low", "medium", "high"] as const).map((r) => (
            <button
              key={r}
              type="button"
              aria-pressed={filters.risk === r}
              className={cn(
                "rounded-sm px-2 py-1 text-[11px] font-medium capitalize transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35 focus-visible:ring-offset-1 focus-visible:ring-offset-surface-0",
                filters.risk === r
                  ? "bg-status-info-bg text-accent"
                  : "text-muted-foreground hover:text-foreground",
              )}
              onClick={() => update({ risk: r })}
            >
              {r} ({rCounts[r]})
            </button>
          ))}
        </div>

        {/* Assignment chips */}
        <div className="flex gap-0.5 rounded-md border border-border p-0.5" role="group" aria-label="Filter by assignment">
          {(["all", "mine", "unassigned"] as const).map((a) => (
            <button
              key={a}
              type="button"
              aria-pressed={filters.assignment === a}
              className={cn(
                "rounded-sm px-2 py-1 text-[11px] font-medium capitalize transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35 focus-visible:ring-offset-1 focus-visible:ring-offset-surface-0",
                filters.assignment === a
                  ? "bg-status-info-bg text-accent"
                  : "text-muted-foreground hover:text-foreground",
              )}
              onClick={() => update({ assignment: a })}
            >
              {a === "mine" ? "Assigned to me" : a === "unassigned" ? "Unassigned" : "All"}
            </button>
          ))}
        </div>

        {/* Workflow dropdown */}
        {uniqueWorkflows.length > 0 && (
          <Select
            className="h-7 w-40 text-xs"
            aria-label="Filter by workflow"
            value={filters.workflow}
            onChange={(e) => update({ workflow: e.target.value })}
          >
            <option value="">All workflows</option>
            {uniqueWorkflows.map((w) => (
              <option key={w} value={w}>{w.slice(0, 20)}</option>
            ))}
          </Select>
        )}

        {/* Rule dropdown */}
        {uniqueRules.length > 0 && (
          <Select
            className="h-7 w-40 text-xs"
            aria-label="Filter by policy rule"
            value={filters.rule}
            onChange={(e) => update({ rule: e.target.value })}
          >
            <option value="">All rules</option>
            {uniqueRules.map((r) => (
              <option key={r} value={r}>{r}</option>
            ))}
          </Select>
        )}

        {/* Sort */}
        <Select
          className="h-7 w-32 text-xs"
          aria-label="Sort approvals"
          value={filters.sortBy}
          onChange={(e) => update({ sortBy: e.target.value as FilterState["sortBy"] })}
        >
          <option value="waitTime">Wait time</option>
          <option value="risk">Risk level</option>
          <option value="workflow">Workflow</option>
          <option value="sla">SLA status</option>
        </Select>

        {/* Clear all */}
        {nonDefault && (
          <Button
            variant="ghost"
            size="sm"
            className="text-xs"
            onClick={() => onFiltersChange(DEFAULT_FILTERS)}
          >
            Clear filters
          </Button>
        )}
      </div>
    </div>
  );
}
