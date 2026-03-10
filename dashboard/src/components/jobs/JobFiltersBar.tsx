import { useCallback, useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { cn } from "../../lib/utils";
import type { JobStatus } from "../../api/types";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const JOB_STATUSES: JobStatus[] = [
  "pending",
  "scheduled",
  "dispatched",
  "running",
  "succeeded",
  "failed",
  "cancelled",
  "approval_required",
  "denied",
  "timeout",
  "output_quarantined",
];

const DECISION_TYPES = [
  { value: "allow", label: "Allow" },
  { value: "deny", label: "Deny" },
  { value: "require_approval", label: "Approval" },
  { value: "throttle", label: "Throttle" },
] as const;

const TIME_RANGES = [
  { value: "1h", label: "1h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
] as const;

// ---------------------------------------------------------------------------
// Multi-select dropdown
// ---------------------------------------------------------------------------

function MultiSelect({
  label,
  options,
  selected,
  onChange,
}: {
  label: string;
  options: readonly { value: string; label: string }[];
  selected: string[];
  onChange: (values: string[]) => void;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, []);

  const toggle = useCallback(
    (value: string) => {
      onChange(
        selected.includes(value)
          ? selected.filter((v) => v !== value)
          : [...selected, value],
      );
    },
    [selected, onChange],
  );

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className={cn(
          "inline-flex items-center gap-1.5 rounded-xl border border-border bg-card/70 px-3 py-1.5 text-xs font-medium text-ink transition hover:border-accent/40",
          selected.length > 0 && "border-accent/50 bg-accent/5",
        )}
      >
        {label}
        {selected.length > 0 && (
          <span className="inline-flex h-4 w-4 items-center justify-center rounded-full bg-accent text-[10px] font-bold text-primary-foreground">
            {selected.length}
          </span>
        )}
      </button>
      {open && (
        <div className="absolute left-0 top-full z-20 mt-1 min-w-[160px] rounded-xl border border-border bg-card p-1.5 shadow-lg">
          {options.map((opt) => (
            <label
              key={opt.value}
              className="flex cursor-pointer items-center gap-2 rounded-lg px-2.5 py-1.5 text-xs text-ink hover:bg-surface2/60"
            >
              <input
                type="checkbox"
                checked={selected.includes(opt.value)}
                onChange={() => toggle(opt.value)}
                className="rounded border-border"
              />
              {opt.label}
            </label>
          ))}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// JobFiltersBar
// ---------------------------------------------------------------------------

export interface JobFilterValues {
  state?: JobStatus[];
  decision?: string[];
  topic?: string;
  pool?: string;
  timeRange?: string;
  updatedAfter?: string;
  updatedBefore?: string;
  tenant?: string;
}

export function JobFiltersBar({
  onChange,
}: {
  onChange: (filters: JobFilterValues) => void;
}) {
  const [searchParams, setSearchParams] = useSearchParams();

  // Parse from URL
  const stateFilter = (searchParams.get("state")?.split(",").filter(Boolean) ?? []) as JobStatus[];
  const decisionFilter = searchParams.get("decision")?.split(",").filter(Boolean) ?? [];
  const topicFilter = searchParams.get("topic") ?? "";
  const poolFilter = searchParams.get("pool") ?? "";
  const timeRangeFilter = searchParams.get("timeRange") ?? "";
  const updatedAfterFilter = searchParams.get("updatedAfter") ?? "";
  const updatedBeforeFilter = searchParams.get("updatedBefore") ?? "";
  const tenantFilter = searchParams.get("tenant") ?? "";

  // Local topic/tenant/pool for debounce
  const [topicLocal, setTopicLocal] = useState(topicFilter);
  const [poolLocal, setPoolLocal] = useState(poolFilter);
  const [tenantLocal, setTenantLocal] = useState(tenantFilter);
  const [showCustomRange, setShowCustomRange] = useState(timeRangeFilter === "custom");
  const topicTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const poolTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const tenantTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Clear pending debounce timers on unmount
  useEffect(() => {
    return () => {
      clearTimeout(topicTimer.current);
      clearTimeout(poolTimer.current);
      clearTimeout(tenantTimer.current);
    };
  }, []);

  // Count active filters
  const activeCount =
    (stateFilter.length > 0 ? 1 : 0) +
    (decisionFilter.length > 0 ? 1 : 0) +
    (topicFilter ? 1 : 0) +
    (poolFilter ? 1 : 0) +
    (timeRangeFilter ? 1 : 0) +
    (updatedAfterFilter ? 1 : 0) +
    (updatedBeforeFilter ? 1 : 0) +
    (tenantFilter ? 1 : 0);

  // Setter: update URL params and notify parent
  const setFilters = useCallback(
    (patch: Partial<Record<string, string>>) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        for (const [k, v] of Object.entries(patch)) {
          if (v) next.set(k, v);
          else next.delete(k);
        }
        return next;
      });
    },
    [setSearchParams],
  );

  // Notify parent whenever URL params change
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  useEffect(() => {
    onChangeRef.current({
      state: stateFilter.length > 0 ? stateFilter : undefined,
      decision: decisionFilter.length > 0 ? decisionFilter : undefined,
      topic: topicFilter || undefined,
      pool: poolFilter || undefined,
      timeRange: timeRangeFilter || undefined,
      updatedAfter: updatedAfterFilter || undefined,
      updatedBefore: updatedBeforeFilter || undefined,
      tenant: tenantFilter || undefined,
    });
  }, [stateFilter.join(","), decisionFilter.join(","), topicFilter, poolFilter, timeRangeFilter, updatedAfterFilter, updatedBeforeFilter, tenantFilter]);

  // Handlers
  const handleStateChange = useCallback(
    (values: string[]) => setFilters({ state: values.join(",") }),
    [setFilters],
  );

  const handleDecisionChange = useCallback(
    (values: string[]) => setFilters({ decision: values.join(",") }),
    [setFilters],
  );

  const handlePoolChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const val = e.target.value;
      setPoolLocal(val);
      clearTimeout(poolTimer.current);
      poolTimer.current = setTimeout(() => setFilters({ pool: val }), 400);
    },
    [setFilters],
  );

  const handleTopicChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const val = e.target.value;
      setTopicLocal(val);
      clearTimeout(topicTimer.current);
      topicTimer.current = setTimeout(() => setFilters({ topic: val }), 400);
    },
    [setFilters],
  );

  const handleTenantChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const val = e.target.value;
      setTenantLocal(val);
      clearTimeout(tenantTimer.current);
      tenantTimer.current = setTimeout(() => setFilters({ tenant: val }), 400);
    },
    [setFilters],
  );

  const handleTimeRange = useCallback(
    (value: string) => {
      if (value === "custom") {
        setShowCustomRange((prev) => !prev);
        setFilters({ timeRange: "custom" });
        return;
      }
      setShowCustomRange(false);
      setFilters({
        timeRange: timeRangeFilter === value ? "" : value,
        updatedAfter: "",
        updatedBefore: "",
      });
    },
    [setFilters, timeRangeFilter],
  );

  const clearAll = useCallback(() => {
    setTopicLocal("");
    setPoolLocal("");
    setTenantLocal("");
    setShowCustomRange(false);
    setFilters({
      state: "",
      decision: "",
      topic: "",
      pool: "",
      timeRange: "",
      updatedAfter: "",
      updatedBefore: "",
      tenant: "",
    });
  }, [setFilters]);

  const statusOptions = JOB_STATUSES.map((s) => ({
    value: s,
    label: s.charAt(0).toUpperCase() + s.slice(1),
  }));

  return (
    <div className="flex flex-wrap items-center gap-2">
      {/* State multi-select */}
      <MultiSelect
        label="State"
        options={statusOptions}
        selected={stateFilter}
        onChange={handleStateChange}
      />

      {/* Decision type multi-select */}
      <MultiSelect
        label="Decision"
        options={DECISION_TYPES}
        selected={decisionFilter}
        onChange={handleDecisionChange}
      />

      {/* Topic text input (debounced) */}
      <input
        type="text"
        placeholder="Topic"
        value={topicLocal}
        onChange={handleTopicChange}
        className="w-28 rounded-xl border border-border bg-card/70 px-3 py-1.5 text-xs text-ink placeholder:text-muted/60 transition hover:border-accent/40 focus:outline-none focus:border-accent focus:ring-1 focus:ring-accent/30"
      />

      {/* Pool text input (debounced) */}
      <input
        type="text"
        placeholder="Pool"
        value={poolLocal}
        onChange={handlePoolChange}
        className="w-24 rounded-xl border border-border bg-card/70 px-3 py-1.5 text-xs text-ink placeholder:text-muted/60 transition hover:border-accent/40 focus:outline-none focus:border-accent focus:ring-1 focus:ring-accent/30"
      />

      {/* Tenant text input (debounced) */}
      <input
        type="text"
        placeholder="Tenant"
        value={tenantLocal}
        onChange={handleTenantChange}
        className="w-24 rounded-xl border border-border bg-card/70 px-3 py-1.5 text-xs text-ink placeholder:text-muted/60 transition hover:border-accent/40 focus:outline-none focus:border-accent focus:ring-1 focus:ring-accent/30"
      />

      {/* Time range preset buttons */}
      <div className="flex items-center gap-0.5 rounded-xl border border-border p-0.5">
        {TIME_RANGES.map((tr) => (
          <button
            key={tr.value}
            type="button"
            onClick={() => handleTimeRange(tr.value)}
            className={cn(
              "rounded-lg px-2.5 py-1 text-xs font-medium transition",
              timeRangeFilter === tr.value
                ? "bg-accent text-primary-foreground"
                : "text-muted-foreground hover:text-ink hover:bg-surface2/60",
            )}
          >
            {tr.label}
          </button>
        ))}
        <button
          type="button"
          onClick={() => handleTimeRange("custom")}
          className={cn(
            "rounded-lg px-2.5 py-1 text-xs font-medium transition",
            timeRangeFilter === "custom"
              ? "bg-accent text-primary-foreground"
              : "text-muted-foreground hover:text-ink hover:bg-surface2/60",
          )}
        >
          Custom
        </button>
      </div>

      {/* Custom date range inputs */}
      {showCustomRange && (
        <div className="flex items-center gap-1.5">
          <input
            type="datetime-local"
            value={updatedAfterFilter}
            onChange={(e) => setFilters({ updatedAfter: e.target.value })}
            className="rounded-xl border border-border bg-card/70 px-2 py-1 text-xs text-ink"
          />
          <span className="text-xs text-muted-foreground">to</span>
          <input
            type="datetime-local"
            value={updatedBeforeFilter}
            onChange={(e) => setFilters({ updatedBefore: e.target.value })}
            className="rounded-xl border border-border bg-card/70 px-2 py-1 text-xs text-ink"
          />
        </div>
      )}

      {/* Active count + clear */}
      {activeCount > 0 && (
        <>
          <Badge variant="info">{activeCount} filter{activeCount !== 1 ? "s" : ""}</Badge>
          <Button variant="ghost" size="sm" onClick={clearAll}>
            Clear all
          </Button>
        </>
      )}
    </div>
  );
}
