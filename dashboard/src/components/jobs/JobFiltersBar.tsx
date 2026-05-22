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
          <span className="inline-flex h-4 w-4 items-center justify-center rounded-full bg-accent text-xs font-bold text-primary-foreground">
            {selected.length}
          </span>
        )}
      </button>
      {open && (
        <div className="absolute left-0 top-full z-20 mt-1 min-w-[160px] rounded-xl border border-border bg-card p-1.5 shadow-lg">
          {options.map((opt) => (
            <label
              key={opt.value}
              className="flex cursor-pointer items-center gap-2 rounded-xl px-2.5 py-1.5 text-xs text-ink hover:bg-surface2/60"
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
// Advanced filters popover
// ---------------------------------------------------------------------------
// Collects the 5 free-text filters (Topic/Pool/Tenant/Session/Run) behind a
// single "Filter" trigger so the always-visible JobFiltersBar surface stays
// quiet. Mirrors MultiSelect's click-outside / position semantics so the
// affordance reads consistently with the rest of the bar.

interface AdvancedFiltersPopoverProps {
  activeCount: number;
  topicLocal: string;
  poolLocal: string;
  tenantLocal: string;
  sessionIdLocal: string;
  runIdLocal: string;
  onTopicChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  onPoolChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  onTenantChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  onSessionIdChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  onRunIdChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  onClearAdvanced: () => void;
}

function AdvancedFiltersPopover({
  activeCount,
  topicLocal,
  poolLocal,
  tenantLocal,
  sessionIdLocal,
  runIdLocal,
  onTopicChange,
  onPoolChange,
  onTenantChange,
  onSessionIdChange,
  onRunIdChange,
  onClearAdvanced,
}: AdvancedFiltersPopoverProps) {
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

  const inputClass =
    "w-full rounded-xl border border-border bg-card/70 px-3 py-1.5 text-xs text-ink placeholder:text-muted/60 transition hover:border-accent/40 focus:outline-none focus:border-accent focus:ring-1 focus:ring-accent/30";
  const labelClass = "block text-[11px] font-medium text-muted-foreground";

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-controls="job-filters-advanced-popover"
        className={cn(
          "inline-flex items-center gap-1.5 rounded-xl border border-border bg-card/70 px-3 py-1.5 text-xs font-medium text-ink transition hover:border-accent/40",
          activeCount > 0 && "border-accent/50 bg-accent/5",
        )}
      >
        Filter
        {activeCount > 0 && (
          <span
            data-testid="advanced-filters-count"
            className="inline-flex h-4 min-w-[1rem] items-center justify-center rounded-full bg-accent px-1 text-xs font-bold text-primary-foreground"
          >
            {activeCount}
          </span>
        )}
      </button>
      {open && (
        <div
          id="job-filters-advanced-popover"
          role="dialog"
          aria-label="Advanced filters"
          className="absolute left-0 top-full z-20 mt-1 w-64 rounded-xl border border-border bg-card p-3 shadow-lg"
        >
          <div className="space-y-2">
            <div>
              <label className={labelClass} htmlFor="adv-topic">Topic</label>
              <input
                id="adv-topic"
                type="text"
                placeholder="Topic"
                value={topicLocal}
                onChange={onTopicChange}
                className={inputClass}
              />
            </div>
            <div>
              <label className={labelClass} htmlFor="adv-pool">Pool</label>
              <input
                id="adv-pool"
                type="text"
                placeholder="Pool"
                value={poolLocal}
                onChange={onPoolChange}
                className={inputClass}
              />
            </div>
            <div>
              <label className={labelClass} htmlFor="adv-tenant">Tenant</label>
              <input
                id="adv-tenant"
                type="text"
                placeholder="Tenant"
                value={tenantLocal}
                onChange={onTenantChange}
                className={inputClass}
              />
            </div>
            <div>
              <label className={labelClass} htmlFor="adv-session-id">Session ID</label>
              <input
                id="adv-session-id"
                type="text"
                placeholder="Session ID"
                value={sessionIdLocal}
                onChange={onSessionIdChange}
                className={inputClass}
              />
            </div>
            <div>
              <label className={labelClass} htmlFor="adv-run-id">Run ID</label>
              <input
                id="adv-run-id"
                type="text"
                placeholder="Run ID"
                value={runIdLocal}
                onChange={onRunIdChange}
                className={inputClass}
              />
            </div>
          </div>
          {activeCount > 0 && (
            <div className="mt-3 flex justify-end border-t border-border pt-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={onClearAdvanced}
              >
                Clear advanced filters
              </Button>
            </div>
          )}
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
  sessionId?: string;
  runId?: string;
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
  const sessionIdFilter = searchParams.get("sessionId") ?? "";
  const runIdFilter = searchParams.get("runId") ?? "";

  // Local inputs for debounce
  const [topicLocal, setTopicLocal] = useState(topicFilter);
  const [poolLocal, setPoolLocal] = useState(poolFilter);
  const [tenantLocal, setTenantLocal] = useState(tenantFilter);
  const [sessionIdLocal, setSessionIdLocal] = useState(sessionIdFilter);
  const [runIdLocal, setRunIdLocal] = useState(runIdFilter);

  // Re-sync local inputs when URL changes externally (back/forward
  // navigation, deep links). Without this, the local debounced state can
  // drift from the URL state and silently restore stale filters.
  useEffect(() => { setTopicLocal(topicFilter); }, [topicFilter]);
  useEffect(() => { setPoolLocal(poolFilter); }, [poolFilter]);
  useEffect(() => { setTenantLocal(tenantFilter); }, [tenantFilter]);
  useEffect(() => { setSessionIdLocal(sessionIdFilter); }, [sessionIdFilter]);
  useEffect(() => { setRunIdLocal(runIdFilter); }, [runIdFilter]);

  const [showCustomRange, setShowCustomRange] = useState(timeRangeFilter === "custom");
  const topicTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const poolTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const tenantTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const sessionIdTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const runIdTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Clear pending debounce timers on unmount
  useEffect(() => {
    return () => {
      clearTimeout(topicTimer.current);
      clearTimeout(poolTimer.current);
      clearTimeout(tenantTimer.current);
      clearTimeout(sessionIdTimer.current);
      clearTimeout(runIdTimer.current);
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
    (tenantFilter ? 1 : 0) +
    (sessionIdFilter ? 1 : 0) +
    (runIdFilter ? 1 : 0);

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
      sessionId: sessionIdFilter || undefined,
      runId: runIdFilter || undefined,
    });
  }, [stateFilter.join(","), decisionFilter.join(","), topicFilter, poolFilter, timeRangeFilter, updatedAfterFilter, updatedBeforeFilter, tenantFilter, sessionIdFilter, runIdFilter]);

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

  const handleSessionIdChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const val = e.target.value;
      setSessionIdLocal(val);
      clearTimeout(sessionIdTimer.current);
      sessionIdTimer.current = setTimeout(() => setFilters({ sessionId: val }), 400);
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
    // Cancel any pending debounce callbacks before resetting; otherwise a
    // user who was mid-typing can have a stale `setFilters({ topic: "..." })`
    // fire after Clear-all and silently re-populate the URL.
    clearTimeout(topicTimer.current);
    clearTimeout(poolTimer.current);
    clearTimeout(tenantTimer.current);
    clearTimeout(sessionIdTimer.current);
    clearTimeout(runIdTimer.current);
    setTopicLocal("");
    setPoolLocal("");
    setTenantLocal("");
    setSessionIdLocal("");
    setRunIdLocal("");
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
      sessionId: "",
      runId: "",
    });
  }, [setFilters]);

  // Clear ONLY the 5 advanced text filters (Topic/Pool/Tenant/Session/Run).
  // Keeps state/decision/timeRange untouched — those are owned by visible
  // controls. Mirrors clearAll's debounce-cancel pattern so a stale pending
  // setFilters can't re-populate the URL after Clear advanced.
  const clearAdvancedTextFilters = useCallback(() => {
    clearTimeout(topicTimer.current);
    clearTimeout(poolTimer.current);
    clearTimeout(tenantTimer.current);
    clearTimeout(sessionIdTimer.current);
    clearTimeout(runIdTimer.current);
    setTopicLocal("");
    setPoolLocal("");
    setTenantLocal("");
    setSessionIdLocal("");
    setRunIdLocal("");
    setFilters({
      topic: "",
      pool: "",
      tenant: "",
      sessionId: "",
      runId: "",
    });
  }, [setFilters]);

  // Count of active advanced text filters — drives the Filter trigger chip.
  // Derived from the URL state so back/forward navigation updates it.
  const advancedTextActiveCount =
    (topicFilter ? 1 : 0) +
    (poolFilter ? 1 : 0) +
    (tenantFilter ? 1 : 0) +
    (sessionIdFilter ? 1 : 0) +
    (runIdFilter ? 1 : 0);

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

      {/* Advanced text filters — Topic/Pool/Tenant/Session/Run — collapsed
          behind a Filter popover trigger. The 5 inputs moved here so the
          always-visible bar shows the main search + categorical controls
          only; the popover shows the typed-token controls when needed.
          URL state for the 5 fields is preserved (nuqs/searchParams) so
          deep-links still work and reload through the popover. */}
      <AdvancedFiltersPopover
        activeCount={advancedTextActiveCount}
        topicLocal={topicLocal}
        poolLocal={poolLocal}
        tenantLocal={tenantLocal}
        sessionIdLocal={sessionIdLocal}
        runIdLocal={runIdLocal}
        onTopicChange={handleTopicChange}
        onPoolChange={handlePoolChange}
        onTenantChange={handleTenantChange}
        onSessionIdChange={handleSessionIdChange}
        onRunIdChange={handleRunIdChange}
        onClearAdvanced={clearAdvancedTextFilters}
      />

      {/* Time range preset buttons */}
      <div className="flex items-center gap-0.5 rounded-xl border border-border p-0.5">
        {TIME_RANGES.map((tr) => (
          <button
            key={tr.value}
            type="button"
            onClick={() => handleTimeRange(tr.value)}
            className={cn(
              "rounded-xl px-2.5 py-1 text-xs font-medium transition",
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
            "rounded-xl px-2.5 py-1 text-xs font-medium transition",
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

