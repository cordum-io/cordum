import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { ApiError, get } from "../api/client";
import { queryKeys } from "../lib/queryKeys";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import {
  normalizeRule,
  type NormalizedRule,
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

function isNotFoundError(err: unknown): boolean {
  return err instanceof ApiError && err.status === 404;
}

interface UseRuleQueryArgs {
  id: string | undefined;
  // When `id === NEW_RULE_ID`, this seeds the in-memory draft and the hook
  // does not hit the backend. Otherwise it's ignored.
  createType?: RuleType;
}

/**
 * Loads a single Rule by id from `/policy/rules/{id}`. The endpoint shape
 * mirrors useRulesList — accept `unknown`, run through the same normalizer,
 * and return null for unsalvageable rows so the drawer can render the
 * not-found state explicitly.
 */
export function useRule({
  id,
  createType,
}: UseRuleQueryArgs): UseQueryResult<NormalizedRule | null> {
  const isCreateNew = id === NEW_RULE_ID;
  const enabled = typeof id === "string" && id.length > 0;
  return useQuery<NormalizedRule | null>({
    queryKey: queryKeys.policyStudioRules.detail(id ?? ""),
    queryFn: async () => {
      if (isCreateNew) {
        return createType ? emptyDraftRule(createType) : null;
      }
      if (!id) return null;
      try {
        const raw = await get<unknown>(`/policy/rules/${encodeURIComponent(id)}`);
        return normalizeRule(raw);
      } catch (err) {
        // 404 is an expected outcome for an unknown rule id. The drawer
        // distinguishes it from network/5xx errors by returning data=null
        // here — only true backend failures bubble up to the error state.
        if (isNotFoundError(err)) return null;
        throw err;
      }
    },
    enabled,
    // Detail pages are usually opened deliberately; a 60s stale window keeps
    // navigation fast without serving wildly stale payloads to the editor.
    staleTime: 60_000,
  });
}

export type RuleEditorTypeOrUnknown = RuleTypeOrUnknown;
