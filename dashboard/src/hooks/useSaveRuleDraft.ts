import { useCallback, useState } from "react";
import { logger } from "@/lib/logger";
import type { NormalizedRule } from "./useRulesList";

// Result of a save-draft attempt. The drawer renders a status banner from
// `kind` so authors know whether their work landed; we do NOT throw on
// known failure shapes (network, validation) because the editor needs to
// stay open with the in-flight rule intact.
export type SaveRuleDraftResult =
  | { ok: true; rule: NormalizedRule }
  | { ok: false; error: string };

/**
 * Phase 3A boundary hook for the Save-draft button. The backend mutation
 * for the unified Rule envelope (`POST /policy/rules` / `PUT
 * /policy/rules/{id}`) is not yet shipped — only `upsertOutputRule` and
 * `updatePolicyGlobal` exist on the generated client. Rather than route
 * a unified-shape draft through a legacy mutation (which would silently
 * lose the type/scope/version envelope) or fake a save, this hook returns
 * `isAvailable: false`. The drawer renders the Save button disabled with
 * a tooltip pointing at Phase 3E.
 *
 * When the backend lands the unified-rule write endpoint, change
 * `BACKEND_DRAFT_AVAILABLE` to `true` and wire the generated mutation
 * (e.g. `useCreatePolicyRule` / `useUpsertPolicyRule`) without changing
 * the consumer contract here.
 */
const BACKEND_DRAFT_AVAILABLE = false;

export interface UseSaveRuleDraftReturn {
  /**
   * Whether the unified-rule draft endpoint is available. Consumers MUST
   * gate the button's `onClick` on this — calling `mutateAsync` when
   * `isAvailable === false` returns a typed error result without making a
   * network call.
   */
  isAvailable: boolean;
  isPending: boolean;
  mutateAsync: (rule: NormalizedRule) => Promise<SaveRuleDraftResult>;
}

export function useSaveRuleDraft(): UseSaveRuleDraftReturn {
  const [isPending, setPending] = useState(false);

  const mutateAsync = useCallback(
    async (rule: NormalizedRule): Promise<SaveRuleDraftResult> => {
      if (!BACKEND_DRAFT_AVAILABLE) {
        // The Save button is supposed to be disabled when isAvailable is
        // false; this is a defense-in-depth guard for direct hook callers
        // (tests, future programmatic use) so a click never silently no-ops.
        const error =
          "Save draft is not enabled in Phase 3A. The unified-rule draft endpoint is part of Phase 3E.";
        logger.warn("policy-studio-editor", "save draft attempted while disabled", {
          ruleId: rule.id,
          ruleType: rule.type,
        });
        return { ok: false, error };
      }
      // Branch reserved for when the backend mutation lands. Keeping the
      // body here (instead of throwing) lets a future PR swap the gate
      // to true and the network call in without changing the call site.
      setPending(true);
      try {
        // Placeholder — the real implementation will call into
        // `src/api/generated/policy/policy.ts` once the upsert hook ships.
        return { ok: false, error: "Save draft endpoint pending wire-up." };
      } finally {
        setPending(false);
      }
    },
    [],
  );

  return {
    isAvailable: BACKEND_DRAFT_AVAILABLE,
    isPending,
    mutateAsync,
  };
}
