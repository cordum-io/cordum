import { useCallback, useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const EVENT_TYPES = [
  "create",
  "update",
  "delete",
  "approve",
  "reject",
  "login",
  "logout",
  "error",
  "policy_change",
  "config_change",
] as const;

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
] as const;

const TIME_RANGES = [
  { value: "1h", label: "1h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
] as const;

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface AuditFilterValues {
  eventType: string[];
  actor: string;
  resourceType: string;
  timeRange: string;
  search: string;
}

interface AuditFiltersBarProps {
  onChange: (filters: AuditFilterValues) => void;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function AuditFiltersBar({ onChange }: AuditFiltersBarProps) {
  const [searchParams, setSearchParams] = useSearchParams();

  // Read initial values from URL
  const eventType = searchParams.get("eventType")?.split(",").filter(Boolean) ?? [];
  const actor = searchParams.get("actor") ?? "";
  const resourceType = searchParams.get("resourceType") ?? "";
  const timeRange = searchParams.get("timeRange") ?? "";
  const search = searchParams.get("q") ?? "";

  // Local state for debounced inputs
  const [localActor, setLocalActor] = useState(actor);
  const [localSearch, setLocalSearch] = useState(search);

  // Stable onChange ref to avoid effect re-triggers
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  // Debounce timers
  const actorTimerRef = useRef<ReturnType<typeof setTimeout>>();
  const searchTimerRef = useRef<ReturnType<typeof setTimeout>>();

  // Sync local state when URL changes externally
  useEffect(() => { setLocalActor(actor); }, [actor]);
  useEffect(() => { setLocalSearch(search); }, [search]);

  // Emit filter changes to parent
  useEffect(() => {
    onChangeRef.current({ eventType, actor, resourceType, timeRange, search });
  }, [eventType.join(","), actor, resourceType, timeRange, search]);

  // ---------------------------------------------------------------------------
  // Param helpers
  // ---------------------------------------------------------------------------

  const setParam = useCallback(
    (key: string, value: string) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        if (value) {
          next.set(key, value);
        } else {
          next.delete(key);
        }
        next.set("page", "1");
        return next;
      });
    },
    [setSearchParams],
  );

  // ---------------------------------------------------------------------------
  // Event type multi-select
  // ---------------------------------------------------------------------------

  const toggleEventType = useCallback(
    (et: string) => {
      const current = searchParams.get("eventType")?.split(",").filter(Boolean) ?? [];
      const next = current.includes(et)
        ? current.filter((v) => v !== et)
        : [...current, et];
      setParam("eventType", next.join(","));
    },
    [searchParams, setParam],
  );

  // ---------------------------------------------------------------------------
  // Debounced actor
  // ---------------------------------------------------------------------------

  const handleActorChange = useCallback(
    (value: string) => {
      setLocalActor(value);
      clearTimeout(actorTimerRef.current);
      actorTimerRef.current = setTimeout(() => setParam("actor", value), 400);
    },
    [setParam],
  );

  // ---------------------------------------------------------------------------
  // Debounced full-text search
  // ---------------------------------------------------------------------------

  const handleSearchChange = useCallback(
    (value: string) => {
      setLocalSearch(value);
      clearTimeout(searchTimerRef.current);
      searchTimerRef.current = setTimeout(() => setParam("q", value), 400);
    },
    [setParam],
  );

  // ---------------------------------------------------------------------------
  // Clear all
  // ---------------------------------------------------------------------------

  const activeCount =
    eventType.length +
    (actor ? 1 : 0) +
    (resourceType ? 1 : 0) +
    (timeRange ? 1 : 0) +
    (search ? 1 : 0);

  const clearAll = useCallback(() => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      next.delete("eventType");
      next.delete("actor");
      next.delete("resourceType");
      next.delete("timeRange");
      next.delete("q");
      next.set("page", "1");
      return next;
    });
    setLocalActor("");
    setLocalSearch("");
  }, [setSearchParams]);

  // ---------------------------------------------------------------------------
  // Render
  // ---------------------------------------------------------------------------

  return (
    <div className="surface-card space-y-3 rounded-2xl p-4">
      {/* Row 1: Full-text search */}
      <div className="relative">
        <Input
          placeholder="Search audit log\u2026"
          value={localSearch}
          onChange={(e) => handleSearchChange(e.target.value)}
          className="pl-9"
        />
        <svg
          className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <circle cx="11" cy="11" r="8" strokeWidth="2" />
          <path d="m21 21-4.3-4.3" strokeWidth="2" strokeLinecap="round" />
        </svg>
      </div>

      {/* Row 2: Event type chips */}
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="mr-1 text-xs font-semibold text-muted">Event:</span>
        {EVENT_TYPES.map((et) => {
          const active = eventType.includes(et);
          return (
            <button
              key={et}
              type="button"
              onClick={() => toggleEventType(et)}
              className={cn(
                "rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors",
                active
                  ? "border-accent bg-accent/10 text-accent"
                  : "border-border text-muted hover:border-accent/40 hover:text-ink",
              )}
            >
              {et}
            </button>
          );
        })}
      </div>

      {/* Row 3: Actor, Resource Type, Time Range */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="w-48">
          <Input
            placeholder="Actor"
            value={localActor}
            onChange={(e) => handleActorChange(e.target.value)}
          />
        </div>

        <div className="w-44">
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

        {/* Time range toggle */}
        <div className="flex items-center gap-1 rounded-full border border-border p-0.5">
          {TIME_RANGES.map((tr) => (
            <button
              key={tr.value}
              type="button"
              onClick={() =>
                setParam("timeRange", timeRange === tr.value ? "" : tr.value)
              }
              className={cn(
                "rounded-full px-3 py-1 text-xs font-medium transition-colors",
                timeRange === tr.value
                  ? "bg-accent text-white"
                  : "text-muted hover:text-ink",
              )}
            >
              {tr.label}
            </button>
          ))}
        </div>

        {/* Active filter count + clear */}
        {activeCount > 0 && (
          <div className="ml-auto flex items-center gap-2">
            <Badge variant="info">{activeCount} active</Badge>
            <Button variant="ghost" size="sm" onClick={clearAll}>
              Clear all
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
