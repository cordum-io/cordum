import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router-dom";
import type { ColumnDef } from "@tanstack/react-table";
import { GripVertical, MoreHorizontal, Plus, Shield, Globe, Users, Zap, Package, TrendingUp } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { DataTable, type DecisionTier } from "@/components/primitives/DataTable";
import { RuleFiringSparkline } from "@/components/charts/RuleFiringSparkline";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { Tabs } from "@/components/ui/Tabs";
import { PoliciesFilterBar } from "./PoliciesFilterBar";
import { PoliciesEmptyTemplatesGallery } from "./PoliciesEmptyTemplatesGallery";
import { RuleEditorDrawer } from "./RuleEditorDrawer";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
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
  decision: string;
  updatedAt: string;
  last7dSeries: number[] | null;
}

const ACTION_VARIANT: Record<string, BadgeVariant> = {
  allow: "healthy",
  deny: "danger",
  require_human: "warning",
  throttle: "warning",
  allow_with_constraints: "warning",
  quarantine: "warning",
  redact: "muted",
};

const STATUS_VARIANT: Record<RuleStatus, BadgeVariant> = {
  [RuleStatus.draft]: "muted",
  [RuleStatus.published]: "healthy",
  [RuleStatus.deprecated]: "warning",
};

const ACTION_DECISION_TIER: Record<string, DecisionTier> = {
  allow: "allow",
  deny: "deny",
  require_human: "require_approval",
  throttle: "throttle",
  allow_with_constraints: "allow_with_constraints",
  quarantine: "throttle",
  redact: "allow_with_constraints",
};

const POLICY_STUDIO_TABS = [
  { id: "rules", label: "Rules", icon: <Shield className="h-4 w-4" /> },
  { id: "bundles", label: "Bundles", icon: <Package className="h-4 w-4" /> },
  { id: "decisions", label: "Decisions", icon: <TrendingUp className="h-4 w-4" /> },
];

function actionVariant(action: string): BadgeVariant {
  return ACTION_VARIANT[action] ?? "muted";
}

function statusVariant(status: RuleStatus): BadgeVariant {
  return STATUS_VARIANT[status] ?? "muted";
}

function actionDecisionTier(action: string): DecisionTier {
  return ACTION_DECISION_TIER[action] ?? "allow";
}

function scopeLabel(rule: NormalizedRule): string {
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
  const decision = String(rule.decide?.type || "allow");
  return {
    rule,
    id: rule.id,
    name: rule.name,
    typeLabel: ruleTypeLabel(rule.type),
    scopeLabel: scopeLabel(rule),
    status: rule.status,
    decision,
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
      aria-label="Rule actions"
      data-row-action
      disabled
      size="icon"
      title="Rule actions coming in follow-up"
      variant="ghost"
    >
      <MoreHorizontal className="h-4 w-4" aria-hidden />
    </Button>
  );
}

interface TierSectionProps {
  title: string;
  description: string;
  icon: ReactNode;
  rows: RuleRow[];
  columns: ColumnDef<RuleRow, unknown>[];
  isPending: boolean;
  emptyState: ReactNode;
}

function TierSection({ title, description, icon, rows, columns, isPending, emptyState }: TierSectionProps) {
  if (!isPending && rows.length === 0) return null;

  return (
    <div className="space-y-4">
      <div className="flex items-start gap-3 px-1">
        <div className="mt-0.5 rounded-lg bg-surface-2 p-1.5 text-muted-foreground shadow-sm ring-1 ring-border">
          {icon}
        </div>
        <div>
          <h3 className="text-sm font-bold tracking-tight text-foreground">{title}</h3>
          <p className="text-xs text-muted-foreground/80 leading-relaxed">{description}</p>
        </div>
      </div>
      <div className="overflow-hidden rounded-2xl border border-border bg-surface-1/40 shadow-sm transition-all hover:bg-surface-1/60">
        <DataTable<RuleRow>
          columns={columns}
          data={rows}
          decisionAccessor={(row) => actionDecisionTier(row.decision)}
          emptyState={emptyState}
          compact
        />
      </div>
    </div>
  );
}

export default function PoliciesPage() {
  const navigate = useNavigate();
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

  const allRows = useMemo(
    () => (data?.rules ?? []).map(toRuleRow),
    [data?.rules],
  );

  const globalRows = useMemo(
    () => allRows.filter((r) => r.rule.scope?.kind === RuleScopeKind.global),
    [allRows],
  );
  const tenantRows = useMemo(
    () => allRows.filter((r) => r.rule.scope?.kind === RuleScopeKind.tenant),
    [allRows],
  );
  const specificRows = useMemo(
    () =>
      allRows.filter(
        (r) =>
          r.rule.scope?.kind !== RuleScopeKind.global &&
          r.rule.scope?.kind !== RuleScopeKind.tenant,
      ),
    [allRows],
  );

  const filtersActive = Object.keys(rawFilters).length > 0;

  const columns = useMemo<ColumnDef<RuleRow, unknown>[]>(
    () => [
      {
        id: "priority",
        header: "#",
        cell: ({ row }) => (
          <div className="flex items-center gap-2 text-muted-foreground font-mono text-[10px]">
            <GripVertical className="h-3 w-3 opacity-20" />
            <span>{(row.index + 1).toString().padStart(2, "0")}</span>
          </div>
        ),
        size: 50,
      },
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
        accessorKey: "decision",
        header: "Decision",
        cell: ({ row }) => (
          <StatusBadge variant={actionVariant(row.original.decision)}>
            {row.original.decision}
          </StatusBadge>
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
        accessorKey: "scopeLabel",
        header: "Scope",
        cell: ({ row }) => (
          <span className="font-mono text-[10px] text-muted-foreground uppercase tracking-tight">
            {row.original.scopeLabel}
          </span>
        ),
      },
      {
        accessorKey: "last7dSeries",
        header: "Traffic (7d)",
        cell: ({ row }) => {
          if (row.original.last7dSeries === null) {
            return <span className="text-muted-foreground">—</span>;
          }
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
        header: "",
        cell: () => <RuleActions />,
        enableSorting: false,
        size: 40,
        meta: { align: "right" },
      },
    ],
    [],
  );

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
    <div className="space-y-6 pb-20">
      <PageHeader
        label="Govern \u00b7 Policy Studio"
        title="Policy Studio"
        subtitle="Author and manage unified firewall rules across cloud + edge"
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

      <Tabs
        tabs={POLICY_STUDIO_TABS}
        activeTab="rules"
        onChange={(id) => navigate(id === "rules" ? "/policies" : `/policies/${id}`)}
        variant="segmented"
        className="w-fit"
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
      ) : allRows.length === 0 ? (
        emptyState
      ) : (
        <div className="space-y-12">
          <TierSection
            title="Global Policies"
            description="Evaluated first for all traffic across cloud jobs and edge agents. Highest precedence."
            icon={<Globe className="h-4 w-4" />}
            rows={globalRows}
            columns={columns}
            isPending={isPending}
            emptyState={emptyState}
          />
          <TierSection
            title="Tenant Policies"
            description="Evaluated after Global rules for specific tenant contexts."
            icon={<Users className="h-4 w-4" />}
            rows={tenantRows}
            columns={columns}
            isPending={isPending}
            emptyState={emptyState}
          />
          <TierSection
            title="Specific & Edge Policies"
            description="Targeted rules for individual Workflows, Edge Fleets, or Users. Evaluated last."
            icon={<Zap className="h-4 w-4" />}
            rows={specificRows}
            columns={columns}
            isPending={isPending}
            emptyState={emptyState}
          />
        </div>
      )}
    </div>
  );
}
