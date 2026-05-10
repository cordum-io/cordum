import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import type { ColumnDef } from "@tanstack/react-table";
import { MoreHorizontal, Plus, Shield } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { DataTable, type DecisionTier } from "@/components/primitives/DataTable";
import { RuleFiringSparkline } from "@/components/charts/RuleFiringSparkline";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { PoliciesFilterBar } from "./PoliciesFilterBar";
import { PoliciesEmptyTemplatesGallery } from "./PoliciesEmptyTemplatesGallery";
import { RuleEditorDrawer } from "./RuleEditorDrawer";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { formatRelativeTime } from "@/lib/utils";
import { ruleTypeIcon, ruleTypeLabel } from "@/lib/policy-studio/rule-type";
import {
  useRulesList,
  type NormalizedRule,
  type RuleFilters,
} from "@/hooks/useRulesList";

interface RuleRow {
  rule: NormalizedRule;
  id: string;
  name: string;
  typeLabel: string;
  scopeLabel: string;
  status: RuleStatus;
  updatedAt: string;
  last7dSeries: number[] | null;
}

const STATUS_VARIANT: Record<RuleStatus, BadgeVariant> = {
  [RuleStatus.draft]: "muted",
  [RuleStatus.published]: "healthy",
  [RuleStatus.deprecated]: "warning",
};

const STATUS_DECISION_TIER: Record<RuleStatus, DecisionTier> = {
  [RuleStatus.draft]: "allow_with_constraints",
  [RuleStatus.published]: "allow",
  [RuleStatus.deprecated]: "throttle",
};

// Defensive lookups — `useRulesList` already normalizes status into the
// generated RuleStatus enum, but indexing a Record on a runtime-cast value
// risks an undefined render if a future caller bypasses the hook. Fall back
// to the neutral "draft" treatment so the page never crashes.
function statusVariant(status: RuleStatus): BadgeVariant {
  return STATUS_VARIANT[status] ?? STATUS_VARIANT[RuleStatus.draft];
}

function statusDecisionTier(status: RuleStatus): DecisionTier {
  return STATUS_DECISION_TIER[status] ?? STATUS_DECISION_TIER[RuleStatus.draft];
}

function scopeLabel(rule: NormalizedRule): string {
  // No direct `rule.scope.kind` assumptions — guard against any residual
  // malformed normalized rows. Preserves task-fd25f310 comment-beeedc8e.
  const scope = rule.scope;
  if (!scope || typeof scope.kind !== "string") return "global";
  if (scope.kind === "global") return "global";
  return scope.value ? `${scope.kind}:${scope.value}` : scope.kind;
}

function readLast7dSeries(rule: NormalizedRule): number[] | null {
  const series = rule.firing_last_7d;
  if (!Array.isArray(series)) return null;
  const numeric = series.filter((value): value is number => typeof value === "number");
  return numeric.length === series.length ? numeric : null;
}

function toRuleRow(rule: NormalizedRule): RuleRow {
  const updatedAt = rule.audit?.updated_at || rule.audit?.created_at || "";
  return {
    rule,
    id: rule.id,
    name: rule.name,
    typeLabel: ruleTypeLabel(rule.type),
    scopeLabel: scopeLabel(rule),
    status: rule.status,
    updatedAt,
    last7dSeries: readLast7dSeries(rule),
  };
}

function RuleTypeCell({ rule }: { rule: NormalizedRule }) {
  const Icon = ruleTypeIcon(rule.type);
  return (
    <span className="inline-flex items-center gap-2">
      <Icon
        aria-hidden
        className="h-3.5 w-3.5 text-muted-foreground"
        data-testid={`rule-type-icon-${rule.type}`}
      />
      <span>{ruleTypeLabel(rule.type)}</span>
    </span>
  );
}

function RuleActions() {
  return (
    <Button
      aria-label="Rule actions coming in Dashboard 3"
      data-row-action
      disabled
      size="icon"
      title="Dashboard 3 wires edit, duplicate, and archive"
      variant="ghost"
    >
      <MoreHorizontal className="h-4 w-4" aria-hidden />
    </Button>
  );
}

