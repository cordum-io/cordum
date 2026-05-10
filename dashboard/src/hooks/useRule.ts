import {
  useQuery,
  useQueryClient,
  type QueryClient,
  type UseQueryResult,
} from "@tanstack/react-query";
import { get } from "../api/client";
import { queryKeys } from "../lib/queryKeys";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import {
  normalizeRule,
  type NormalizedRule,
  type RulesListResult,
  UNKNOWN_RULE_TYPE,
  type RuleTypeOrUnknown,
} from "./useRulesList";

// Sentinel id used by the create-new path: PoliciesPage links to
// `/policies?rule=new&open=editor&type=<RuleType>` for "+ New rule", and the
// drawer fabricates a draft NormalizedRule in memory rather than hitting the
// backend. The drawer treats this as a true draft until the first save.
export const NEW_RULE_ID = "new" as const;

// Subset of RuleType values the create-new path accepts. Excludes the
// UNKNOWN_RULE_TYPE sentinel — we never let an author start a rule from
// "unknown"; they must pick a real type. The exhaustiveness guard below
// keeps this list aligned with generated RuleType.
const CREATE_NEW_TYPES = new Set<RuleType>([
  RuleType.input,
  RuleType.output,
  RuleType.velocity,
  RuleType.edge,
]);

interface BackendRulesListResponse {
  items?: unknown;
  rules?: unknown;
  total?: number;
}

/**
 * Returns a fresh draft NormalizedRule for the create-new path. The
 * generated audit timestamps are intentionally empty — they are populated
 * server-side on first save. RuleScopeKind.global is the safest default;
 * authors can change scope inside the form view before saving.
 */
export function emptyDraftRule(type: RuleType): NormalizedRule {
  return {
    id: "",
    name: "",
    type,
    scope: { kind: RuleScopeKind.global },
    status: RuleStatus.draft,
    version: "v1",
    audit: { created_at: "", created_by: "" },
    match: {},
    decide: { type: "allow" },
  };
}

/**
 * Type guard for the create-new path's `type` query param. Accepts only
 * canonical generated RuleType values; unknown / missing inputs return
 * undefined so the drawer can show a "pick a rule type" empty state.
 */
export function parseCreateNewType(raw: string | null): RuleType | undefined {
  if (!raw) return undefined;
  if ((CREATE_NEW_TYPES as Set<string>).has(raw)) {
    return raw as RuleType;
  }
  return undefined;
}

/**
 * Reports whether the resolved rule's type is the safe Unknown fallback.
 * Drawer callers use this to render a defensive "rule type not recognized"
 * state instead of attempting to mount the Monaco/Form editors against a
 * schema we don't have.
 */
export function ruleHasKnownType(
  rule: Pick<NormalizedRule, "type">,
): rule is NormalizedRule & { type: RuleType } {
  return rule.type !== UNKNOWN_RULE_TYPE;
}

/**
 * Searches every cached `useRulesList` query for a rule with the given id.
 * The drawer is normally opened from a list-row click, so the rule is
 * already in cache and no extra network request is needed. Exported for
 * tests that want to seed the cache and assert cache-first behavior.
 */
export function findRuleInListCaches(
  queryClient: QueryClient,
  id: string,
): NormalizedRule | null {
  // The umbrella `policyStudioRules.all()` key matches BOTH list and detail
  // query keys (`['policy-studio-rules', 'list', ...]` and
  // `['policy-studio-rules', 'detail', id]`). Detail data is a single
  // NormalizedRule and has no `.rules` array, so we must filter to list
  // queries only — a permissive `data.rules.find` here crashes once a
  // rule has been opened (QA reopen #2 finding 2026-05-10).
  const cached = queryClient.getQueriesData<RulesListResult>({
    queryKey: queryKeys.policyStudioRules.all(),
  });
  for (const [key, data] of cached) {
    if (!data) continue;
    if (!Array.isArray(key) || key[1] !== "list") continue;
    const rules = (data as RulesListResult).rules;
    if (!Array.isArray(rules)) continue;
    const found = rules.find((rule) => rule.id === id);
    if (found) return found;
  }
  return null;
}

/**
 * Fallback for direct URL navigation to `/policies?rule=<id>&open=editor`
 * when the list cache hasn't been populated yet. The current dashboard/core
 * contract exposes only the list endpoint (cordum-api.yaml:2609 +
 * gateway.go:1415 register only GET `/api/v1/policy/rules`); there is no
 * single-rule detail route. So we fetch the list, normalize each row, and
 * pick out the requested id. Returns null when the rule is not in the list
 * (deleted / renamed / unknown id).
 */
async function fetchRuleFromListEndpoint(id: string): Promise<NormalizedRule | null> {
  const raw = await get<BackendRulesListResponse>("/policy/rules");
  const items = Array.isArray(raw.rules)
    ? raw.rules
    : Array.isArray(raw.items)
      ? raw.items
      : [];
  for (const row of items) {
    const rule = normalizeRule(row);
    if (rule && rule.id === id) return rule;
  }
  return null;
}

interface UseRuleQueryArgs {
  id: string | undefined;
  // When `id === NEW_RULE_ID`, this seeds the in-memory draft and the hook
  // does not hit the backend. Otherwise it's ignored.
  createType?: RuleType;
}

/**
 * Resolves a single Rule by id. There is no `/api/v1/policy/rules/{id}`
 * route in the current dashboard/core contract; instead the drawer reads
 * from the React Query list cache (populated by `useRulesList`) and falls
 * back to the list endpoint when the cache is cold. Returns null for
 * unsalvageable rows so the drawer can render a not-found state explicitly
 * — only true network/backend failures bubble up to the error state.
 */
export function useRule({
  id,
  createType,
}: UseRuleQueryArgs): UseQueryResult<NormalizedRule | null> {
  const queryClient = useQueryClient();
  const isCreateNew = id === NEW_RULE_ID;
  const enabled = typeof id === "string" && id.length > 0;
  return useQuery<NormalizedRule | null>({
    queryKey: queryKeys.policyStudioRules.detail(id ?? ""),
    queryFn: async () => {
      if (isCreateNew) {
        return createType ? emptyDraftRule(createType) : null;
      }
      if (!id) return null;
      const fromCache = findRuleInListCaches(queryClient, id);
      if (fromCache) return fromCache;
      return fetchRuleFromListEndpoint(id);
    },
    enabled,
    // Detail pages are usually opened deliberately; a 60s stale window keeps
    // navigation fast without serving wildly stale payloads to the editor.
    staleTime: 60_000,
  });
}

export type RuleEditorTypeOrUnknown = RuleTypeOrUnknown;
