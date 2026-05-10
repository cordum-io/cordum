import { useMemo } from "react";
import { useQueryState, parseAsString, parseAsStringLiteral } from "nuqs";
import { Filter, RefreshCw, X } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Select } from "@/components/ui/Select";
import { LabeledField } from "@/components/ui/LabeledField";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import { DecisionType } from "@/api/generated/model/decisionType";
import type { ListPolicyDecisionsParams } from "@/api/generated/model/listPolicyDecisionsParams";

const SOURCE_VALUES: ReadonlyArray<DecisionSource> = [
  DecisionSource.job,
  DecisionSource.edge,
];

const TYPE_VALUES: ReadonlyArray<DecisionType> = [
  DecisionType.allow,
  DecisionType.deny,
  DecisionType.require_human,
  DecisionType.throttle,
  DecisionType.allow_with_constraints,
  DecisionType.quarantine,
  DecisionType.redact,
];

// Time presets emit RFC3339 lower-bounds for the `since` query param.
// `since=` controls the rolling window; `until=` is left unset so the
// query runs against "now" for the live feel.
const TIME_PRESETS = [
  { label: "Last 5 min", minutes: 5 },
  { label: "Last 1 hr", minutes: 60 },
  { label: "Last 24 hr", minutes: 60 * 24 },
  { label: "Last 7 days", minutes: 60 * 24 * 7 },
] as const;

const DEFAULT_PRESET_MINUTES = 60;

function isoSinceMinutesAgo(minutes: number): string {
  return new Date(Date.now() - minutes * 60_000).toISOString();
}

function presetMinutesFromSince(since: string | null): number {
  if (!since) return DEFAULT_PRESET_MINUTES;
  const ts = Date.parse(since);
  if (Number.isNaN(ts)) return DEFAULT_PRESET_MINUTES;
  const minutes = Math.round((Date.now() - ts) / 60_000);
  // Snap to the nearest preset boundary so the picker shows a clean
  // selection. Off-preset windows fall back to the default rather than
  // displaying "0 min" or a stale value.
  for (const p of TIME_PRESETS) {
    if (Math.abs(p.minutes - minutes) < p.minutes * 0.1) return p.minutes;
  }
  return DEFAULT_PRESET_MINUTES;
}

export interface DecisionsFilterValues
  extends Pick<ListPolicyDecisionsParams, "source" | "type" | "since" | "until" | "limit"> {}

interface DecisionsFilterBarProps {
  totalCount?: number;
  onRefresh?: () => void;
  isFetching?: boolean;
}

/**
 * Filter bar for /policies/decisions. nuqs URL state owns: source, type,
 * since (preset minutes), until (rarely used; reserved for D8b cursor
 * pagination). Charts toggle + Live toggle both ship in D8b alongside the
 * stream + charts panel; this filter bar exposes only the canonical
 * server-supported filters today.
 *
 * Returns the parsed filter values via the parent's controlled getter
 * (see `useDecisionsFilters` hook below) so the page's data hook + the
 * filter bar share a single source of truth.
 */
export function DecisionsFilterBar({
  totalCount,
  onRefresh,
  isFetching,
}: DecisionsFilterBarProps) {
  const [source, setSource] = useQueryState(
    "source",
    parseAsStringLiteral(SOURCE_VALUES),
  );
  const [type, setType] = useQueryState(
    "type",
    parseAsStringLiteral(TYPE_VALUES),
  );
  const [since, setSince] = useQueryState("since", parseAsString);

  const presetMinutes = useMemo(() => presetMinutesFromSince(since), [since]);

  const handlePresetChange = (value: string) => {
    const minutes = Number(value);
    if (!Number.isFinite(minutes)) return;
    void setSince(isoSinceMinutesAgo(minutes));
  };

  const filtersActive = Boolean(source || type);

  const clearFilters = () => {
    void setSource(null);
    void setType(null);
  };

  return (
    <div className="space-y-3 rounded-2xl border border-border bg-surface-1 p-3">
      <div className="flex flex-wrap items-end gap-3">
        <LabeledField label="Time range" className="w-44">
          <Select
            aria-label="Time range"
            value={String(presetMinutes)}
            onChange={(event) => handlePresetChange(event.target.value)}
          >
            {TIME_PRESETS.map((p) => (
              <option key={p.minutes} value={String(p.minutes)}>
                {p.label}
              </option>
            ))}
          </Select>
        </LabeledField>

        <LabeledField label="Decision" className="w-52">
          <Select
            aria-label="Decision filter"
            value={type ?? ""}
            onChange={(event) => {
              const next = event.target.value;
              void setType(next ? (next as DecisionType) : null);
            }}
          >
            <option value="">All decisions</option>
            {TYPE_VALUES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </Select>
        </LabeledField>

        <LabeledField label="Source" className="w-40">
          <Select
            aria-label="Source filter"
            value={source ?? ""}
            onChange={(event) => {
              const next = event.target.value;
              void setSource(next ? (next as DecisionSource) : null);
            }}
          >
            <option value="">job + edge</option>
            <option value={DecisionSource.job}>job</option>
            <option value={DecisionSource.edge}>edge</option>
          </Select>
        </LabeledField>

        <div className="ml-auto flex items-center gap-2">
          {filtersActive && (
            <Button
              variant="ghost"
              size="sm"
              onClick={clearFilters}
              aria-label="Clear filters"
            >
              <X className="mr-1 h-3.5 w-3.5" aria-hidden />
              Clear
            </Button>
          )}
          {onRefresh && (
            <Button
              variant="outline"
              size="sm"
              onClick={onRefresh}
              loading={isFetching}
              aria-label="Refresh decisions"
            >
              <RefreshCw className="mr-1 h-3.5 w-3.5" aria-hidden />
              Refresh
            </Button>
          )}
        </div>
      </div>

      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Filter aria-hidden className="h-3.5 w-3.5" />
        <span>
          {filtersActive
            ? `Filtered by ${[source && `source=${source}`, type && `type=${type}`].filter(Boolean).join(", ")}`
            : "No filters applied"}
        </span>
        {typeof totalCount === "number" && (
          <span className="ml-auto font-mono">{totalCount} decisions</span>
        )}
      </div>
    </div>
  );
}

/**
 * Hook for the page to read the current URL filter state without
 * subscribing the filter bar twice. Mirrors DecisionsFilterBar's nuqs
 * keys so the data hook receives a single canonical params object.
 *
 * Memoizes against `since`'s rounded-to-minute value so the React Query
 * key is stable across the second-by-second `Date.now()` drift in the
 * absence of an explicit URL `since` param.
 */
export function useDecisionsFilterValues(): DecisionsFilterValues {
  const [source] = useQueryState("source", parseAsStringLiteral(SOURCE_VALUES));
  const [type] = useQueryState("type", parseAsStringLiteral(TYPE_VALUES));
  const [since] = useQueryState("since", parseAsString);
  return useMemo<DecisionsFilterValues>(() => {
    // Floor `since` to the minute so a single `?since=...` URL stays
    // stable across renders even when generated from `Date.now()`.
    const sinceIso =
      since ?? new Date(Math.floor(Date.now() / 60_000) * 60_000 - DEFAULT_PRESET_MINUTES * 60_000).toISOString();
    return {
      source: source ?? undefined,
      type: type ?? undefined,
      since: sinceIso,
      limit: 100,
    };
  }, [source, type, since]);
}