export default function PoliciesPage() {
  const [rawFilters, setRawFilters] = useState<RuleFilters>({});
  const [queryFilters, setQueryFilters] = useState<RuleFilters>({});
  const hasSyncedFilters = useRef(false);
  const previousSearch = useRef<string | undefined>(undefined);
  const { data, isError, isPending } = useRulesList(queryFilters);

  useEffect(() => {
    const searchChanged =
      hasSyncedFilters.current && rawFilters.search !== previousSearch.current;
    previousSearch.current = rawFilters.search;

    if (!hasSyncedFilters.current) {
      hasSyncedFilters.current = true;
      setQueryFilters(rawFilters);
      return;
    }

    if (!searchChanged) {
      setQueryFilters(rawFilters);
      return;
    }

    const timeoutId = window.setTimeout(() => {
      setQueryFilters(rawFilters);
    }, 300);
    return () => window.clearTimeout(timeoutId);
  }, [rawFilters]);

  const rows = useMemo(
    () => (data?.rules ?? []).map(toRuleRow),
    [data?.rules],
  );
  const filtersActive = Object.keys(rawFilters).length > 0;

  const columns = useMemo<ColumnDef<RuleRow, unknown>[]>(
    () => [
      {
        accessorKey: "name",
        header: "Name",
        cell: ({ row }) => (
          <Link
            className="font-medium text-foreground hover:text-cordum"
            data-row-action
            to={`/policies?rule=${encodeURIComponent(row.original.id)}&open=editor`}
          >
            {row.original.name}
          </Link>
        ),
      },
      {
        accessorKey: "typeLabel",
        header: "Type",
        cell: ({ row }) => <RuleTypeCell rule={row.original.rule} />,
      },
      {
        accessorKey: "scopeLabel",
        header: "Scope",
        cell: ({ row }) => (
          <span className="font-mono text-xs text-muted-foreground">
            {row.original.scopeLabel}
          </span>
        ),
      },
      {
        accessorKey: "status",
        header: "Status",
        cell: ({ row }) => (
          <StatusBadge variant={statusVariant(row.original.status)}>
            {row.original.status}
          </StatusBadge>
        ),
      },
      {
        accessorKey: "last7dSeries",
        header: "Last 7d",
        cell: ({ row }) => {
          if (row.original.last7dSeries === null) {
            return <span className="text-muted-foreground">—</span>;
          }
          // Cross-link (D10a #1): clicking the sparkline deep-links to the
          // Decisions surface filtered to this rule. The /policies/decisions
          // route is a Dashboard 1 stub today; the URL contract is canonical
          // (rule= filter) and routes correctly once D8 ships its filter bar.
          // Total firings supplies the tooltip + accessible name.
          const total = row.original.last7dSeries.reduce(
            (sum, n) => sum + (Number.isFinite(n) ? n : 0),
            0,
          );
          return (
            <Link
              to={`/policies/decisions?rule=${encodeURIComponent(row.original.id)}`}
              className="inline-flex items-center justify-end rounded-md transition-colors hover:bg-surface-2/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cordum/40"
              title={`View ${total} decisions for ${row.original.name}`}
              aria-label={`View decisions: ${total} firings (last 7d)`}
              data-row-action="cross-link-decisions"
            >
              <RuleFiringSparkline values={row.original.last7dSeries} />
            </Link>
          );
        },
        meta: { align: "right" },
      },
      {
        accessorKey: "updatedAt",
        header: "Updated",
        cell: ({ row }) =>
          row.original.updatedAt ? formatRelativeTime(row.original.updatedAt) : "—",
      },
      {
        id: "actions",
        header: "Actions",
        cell: () => <RuleActions />,
        enableSorting: false,
        meta: { align: "right" },
      },
    ],
    [],
  );

  // Truly-empty rules list (no rows, no filters) renders the templates
  // gallery as the empty-state CTA. Filtered-empty (filters active + zero
  // matches) falls back to the existing "No rules match these filters"
  // copy without the gallery — surfacing it there would mislead authors
  // into thinking template-creation clears their active filters (it
  // doesn't).
  const emptyState = filtersActive ? (
    <EmptyState
      icon={<Shield className="h-5 w-5" />}
      title="No rules match these filters"
      description="Clear filters or adjust the search term to see more rules."
    />
  ) : (
    <div className="space-y-4">
      <EmptyState
        icon={<Shield className="h-5 w-5" />}
        title="No rules yet"
        description="Create the first unified job or edge rule from a template, or start from a blank rule via + New rule."
      />
      <PoliciesEmptyTemplatesGallery />
    </div>
  );

  return (
    <div className="space-y-6">
      <PageHeader
        label="Policy Studio"
        title="Policy Rules"
        subtitle="Author and manage rules across job + edge surfaces"
        actions={
          <Link
            to={`/policies?rule=new&open=editor&type=${RuleType.input}`}
            className="inline-flex items-center gap-1.5 rounded-xl bg-cordum px-3 py-1.5 text-sm font-medium text-white shadow-sm transition-colors hover:bg-cordum/90"
            data-row-action
          >
            <Plus aria-hidden className="h-3.5 w-3.5" />
            New rule
          </Link>
        }
      />

      <PoliciesFilterBar onFiltersChange={setRawFilters} />
      <RuleEditorDrawer />

      {isError ? (
        <EmptyState
          icon={<Shield className="h-5 w-5" />}
          title="Couldn't load rules"
          description="The rules list endpoint returned an error. Refresh or try again once the Policy Studio backend settles."
        />
      ) : isPending ? (
        <EmptyState
          icon={<Shield className="h-5 w-5" />}
          title="Loading rules…"
          description="Fetching from /api/v1/policy/rules."
        />
      ) : (
        <DataTable<RuleRow>
          columns={columns}
          data={rows}
          decisionAccessor={(row) => statusDecisionTier(row.status)}
          emptyState={emptyState}
          initialSorting={[{ id: "name", desc: false }]}
          virtualizedHeight={520}
        />
      )}
    </div>
  );
}
