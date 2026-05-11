import { useMemo } from "react";
import { AlertTriangle, GaugeCircle, Loader2 } from "lucide-react";
import { EmptyState } from "@/components/ui/EmptyState";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { useRecentDecisionsForType } from "@/hooks/useRecentDecisionsForType";
import {
  SIMULATOR_BUCKETS,
  useEvaluateRule,
  type SimulatorBucket,
  type SimulatorOutcome,
} from "@/hooks/useEvaluateRule";
import type { NormalizedRule } from "@/hooks/useRulesList";
import { ruleHasKnownType } from "@/hooks/useRule";

const MATCHING_LIST_LIMIT = 10;

interface RuleInlineSimulatorProps {
  draft: NormalizedRule;
}

/**
 * Persistent bottom panel under the Rule editor (yaml + form views).
 * Fetches the last 100 audit/decision events for the active rule type,
 * evaluates the in-progress draft against each event via the unified
 * /api/v1/policy/evaluate endpoint, and surfaces aggregate counts +
 * a bounded matching-event list. Failures are bucketed (no crash);
 * the panel never mutates downstream policy state.
 */
export function RuleInlineSimulator({ draft }: RuleInlineSimulatorProps) {
  const isKnownType = ruleHasKnownType(draft);
  const ruleTypeForFetch = isKnownType ? draft.type : undefined;
  const audit = useRecentDecisionsForType(ruleTypeForFetch);

  const entries = audit.data?.entries;
  const result = useEvaluateRule({
    rule: isKnownType ? draft : null,
    entries,
    enabled: isKnownType && !audit.isPending && !audit.isError,
  });

  return (
    <section
      aria-label="Inline policy simulator"
      className="rounded-2xl border border-border bg-surface-1 p-3"
    >
      <header className="flex items-center gap-2 pb-2 text-xs font-mono uppercase tracking-wider text-muted-foreground">
        <GaugeCircle aria-hidden className="h-3.5 w-3.5" />
        Simulator
        <span className="ml-auto rounded-full bg-surface-2 px-2 py-0.5 text-[0.6rem] tracking-wide">
          last 100 events
        </span>
        {result && !result.disabled && (
          <span
            className="rounded-full bg-surface-2 px-2 py-0.5 text-[0.6rem] tracking-wide"
            data-testid="simulator-elapsed-ms"
          >
            {Math.round(result.elapsedMs)}ms
          </span>
        )}
      </header>

      {!isKnownType && <SimulatorDisabled reason="Rule type unknown — pick a known type to enable the simulator." />}

      {isKnownType && audit.isPending && <SimulatorLoading />}

      {isKnownType && !audit.isPending && audit.isError && (
        <SimulatorBackendError onRetry={() => void audit.refetch()} />
      )}

      {isKnownType && audit.data && audit.data.entries.length === 0 && (
        <SimulatorEmpty />
      )}

      {isKnownType &&
        audit.data &&
        audit.data.entries.length > 0 &&
        result?.disabled && (
          <SimulatorDisabled reason={result.disabledReason ?? "Simulator unavailable."} />
        )}

      {isKnownType &&
        audit.data &&
        audit.data.entries.length > 0 &&
        result &&
        !result.disabled && (
          <SimulatorResults
            counts={result.counts}
            outcomes={result.outcomes}
            totalEntries={audit.data.entries.length}
          />
        )}

      {isKnownType &&
        audit.data &&
        audit.data.entries.length > 0 &&
        result === null && <SimulatorRunning />}
    </section>
  );
}

function SimulatorLoading() {
  return (
    <div
      className="flex items-center gap-2 px-1 py-3 text-xs text-muted-foreground"
      role="status"
    >
      <Loader2 aria-hidden className="h-3.5 w-3.5 animate-spin" />
      Loading recent events…
    </div>
  );
}

function SimulatorRunning() {
  return (
    <div
      className="flex items-center gap-2 px-1 py-3 text-xs text-muted-foreground"
      role="status"
    >
      <Loader2 aria-hidden className="h-3.5 w-3.5 animate-spin" />
      Evaluating draft against events…
    </div>
  );
}

