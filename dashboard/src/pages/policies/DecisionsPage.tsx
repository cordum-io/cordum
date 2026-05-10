import { lazy, Suspense, useCallback, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import type { ColumnDef } from "@tanstack/react-table";
import { History } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { EmptyState } from "@/components/ui/EmptyState";
import { Button } from "@/components/ui/Button";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import {
  DataTable,
  type DecisionTier,
} from "@/components/primitives/DataTable";
import { useDecisionsList } from "@/hooks/useDecisionsList";
import { useDecisionsStream } from "@/hooks/useDecisionsStream";
import { decisionTone, type DecisionTone } from "@/lib/policy-studio/decision-tone";
import { formatRelativeTime } from "@/lib/utils";
import { DecisionType } from "@/api/generated/model/decisionType";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import type { Decision } from "@/api/generated/model/decision";
import {
  DecisionsFilterBar,
  useDecisionsFilterValues,
  useDecisionsLiveMode,
  useDecisionsChartsToggle,
} from "./DecisionsFilterBar";
import { DecisionExpandRow } from "./DecisionExpandRow";

// Recharts is heavyweight — lazy-load so the eager /policies/decisions
// chunk stays small (epic rail "Recharts must be in a separate Vite
// chunk; not in the eager /policies/decisions chunk").
const DecisionsChartsPanel = lazy(() =>
  import("./DecisionsChartsPanel").then((mod) => ({
    default: mod.DecisionsChartsPanel,
  })),
);

interface DecisionRow {
  decision: Decision;
  id: string;
  timestamp: string;
  type: DecisionType;
  ruleId: string;
  bundleLabel: string;
  source: DecisionSource;
  target: string;
}

const TONE_TO_BADGE: Record<DecisionTone, BadgeVariant> = {
  success: "healthy",
  warning: "warning",
  danger: "danger",
  info: "info",
  neutral: "muted",
};

const TONE_TO_TIER: Record<DecisionTone, DecisionTier> = {
  success: "allow",
  warning: "throttle",
  danger: "deny",
  info: "allow_with_constraints",
  neutral: "allow_with_constraints",
};

function bundleLabel(d: Decision): string {
  if (!d.bundle_id) return "—";
  return d.bundle_version ? `${d.bundle_id}:${d.bundle_version}` : d.bundle_id;
}

function decisionRowKey(d: Decision, index: number): string {
  // Decisions don't carry a stable id in the unified shape; use
  // (timestamp + rule_id + audit_hash + index) so re-renders preserve
  // row identity for virtualization without colliding on synthetic data.
  return `${d.timestamp}|${d.rule_id}|${d.audit_hash ?? ""}|${index}`;
}

function toRow(d: Decision, index: number): DecisionRow {
  return {
    decision: d,
    id: decisionRowKey(d, index),
    timestamp: d.timestamp,
    type: d.type,
    ruleId: d.rule_id,
    bundleLabel: bundleLabel(d),
    source: d.source,
    target: d.input_ref ?? "—",
  };
}

/**
 * Policy Studio — Decisions surface (D8a + D8b, epic-d9a6c0a1).
 *
 * D8a (commit 2f67b3ee): page shell + filter bar + paged table.
 * D8b (this commit): expand-row inline (Trace + Input + Bundle context +
 * Actions); useDecisionsStream with WS auto-fallback to polling for live
 * mode; cursor-paginated "Load more"; Charts toggle (URL state only;
 * panel content lands in D9b / task-e343469b).
 */
export default function DecisionsPage() {
  const filters = useDecisionsFilterValues();
  const [live] = useDecisionsLiveMode();
  const [chartsOn] = useDecisionsChartsToggle();
  const [expandedIds, setExpandedIds] = useState<ReadonlySet<string>>(
    () => new Set(),
  );

  const list = useDecisionsList(filters);
  const stream = useDecisionsStream(
    { source: filters.source, type: filters.type },
    live,
  );

  // When live mode is active, the ring-buffer is the source of truth.
  // Otherwise paginated history is. Switching back to history-mode
  // resumes from cursor-page-1; live-mode does not advance the cursor.
  const decisions = live && stream.mode !== "closed" ? stream.decisions : list.items;

  const rows = useMemo<DecisionRow[]>(
    () => decisions.map(toRow),
    [decisions],
  );

  const toggleExpanded = useCallback((row: DecisionRow) => {
    setExpandedIds((prev) => {
      const next = new Set(prev);
      if (next.has(row.id)) next.delete(row.id);
      else next.add(row.id);
      return next;
    });
  }, []);

  const columns = useMemo<ColumnDef<DecisionRow, unknown>[]>(
    () => [
      {
        accessorKey: "timestamp",
        header: "Time",
        cell: ({ row }) => (
          <span title={row.original.timestamp} className="font-mono text-xs text-muted-foreground">
            {formatRelativeTime(row.original.timestamp)}
          </span>
        ),
      },
      {
        accessorKey: "type",
        header: "Decision",
        cell: ({ row }) => {
          const tone = decisionTone(row.original.type);
          return (
            <StatusBadge variant={TONE_TO_BADGE[tone]}>
              {row.original.type}
            </StatusBadge>
          );
        },
      },
      {
        accessorKey: "ruleId",
        header: "Rule",
        cell: ({ row }) => (
          // D10a cross-link contract: clicking the rule cell deep-links
          // to the rule editor pre-opened on this rule.
          <Link
            to={`/policies?rule=${encodeURIComponent(row.original.ruleId)}&open=editor`}
            className="font-mono text-xs text-foreground hover:text-cordum"
            data-row-action="cross-link-decisions-rule"
            aria-label={`Open rule ${row.original.ruleId} in editor`}
          >
            {row.original.ruleId}
          </Link>
        ),
      },
      {
        accessorKey: "bundleLabel",
        header: "Bundle:Version",
        cell: ({ row }) => {
          const bundleId = row.original.decision.bundle_id;
          if (!bundleId) return <span className="text-muted-foreground">—</span>;
          return (
            <Link
              to={`/policies/bundles/${encodeURIComponent(bundleId)}?tab=versions${row.original.decision.bundle_version ? `&v=${encodeURIComponent(row.original.decision.bundle_version)}` : ""}`}
              className="font-mono text-xs text-foreground hover:text-cordum"
              data-row-action="cross-link-decisions-bundle"
              aria-label={`Open bundle ${row.original.bundleLabel}`}
            >
              {row.original.bundleLabel}
            </Link>
          );
        },
      },
      {
        accessorKey: "source",
        header: "Source",
        cell: ({ row }) => (
          // Source badge differentiates job vs edge per spec § Decisions
          // (DoD #3). Backend 5b's read-side default-fill ensures `source`
          // is always non-empty.
          <StatusBadge
            variant={row.original.source === DecisionSource.job ? "info" : "warning"}
          >
            {row.original.source}
          </StatusBadge>
        ),
      },
      {
        accessorKey: "target",
        header: "Target",
        cell: ({ row }) => (
          <span className="font-mono text-xs text-muted-foreground">
            {row.original.target === "—" ? row.original.target : row.original.target.slice(0, 24)}
          </span>
        ),
      },
    ],
    [],
  );

  if (list.isError) {
    return (
      <div className="space-y-6">
        <PageHeader
          label="Policy Studio"
          title="Policy Decisions"
          subtitle="Live stream of policy outcomes"
        />
        <EmptyState
          icon={<History className="h-5 w-5" />}
          title="Couldn't load decisions"
          description="The decisions endpoint returned an error. Try again or adjust the filters."
        />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        label="Policy Studio"
        title="Policy Decisions"
        subtitle="Live stream of policy outcomes"
      />

      <DecisionsFilterBar
        totalCount={rows.length}
        onRefresh={() => void list.refetch()}
        isFetching={list.isFetching}
        liveMode={live ? stream.mode : "closed"}
      />

      {chartsOn && (
        <Suspense
          fallback={
            <div
              data-testid="decisions-charts-loading"
              className="rounded-2xl border border-dashed border-border/60 bg-surface-1 p-4 text-xs italic text-muted-foreground"
            >
              Loading charts...
            </div>
          }
        >
          <DecisionsChartsPanel decisions={decisions} />
        </Suspense>
      )}

      <DataTable
        columns={columns}
        data={rows}
        decisionAccessor={(row) => TONE_TO_TIER[decisionTone(row.type)]}
        onRowClick={toggleExpanded}
        renderExpanded={(row) => <DecisionExpandRow decision={row.decision} />}
        expandedIds={expandedIds}
        getRowId={(row) => row.id}
        emptyState={
          <EmptyState
            icon={<History className="h-5 w-5" />}
            title="No decisions match these filters"
            description="Adjust the time range or decision/source filters to see more decisions."
          />
        }
      />

      {/* Cursor pagination: only relevant in history mode (not live).
       * Live ring-buffer is bounded at 200 events; user flips Live off to
       * paginate older history. Hiding the button in live mode keeps the
       * UX honest. */}
      {!live && list.hasMore && (
        <div className="flex justify-center">
          <Button
            variant="outline"
            size="sm"
            onClick={() => void list.fetchNextPage()}
            loading={list.isFetchingNextPage}
            aria-label="Load more decisions"
          >
            Load more
          </Button>
        </div>
      )}
    </div>
  );
}
