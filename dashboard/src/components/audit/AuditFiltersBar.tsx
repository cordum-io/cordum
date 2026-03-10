import { useCallback, useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { X } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { cn } from "../../lib/utils";
import type { AuditFilters } from "../../hooks/useAudit";

// ---------------------------------------------------------------------------
// Category-grouped event types
// ---------------------------------------------------------------------------

const EVENT_CATEGORIES = [
  {
    id: "safety_decision",
    label: "Safety Decisions",
    subTypes: ["evaluate", "allow", "deny", "approve", "throttle"],
  },
  {
    id: "human_action",
    label: "Human Actions",
    subTypes: [
      "edit", "create", "delete", "approve", "reject", "cancel",
      "remediate", "change_password", "set", "snapshot", "submit",
      "publish", "rollback",
    ],
  },
  {
    id: "system_event",
    label: "System Events",
    subTypes: [
      "dispatch", "complete", "fail", "timeout", "retry",
      "escalate", "connect", "disconnect", "reload",
    ],
  },
  {
    id: "access_event",
    label: "Access Events",
    subTypes: ["login", "logout", "register", "key_create", "key_revoke"],
  },
] as const;

const ALL_SUB_TYPES = EVENT_CATEGORIES.flatMap((c) =>
  c.subTypes.map((s) => ({ category: c.id, label: s, value: s })),
);

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const RESOURCE_TYPES = [
  { value: "", label: "All resources" },
  { value: "job", label: "Job" },
  { value: "workflow", label: "Workflow" },
  { value: "policy", label: "Policy" },
  { value: "approval", label: "Approval" },
  { value: "agent", label: "Agent" },
  { value: "pack", label: "Pack" },
  { value: "config", label: "Config" },
  { value: "user", label: "User" },
  { value: "schema", label: "Schema" },
] as const;

const TIME_RANGES = [
  { value: "1h", label: "1h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
  { value: "90d", label: "90d" },
] as const;

const SEVERITY_OPTIONS = ["high", "medium", "low"] as const;

const OUTCOME_OPTIONS = [
  { value: "allow", label: "Allow" },
  { value: "deny", label: "Deny" },
  { value: "approve", label: "Approve" },
  { value: "reject", label: "Reject" },
  { value: "throttle", label: "Throttle" },
  { value: "succeeded", label: "Succeeded" },
  { value: "failed", label: "Failed" },
] as const;

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface AuditFilterValues {
  eventType: string[];
  actor: string;
  resourceType: string;
  resourceId: string;
  severity: string[];
  outcome: string[];
  timeRange: string;
  from: string;
  to: string;
  search: string;
}

interface AuditFiltersBarProps {
  onChange: (filters: AuditFilterValues) => void;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function parseList(param: string | null): string[] {
  return param?.split(",").filter(Boolean) ?? [];
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function AuditFiltersBar({ onChange }: AuditFiltersBarProps) {
  const [searchParams, setSearchParams] = useSearchParams();

  // Read from URL
  const eventType = parseList(searchParams.get("eventType"));
  const actor = searchParams.get("actor") ?? "";
  const resourceType = searchParams.get("resourceType") ?? "";
  const resourceId = searchParams.get("resourceId") ?? "";
  const severity = parseList(searchParams.get("severity"));
  const outcome = parseList(searchParams.get("outcome"));
  const timeRange = searchParams.get("timeRange") ?? "";
  const from = searchParams.get("from") ?? "";
  const to = searchParams.get("to") ?? "";
  const search = searchParams.get("q") ?? "";

  // Local state for debounced inputs
  const [localActor, setLocalActor] = useState(actor);
  const [localSearch, setLocalSearch] = useState(search);
  const [localResourceId, setLocalResourceId] = useState(resourceId);
  const [showSubTypes, setShowSubTypes] = useState(false);

  // Stable onChange ref
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  // Debounce timers
  const actorTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const searchTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const resourceIdTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Clear pending debounce timers on unmount
  useEffect(() => {
    return () => {
      clearTimeout(actorTimerRef.current);
      clearTimeout(searchTimerRef.current);
      clearTimeout(resourceIdTimerRef.current);
    };
  }, []);

  // Sync local state when URL changes externally
  useEffect(() => { setLocalActor(actor); }, [actor]);
  useEffect(() => { setLocalSearch(search); }, [search]);
  useEffect(() => { setLocalResourceId(resourceId); }, [resourceId]);

  // Emit filter changes
  useEffect(() => {
    onChangeRef.current({
      eventType, actor, resourceType, resourceId,
      severity, outcome, timeRange, from, to, search,
    });
  }, [
    eventType.join(","), actor, resourceType, resourceId,
    severity.join(","), outcome.join(","), timeRange, from, to, search,
  ]);

  // ---------------------------------------------------------------------------
  // Param helpers
  // ---------------------------------------------------------------------------

  const setParam = useCallback(
    (key: string, value: string) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        if (value) next.set(key, value);
        else next.delete(key);
        next.set("page", "1");
        return next;
      });
    },
    [setSearchParams],
  );

  const setListParam = useCallback(
    (key: string, values: string[]) => {
      setParam(key, values.join(","));
    },
    [setParam],
  );

  // ---------------------------------------------------------------------------
  // Event type toggles
  // ---------------------------------------------------------------------------

  const toggleEventType = useCallback(
    (et: string) => {
      const current = parseList(searchParams.get("eventType"));
      const next = current.includes(et)
        ? current.filter((v) => v !== et)
        : [...current, et];
      setListParam("eventType", next);
    },
    [searchParams, setListParam],
  );

  const toggleCategory = useCallback(
    (categoryId: string) => {
      const cat = EVENT_CATEGORIES.find((c) => c.id === categoryId);
      if (!cat) return;
      const current = parseList(searchParams.get("eventType"));
      const catTypes = cat.subTypes as readonly string[];
      const allSelected = catTypes.every((t) => current.includes(t));
      const next = allSelected
        ? current.filter((v) => !catTypes.includes(v))
        : [...new Set([...current, ...catTypes])];
      setListParam("eventType", next);
    },
    [searchParams, setListParam],
  );

  // ---------------------------------------------------------------------------
  // Severity toggle
  // ---------------------------------------------------------------------------

  const toggleSeverity = useCallback(
    (sev: string) => {
      const current = parseList(searchParams.get("severity"));
      const next = current.includes(sev)
        ? current.filter((v) => v !== sev)
        : [...current, sev];
      setListParam("severity", next);
    },
    [searchParams, setListParam],
  );

  // ---------------------------------------------------------------------------
  // Outcome toggle
  // ---------------------------------------------------------------------------

  const toggleOutcome = useCallback(
    (oc: string) => {
      const current = parseList(searchParams.get("outcome"));
      const next = current.includes(oc)
        ? current.filter((v) => v !== oc)
        : [...current, oc];
      setListParam("outcome", next);
    },
    [searchParams, setListParam],
  );

  // ---------------------------------------------------------------------------
  // Debounced text inputs
  // ---------------------------------------------------------------------------

  const handleActorChange = useCallback(
    (value: string) => {
      setLocalActor(value);
      clearTimeout(actorTimerRef.current);
      actorTimerRef.current = setTimeout(() => setParam("actor", value), 400);
    },
    [setParam],
  );

  const handleSearchChange = useCallback(
    (value: string) => {
      setLocalSearch(value);
      clearTimeout(searchTimerRef.current);
      searchTimerRef.current = setTimeout(() => setParam("q", value), 400);
    },
    [setParam],
  );

  const handleResourceIdChange = useCallback(
    (value: string) => {
      setLocalResourceId(value);
      clearTimeout(resourceIdTimerRef.current);
      resourceIdTimerRef.current = setTimeout(() => setParam("resourceId", value), 400);
    },
    [setParam],
  );

  // ---------------------------------------------------------------------------
  // Time range
  // ---------------------------------------------------------------------------

  const isCustomRange = timeRange === "custom";

  const selectPresetRange = useCallback(
    (value: string) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        if (timeRange === value) {
          next.delete("timeRange");
        } else {
          next.set("timeRange", value);
        }
        next.delete("from");
        next.delete("to");
        next.set("page", "1");
        return next;
      });
    },
    [timeRange, setSearchParams],
  );

  const enableCustomRange = useCallback(() => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      next.set("timeRange", "custom");
      next.set("page", "1");
      return next;
    });
  }, [setSearchParams]);

  // ---------------------------------------------------------------------------
  // Clear all
  // ---------------------------------------------------------------------------

  const clearAll = useCallback(() => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      for (const key of [
        "eventType", "actor", "resourceType", "resourceId",
        "severity", "outcome", "timeRange", "from", "to", "q",
      ]) {
        next.delete(key);
      }
      next.set("page", "1");
      return next;
    });
    setLocalActor("");
    setLocalSearch("");
    setLocalResourceId("");
  }, [setSearchParams]);

  // ---------------------------------------------------------------------------
  // Active filter chips
  // ---------------------------------------------------------------------------

  const chips: { label: string; onRemove: () => void }[] = [];

  for (const et of eventType) {
    chips.push({
      label: `Type: ${et}`,
      onRemove: () => toggleEventType(et),
    });
  }
  if (actor) {
    chips.push({ label: `Actor: ${actor}`, onRemove: () => setParam("actor", "") });
  }
  if (resourceType) {
    chips.push({ label: `Resource: ${resourceType}`, onRemove: () => setParam("resourceType", "") });
  }
  if (resourceId) {
    chips.push({ label: `ID: ${resourceId}`, onRemove: () => setParam("resourceId", "") });
  }
  for (const s of severity) {
    chips.push({
      label: `Severity: ${s}`,
      onRemove: () => toggleSeverity(s),
    });
  }
  for (const o of outcome) {
    chips.push({
      label: `Outcome: ${o}`,
      onRemove: () => toggleOutcome(o),
    });
  }
  if (timeRange) {
    chips.push({
      label: `Range: ${isCustomRange ? "custom" : timeRange}`,
      onRemove: () => {
        setSearchParams((prev) => {
          const next = new URLSearchParams(prev);
          next.delete("timeRange");
          next.delete("from");
          next.delete("to");
          return next;
        });
      },
    });
  }
  if (search) {
    chips.push({ label: `Search: ${search}`, onRemove: () => { setParam("q", ""); setLocalSearch(""); } });
  }

  // Timezone for display
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;

  // ---------------------------------------------------------------------------
  // Render
  // ---------------------------------------------------------------------------

  return (
    <div className="surface-card space-y-3 rounded-2xl p-4">
      {/* Row 1: Full-text search */}
      <div className="relative">
        <Input
          placeholder="Search audit trail\u2026"
          value={localSearch}
          onChange={(e) => handleSearchChange(e.target.value)}
          className="pl-9"
        />
        <svg
          className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <circle cx="11" cy="11" r="8" strokeWidth="2" />
          <path d="m21 21-4.3-4.3" strokeWidth="2" strokeLinecap="round" />
        </svg>
      </div>

      {/* Row 2: Event type categories */}
      <div className="space-y-1.5">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="mr-1 text-xs font-semibold text-muted-foreground">Event:</span>
          {EVENT_CATEGORIES.map((cat) => {
            const catTypes = cat.subTypes as readonly string[];
            const selectedCount = catTypes.filter((t) => eventType.includes(t)).length;
            const allSelected = selectedCount === catTypes.length;
            return (
              <button
                key={cat.id}
                type="button"
                onClick={() => toggleCategory(cat.id)}
                className={cn(
                  "rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors",
                  allSelected
                    ? "border-accent bg-accent/10 text-accent"
                    : selectedCount > 0
                      ? "border-accent/50 bg-accent/5 text-accent"
                      : "border-border text-muted-foreground hover:border-accent/40 hover:text-ink",
                )}
              >
                {cat.label}
                {selectedCount > 0 && !allSelected && (
                  <span className="ml-1 text-[10px]">({selectedCount})</span>
                )}
              </button>
            );
          })}
          <button
            type="button"
            onClick={() => setShowSubTypes(!showSubTypes)}
            className="text-xs text-muted-foreground hover:text-accent transition-colors"
          >
            {showSubTypes ? "Hide sub-types" : "Sub-types\u2026"}
          </button>
        </div>
        {showSubTypes && (
          <div className="flex flex-wrap items-center gap-1 pl-14">
            {ALL_SUB_TYPES.map((st) => {
              const active = eventType.includes(st.value);
              return (
                <button
                  key={`${st.category}-${st.value}`}
                  type="button"
                  onClick={() => toggleEventType(st.value)}
                  className={cn(
                    "rounded-full border px-2 py-0.5 text-[11px] font-medium transition-colors",
                    active
                      ? "border-accent bg-accent/10 text-accent"
                      : "border-border text-muted-foreground hover:border-accent/40 hover:text-ink",
                  )}
                >
                  {st.label}
                </button>
              );
            })}
          </div>
        )}
      </div>

      {/* Row 3: Severity chips */}
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="mr-1 text-xs font-semibold text-muted-foreground">Severity:</span>
        {SEVERITY_OPTIONS.map((sev) => {
          const active = severity.includes(sev);
          return (
            <button
              key={sev}
              type="button"
              onClick={() => toggleSeverity(sev)}
              className={cn(
                "rounded-full border px-2.5 py-0.5 text-xs font-medium capitalize transition-colors",
                active
                  ? sev === "high"
                    ? "border-destructive/40 bg-destructive/5 text-destructive"
                    : sev === "medium"
                      ? "border-[var(--color-warning)]/40 bg-[var(--color-warning)]/5 text-[var(--color-warning)]"
                      : "border-muted bg-muted/50 text-muted-foreground"
                  : "border-border text-muted-foreground hover:border-accent/40 hover:text-ink",
              )}
            >
              {sev}
            </button>
          );
        })}
      </div>

      {/* Row 4: Actor, Resource Type, Resource ID, Outcome */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="w-44">
          <Input
            placeholder="Actor"
            value={localActor}
            onChange={(e) => handleActorChange(e.target.value)}
          />
        </div>

        <div className="w-40">
          <Select
            value={resourceType}
            onChange={(e) => setParam("resourceType", e.target.value)}
          >
            {RESOURCE_TYPES.map((rt) => (
              <option key={rt.value} value={rt.value}>
                {rt.label}
              </option>
            ))}
          </Select>
        </div>

        <div className="w-40">
          <Input
            placeholder="Resource ID\u2026"
            value={localResourceId}
            onChange={(e) => handleResourceIdChange(e.target.value)}
          />
        </div>

        <div className="w-40">
          <Select
            value={outcome.length === 1 ? outcome[0] : ""}
            onChange={(e) => {
              const val = e.target.value;
              setListParam("outcome", val ? [val] : []);
            }}
          >
            <option value="">All outcomes</option>
            {OUTCOME_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </Select>
        </div>
      </div>

      {/* Row 5: Time range presets + custom */}
      <div className="space-y-2">
        <div className="flex items-center gap-1 rounded-full border border-border p-0.5 w-fit">
          {TIME_RANGES.map((tr) => (
            <button
              key={tr.value}
              type="button"
              onClick={() => selectPresetRange(tr.value)}
              className={cn(
                "rounded-full px-3 py-1 text-xs font-medium transition-colors",
                timeRange === tr.value
                  ? "bg-accent text-primary-foreground"
                  : "text-muted-foreground hover:text-ink",
              )}
            >
              {tr.label}
            </button>
          ))}
          <button
            type="button"
            onClick={enableCustomRange}
            className={cn(
              "rounded-full px-3 py-1 text-xs font-medium transition-colors",
              isCustomRange
                ? "bg-accent text-primary-foreground"
                : "text-muted-foreground hover:text-ink",
            )}
          >
            Custom
          </button>
        </div>

        {isCustomRange && (
          <div className="flex flex-wrap items-center gap-2 pl-1">
            <label className="text-xs text-muted-foreground">From:</label>
            <input
              type="datetime-local"
              value={from}
              onChange={(e) => setParam("from", e.target.value)}
              className="rounded-lg border border-border bg-card/70 px-2 py-1 text-xs text-ink"
            />
            <label className="text-xs text-muted-foreground">To:</label>
            <input
              type="datetime-local"
              value={to}
              onChange={(e) => setParam("to", e.target.value)}
              className="rounded-lg border border-border bg-card/70 px-2 py-1 text-xs text-ink"
            />
            <span className="text-[10px] text-muted-foreground">({tz})</span>
          </div>
        )}
      </div>

      {/* Active filter chips + clear */}
      {chips.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 pt-1 border-t border-border">
          {chips.map((chip) => (
            <Badge
              key={chip.label}
              variant="info"
              className="gap-1 pr-1"
            >
              {chip.label}
              <button
                type="button"
                onClick={chip.onRemove}
                className="ml-0.5 rounded-full p-0.5 hover:bg-black/10"
              >
                <X className="h-3 w-3" />
              </button>
            </Badge>
          ))}
          <Button variant="ghost" size="sm" onClick={clearAll}>
            Clear all
          </Button>
        </div>
      )}
    </div>
  );
}
