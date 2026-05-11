import { useQuery } from "@tanstack/react-query";
import { listPolicyRules } from "@/api/generated/policy/policy";
import type { Rule } from "@/api/generated/model/rule";
import { logger } from "@/lib/logger";

export interface UseRuleAtVersionResult {
  rule: Rule | null;
  loading: boolean;
  error: Error | null;
}

const STALE_MS = 60_000;

// Fetches the rule that fired this Decision so the What-if drawer can
// show + edit its YAML. Backend 5d's GET /api/v1/policy/rules does NOT
// yet accept a `version` query parameter (the plan's fast-follow), so
// when bundleVersion is provided we issue the same list call and warn
// once that we're returning the latest rather than the historical
// snapshot. Filter is client-side on rule.id since the list endpoint
// does not yet expose `id` as a server-side filter either.
export function useRuleAtVersion(
  ruleID: string | null | undefined,
  bundleVersion?: string,
): UseRuleAtVersionResult {
  const query = useQuery<{ items?: Rule[] } | { items: Rule[] }>({
    queryKey: ["policy-studio-rule-at-version", ruleID ?? "", bundleVersion ?? ""],
    queryFn: async ({ signal }) => {
      if (bundleVersion) {
        logger.warn(
          "decisions-whatif",
          "rule-version filter unsupported; returning latest rule",
          { rule_id: ruleID, bundle_version: bundleVersion },
        );
      }
      return listPolicyRules({ limit: 500 }, signal) as Promise<{ items?: Rule[] }>;
    },
    enabled: Boolean(ruleID),
    staleTime: STALE_MS,
  });

  const rule = (() => {
    if (!ruleID) return null;
    const items = (query.data && "items" in query.data && Array.isArray(query.data.items))
      ? query.data.items
      : [];
    return items.find((r) => r.id === ruleID) ?? null;
  })();

  return {
    rule,
    loading: query.isPending && Boolean(ruleID),
    error: query.error as Error | null,
  };
}
