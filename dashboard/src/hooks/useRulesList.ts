import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import { queryKeys } from "../lib/queryKeys";
import type { Rule } from "@/api/generated/model/rule";
import type { RuleStatus } from "@/api/generated/model/ruleStatus";
import type { RuleType } from "@/api/generated/model/ruleType";

export interface RuleFilters {
  type?: RuleType;
  scope?: string;
  status?: RuleStatus;
  search?: string;
  cursor?: string;
  limit?: number;
}

export interface RulesListResult {
  rules: Rule[];
  total: number;
  nextCursor?: string;
}

interface BackendRulesListResponse {
  items?: Rule[];
  rules?: Rule[];
  total?: number;
  next_cursor?: string;
}

function rulesListPath(filters: RuleFilters): string {
  const params = new URLSearchParams();
  if (filters.type) params.set("type", filters.type);
  if (filters.scope) params.set("scope", filters.scope);
  if (filters.status) params.set("status", filters.status);
  if (filters.search) params.set("search", filters.search);
  if (filters.cursor) params.set("cursor", filters.cursor);
  if (filters.limit !== undefined) params.set("limit", String(filters.limit));
  const qs = params.toString();
  return `/policy/rules${qs ? `?${qs}` : ""}`;
}

function normalizeRulesList(response: BackendRulesListResponse): RulesListResult {
  const rules = response.rules ?? response.items ?? [];
  return {
    rules,
    total: response.total ?? rules.length,
    nextCursor: response.next_cursor,
  };
}

export function useRulesList(filters: RuleFilters = {}) {
  return useQuery<RulesListResult>({
    queryKey: queryKeys.policyStudioRules.list(filters),
    queryFn: async () => normalizeRulesList(await get<BackendRulesListResponse>(rulesListPath(filters))),
    staleTime: 30_000,
  });
}