function SimulatorBackendError({ onRetry }: { onRetry: () => void }) {
  return (
    <EmptyState
      icon={<AlertTriangle className="h-4 w-4 text-warning" />}
      title="Couldn't load recent events"
      description="The audit endpoint returned an error. Retry, or close and reopen the editor once the backend recovers."
      action={
        <button
          type="button"
          onClick={onRetry}
          className="text-xs font-medium text-cordum hover:text-cordum/80"
        >
          Retry
        </button>
      }
    />
  );
}

function SimulatorEmpty() {
  return (
    <EmptyState
      icon={<GaugeCircle className="h-4 w-4 text-muted-foreground" />}
      title="No recent events for this rule type"
      description="Nothing to simulate against yet. Once the policy emits decisions for this type, they'll appear here."
    />
  );
}

function SimulatorDisabled({ reason }: { reason: string }) {
  return (
    <EmptyState
      icon={<AlertTriangle className="h-4 w-4 text-warning" />}
      title="Simulator unavailable"
      description={reason}
    />
  );
}

interface SimulatorResultsProps {
  counts: Record<SimulatorBucket, number>;
  outcomes: ReadonlyArray<SimulatorOutcome>;
  totalEntries: number;
}

function SimulatorResults({ counts, outcomes, totalEntries }: SimulatorResultsProps) {
  const matching = useMemo(
    () => outcomes.filter((o) => o.bucket !== "allow" && o.bucket !== "error").slice(0, MATCHING_LIST_LIMIT),
    [outcomes],
  );

  return (
    <div className="space-y-3">
      <ul
        className="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-8"
        aria-label="Decision counts"
      >
        {SIMULATOR_BUCKETS.map((bucket) => (
          <li key={bucket}>
            <div
              data-testid={`simulator-bucket-${bucket}`}
              className="flex flex-col gap-0.5 rounded-xl border border-border bg-surface-2 px-2 py-1.5"
            >
              <StatusBadge variant={bucketVariant(bucket)}>
                <span className="text-[0.6rem] font-mono uppercase tracking-wider">
                  {bucketLabel(bucket)}
                </span>
              </StatusBadge>
              <span className="text-base font-semibold tabular-nums text-foreground">
                {counts[bucket]}
              </span>
            </div>
          </li>
        ))}
      </ul>

      <p className="text-[0.65rem] text-muted-foreground" data-testid="simulator-total">
        Evaluated {totalEntries} events.{" "}
        {counts.error > 0 && (
          <span className="text-warning">
            {counts.error} failed to construct a context.
          </span>
        )}
      </p>

      {matching.length > 0 && (
        <div>
          <p className="mb-1 text-[0.65rem] font-mono uppercase tracking-wider text-muted-foreground">
            Matching events ({matching.length} shown)
          </p>
          <ul className="space-y-1" aria-label="Matching events">
            {matching.map((outcome) => (
              <li
                key={outcome.entryId || `synthetic-${Math.random()}`}
                data-testid={`simulator-match-${outcome.bucket}`}
                className="flex items-center justify-between gap-2 rounded-lg border border-border bg-surface-2 px-2 py-1 text-xs"
              >
                <span className="truncate font-mono text-muted-foreground">
                  {outcome.resourceId || outcome.entryId || "—"}
                </span>
                <StatusBadge variant={bucketVariant(outcome.bucket)}>
                  <span className="text-[0.6rem] font-mono uppercase tracking-wider">
                    {bucketLabel(outcome.bucket)}
                  </span>
                </StatusBadge>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

function bucketLabel(bucket: SimulatorBucket): string {
  switch (bucket) {
    case "allow":
      return "allow";
    case "deny":
      return "deny";
    case "require_human":
      return "human";
    case "throttle":
      return "throttle";
    case "quarantine":
      return "quarantine";
    case "redact":
      return "redact";
    case "allow_with_constraints":
      return "with-constraints";
    case "error":
      return "error";
  }
}

function bucketVariant(bucket: SimulatorBucket): BadgeVariant {
  switch (bucket) {
    case "allow":
      return "healthy";
    case "deny":
      return "danger";
    case "redact":
    case "quarantine":
      return "warning";
    case "throttle":
      return "warning";
    case "require_human":
      return "info";
    case "allow_with_constraints":
      return "muted";
    case "error":
      return "danger";
  }
}
