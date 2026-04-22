import { useEffect, useMemo, useState } from "react";
import { Input } from "@/components/ui/Input";
import { Checkbox } from "@/components/ui/Checkbox";
import { Button } from "@/components/ui/Button";
import { LabeledField } from "@/components/ui/LabeledField";
import { EmptyState } from "@/components/ui/EmptyState";
import { EntryResultRow } from "./EntryResultRow";
import type { EvalEntryResult, EvalRun, EvalRunStatus } from "@/api/types";

const ALL_STATUSES: EvalRunStatus[] = ["pass", "fail", "regression", "error"];

const VIRTUALIZATION_THRESHOLD = 200;

export function EntryResultList({ run }: { run: EvalRun }) {
  const hasRegression = (run.summary.regressions ?? 0) > 0;
  const [onlyRegressions, setOnlyRegressions] = useState(hasRegression);
  const [statuses, setStatuses] = useState<Set<EvalRunStatus>>(new Set(ALL_STATUSES));
  const [ruleFilter, setRuleFilter] = useState("");
  const [windowSize, setWindowSize] = useState(VIRTUALIZATION_THRESHOLD);

  useEffect(() => {
    setOnlyRegressions(hasRegression);
  }, [hasRegression]);

  const entries = run.entries ?? [];

  const filtered = useMemo(() => {
    return entries.filter((e) => {
      if (onlyRegressions && e.status !== "regression") return false;
      if (!statuses.has(e.status)) return false;
      if (ruleFilter && !(e.ruleId ?? "").toLowerCase().includes(ruleFilter.toLowerCase())) {
        return false;
      }
      return true;
    });
  }, [entries, onlyRegressions, statuses, ruleFilter]);

  const shouldVirtualize = filtered.length > VIRTUALIZATION_THRESHOLD;
  const visible = shouldVirtualize ? filtered.slice(0, windowSize) : filtered;

  function toggleStatus(s: EvalRunStatus) {
    setStatuses((prev) => {
      const next = new Set(prev);
      if (next.has(s)) next.delete(s);
      else next.add(s);
      return next;
    });
  }

  return (
    <div className="space-y-3">
      <div className="rounded-2xl border border-border bg-surface-1 p-3 space-y-3" data-testid="entry-filter-bar">
        <div className="flex flex-wrap items-center gap-2">
          <LabeledField label="Rule" className="flex-1 min-w-[180px]">
            <Input
              placeholder="rule-…"
              value={ruleFilter}
              onChange={(e) => setRuleFilter(e.target.value)}
            />
          </LabeledField>
          <LabeledField label="Status" className="flex-1 min-w-[240px]">
            <div className="flex flex-wrap gap-2">
              {ALL_STATUSES.map((s) => (
                <Checkbox
                  key={s}
                  checked={statuses.has(s)}
                  onChange={() => toggleStatus(s)}
                  label={s}
                />
              ))}
            </div>
          </LabeledField>
        </div>
        <Checkbox
          checked={onlyRegressions}
          onChange={(e) => setOnlyRegressions((e.target as HTMLInputElement).checked)}
          label="Only regressions"
          description={
            hasRegression
              ? "Default on when the run has regressions."
              : "No regressions in this run."
          }
        />
        <p className="text-xs text-muted-foreground">
          Showing {visible.length} of {filtered.length}
          {filtered.length !== entries.length && ` (filtered from ${entries.length})`}
        </p>
      </div>

      {filtered.length === 0 ? (
        <EmptyState
          title="No entries match your filters"
          description="Widen the status filter or clear the rule filter."
        />
      ) : (
        <div className="space-y-1" data-testid="entry-list" data-virtualized={shouldVirtualize}>
          {visible.map((e) => (
            <EntryResultRow key={e.entryId} entry={e} />
          ))}
          {shouldVirtualize && windowSize < filtered.length && (
            <div className="flex justify-center pt-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => setWindowSize((w) => w + VIRTUALIZATION_THRESHOLD)}
              >
                Show next {Math.min(VIRTUALIZATION_THRESHOLD, filtered.length - windowSize)} entries
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export const __entryListInternal = {
  VIRTUALIZATION_THRESHOLD,
};
