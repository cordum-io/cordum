import { useEffect, useMemo } from "react";
import { parseAsString, parseAsStringLiteral, useQueryState } from "nuqs";
import { Plus, Search } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { Select } from "@/components/ui/Select";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { ruleTypeIcon, ruleTypeLabel } from "@/lib/policy-studio/rule-type";
import type { RuleFilters } from "@/hooks/useRulesList";

const RULE_TYPE_VALUES = Object.values(RuleType) as [RuleType, ...RuleType[]];
const RULE_STATUS_VALUES = Object.values(RuleStatus) as [RuleStatus, ...RuleStatus[]];

function ruleStatusLabel(status: RuleStatus): string {
  return status.replaceAll("_", " ");
}

interface PoliciesFilterBarProps {
  onFiltersChange?: (filters: RuleFilters) => void;
}

export function PoliciesFilterBar({ onFiltersChange }: PoliciesFilterBarProps) {
  const [type, setType] = useQueryState(
    "type",
    parseAsStringLiteral(RULE_TYPE_VALUES).withOptions({ clearOnDefault: true }),
  );
  const [status, setStatus] = useQueryState(
    "status",
    parseAsStringLiteral(RULE_STATUS_VALUES).withOptions({ clearOnDefault: true }),
  );
  const [scope, setScope] = useQueryState(
    "scope",
    parseAsString.withDefault("").withOptions({ clearOnDefault: true }),
  );
  const [search, setSearch] = useQueryState(
    "search",
    parseAsString.withDefault("").withOptions({ clearOnDefault: true }),
  );

  const filters = useMemo<RuleFilters>(() => {
    const next: RuleFilters = {};
    if (type) next.type = type;
    if (status) next.status = status;
    if (scope) next.scope = scope;
    if (search) next.search = search;
    return next;
  }, [search, scope, status, type]);

  useEffect(() => {
    onFiltersChange?.(filters);
  }, [filters, onFiltersChange]);

  const TypeIcon = type ? ruleTypeIcon(type) : null;

  return (
    <div className="flex flex-wrap items-end gap-3">
      <label className="min-w-[150px] flex-1 text-xs font-medium text-muted-foreground">
        <span className="mb-1 block">Type</span>
        <div className="relative">
          {TypeIcon && (
            <TypeIcon className="pointer-events-none absolute left-3 top-1/2 z-10 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          )}
          <Select
            aria-label="Filter rules by type"
            value={type ?? ""}
            onChange={(event) => void setType(event.target.value ? (event.target.value as RuleType) : null)}
            className={TypeIcon ? "pl-8" : undefined}
          >
            <option value="">All types</option>
            {RULE_TYPE_VALUES.map((value) => (
              <option key={value} value={value}>
                {ruleTypeLabel(value)}
              </option>
            ))}
          </Select>
        </div>
      </label>

      <label className="min-w-[150px] flex-1 text-xs font-medium text-muted-foreground">
        <span className="mb-1 block">Status</span>
        <Select
          aria-label="Filter rules by status"
          value={status ?? ""}
          onChange={(event) => void setStatus(event.target.value ? (event.target.value as RuleStatus) : null)}
        >
          <option value="">All statuses</option>
          {RULE_STATUS_VALUES.map((value) => (
            <option key={value} value={value}>
              {ruleStatusLabel(value)}
            </option>
          ))}
        </Select>
      </label>

      <label className="min-w-[180px] flex-1 text-xs font-medium text-muted-foreground">
        <span className="mb-1 block">Scope</span>
        <Input
          aria-label="Filter rules by scope"
          placeholder="tenant:acme"
          value={scope}
          onChange={(event) => void setScope(event.target.value)}
        />
      </label>

      <label className="min-w-[220px] flex-[2] text-xs font-medium text-muted-foreground">
        <span className="mb-1 block">Search</span>
        <Input
          aria-label="Search rules"
          icon={<Search className="h-3.5 w-3.5" aria-hidden />}
          placeholder="Search rules…"
          type="search"
          value={search}
          onChange={(event) => void setSearch(event.target.value)}
        />
      </label>

      <Button size="sm" disabled title="Dashboard 3 opens the rule editor">
        <Plus className="h-3.5 w-3.5" aria-hidden />
        New rule
      </Button>
    </div>
  );
}
