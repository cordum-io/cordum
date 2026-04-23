import { useCallback, useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
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
        aria-label={`${label} filters`}
        className={cn(
          "inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface-1/70 px-2.5 text-xs font-medium text-ink transition hover:border-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35 focus-visible:ring-offset-1 focus-visible:ring-offset-surface-0",
          selected.length > 0 && "border-accent/50 bg-accent/5",
        )}
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        {label}
        {selected.length > 0 && (
          <span className="inline-flex h-4 w-4 items-center justify-center rounded-full bg-accent text-[10px] font-bold text-white">
            {selected.length}
          </span>
        )}
      </button>
      {open && (
        <div className="absolute left-0 top-full z-20 mt-1 min-w-[180px] rounded-md border border-border bg-surface-1 p-1.5 shadow-lg">
          {options.map((opt) => (
            <label
              key={opt.value}
              className="flex min-h-8 cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-xs text-ink hover:bg-surface2/60"
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
  runId?: string;
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
  const runIdFilter = searchParams.get("runId") ?? "";
  const timeRangeFilter = searchParams.get("timeRange") ?? "";
  const updatedAfterFilter = searchParams.get("updatedAfter") ?? "";
  const updatedBeforeFilter = searchParams.get("updatedBefore") ?? "";
  const tenantFilter = searchParams.get("tenant") ?? "";

  // Local topic/tenant/pool/runId for debounce
  const [topicLocal, setTopicLocal] = useState(topicFilter);
  const [poolLocal, setPoolLocal] = useState(poolFilter);
  const [runIdLocal, setRunIdLocal] = useState(runIdFilter);
  const [tenantLocal, setTenantLocal] = useState(tenantFilter);
  const [showCustomRange, setShowCustomRange] = useState(timeRangeFilter === "custom");
  const topicTimer = useRef<ReturnType<typeof setTimeout>>();
  const poolTimer = useRef<ReturnType<typeof setTimeout>>();
  const runIdTimer = useRef<ReturnType<typeof setTimeout>>();
  const tenantTimer = useRef<ReturnType<typeof setTimeout>>();

  // Clear pending debounce timers on unmount
  useEffect(() => {
    return () => {
      clearTimeout(topicTimer.current);
      clearTimeout(poolTimer.current);
      clearTimeout(runIdTimer.current);
      clearTimeout(tenantTimer.current);
    };
  }, []);

  // Count active filters
  const activeCount =
    (stateFilter.length > 0 ? 1 : 0) +
    (decisionFilter.length > 0 ? 1 : 0) +
    (topicFilter ? 1 : 0) +
    (poolFilter ? 1 : 0) +
    (runIdFilter ? 1 : 0) +
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
      runId: runIdFilter || undefined,
      timeRange: timeRangeFilter || undefined,
      updatedAfter: updatedAfterFilter || undefined,
      updatedBefore: updatedBeforeFilter || undefined,
      tenant: tenantFilter || undefined,
    });
  }, [stateFilter.join(","), decisionFilter.join(","), topicFilter, poolFilter, runIdFilter, timeRangeFilter, updatedAfterFilter, updatedBeforeFilter, tenantFilter]);

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

  const handleRunIdChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const val = e.target.value;
      setRunIdLocal(val);
      clearTimeout(runIdTimer.current);
      runIdTimer.current = setTimeout(() => setFilters({ runId: val }), 400);
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
    setRunIdLocal("");
    setTenantLocal("");
    setShowCustomRange(false);
    setFilters({
      state: "",
      decision: "",
      topic: "",
      pool: "",
      runId: "",
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
    <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border/70 bg-surface-1/45 px-3 py-2" role="region" aria-label="Job filters">
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
      <Input
        type="text"
        placeholder="Topic"
        value={topicLocal}
        onChange={handleTopicChange}
        className="h-8 w-28 rounded-md px-2.5 py-1 text-xs"
        aria-label="Filter by topic"
      />

      {/* Pool text input (debounced) */}
      <Input
        type="text"
        placeholder="Pool"
        value={poolLocal}
        onChange={handlePoolChange}
        className="h-8 w-24 rounded-md px-2.5 py-1 text-xs"
        aria-label="Filter by pool"
      />

      {/* Run ID text input (debounced) */}
      <Input
        type="text"
        placeholder="Run ID"
        value={runIdLocal}
        onChange={handleRunIdChange}
        className="h-8 w-28 rounded-md px-2.5 py-1 text-xs font-mono"
        aria-label="Filter by run ID"
      />

      {/* Tenant text input (debounced) */}
      <Input
        type="text"
        placeholder="Tenant"
        value={tenantLocal}
        onChange={handleTenantChange}
        className="h-8 w-24 rounded-md px-2.5 py-1 text-xs font-mono"
        aria-label="Filter by tenant"
      />

      {/* Time range preset buttons */}
      <div className="flex items-center gap-0.5 rounded-md border border-border p-0.5" role="group" aria-label="Quick time filters">
        {TIME_RANGES.map((tr) => (
          <button
            key={tr.value}
            type="button"
            onClick={() => handleTimeRange(tr.value)}
            className={cn(
              "h-8 rounded-md px-2 py-1 text-xs font-medium transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35 focus-visible:ring-offset-1 focus-visible:ring-offset-surface-0",
              timeRangeFilter === tr.value
                ? "bg-accent text-white"
                : "text-muted hover:text-ink hover:bg-surface2/60",
            )}
          >
            {tr.label}
          </button>
        ))}
        <button
          type="button"
          onClick={() => handleTimeRange("custom")}
          className={cn(
            "h-8 rounded-md px-2 py-1 text-xs font-medium transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35 focus-visible:ring-offset-1 focus-visible:ring-offset-surface-0",
            timeRangeFilter === "custom"
              ? "bg-accent text-white"
              : "text-muted hover:text-ink hover:bg-surface2/60",
          )}
        >
          Custom
        </button>
      </div>

      {/* Custom date range inputs */}
      {showCustomRange && (
        <div className="flex items-center gap-1.5">
          <Input
            type="datetime-local"
            value={updatedAfterFilter}
            onChange={(e) => setFilters({ updatedAfter: e.target.value })}
            className="h-8 rounded-md px-2 py-1 text-xs"
            aria-label="Updated after"
          />
          <span className="text-xs text-muted">to</span>
          <Input
            type="datetime-local"
            value={updatedBeforeFilter}
            onChange={(e) => setFilters({ updatedBefore: e.target.value })}
            className="h-8 rounded-md px-2 py-1 text-xs"
            aria-label="Updated before"
          />
        </div>
      )}

      {/* Active count + clear */}
      {activeCount > 0 && (
        <>
          <Badge variant="info" className="text-[10px]">{activeCount} filter{activeCount !== 1 ? "s" : ""}</Badge>
          <Button variant="ghost" size="sm" className="h-8 text-xs" onClick={clearAll}>
            Clear all
          </Button>
        </>
      )}
    </div>
  );
}
