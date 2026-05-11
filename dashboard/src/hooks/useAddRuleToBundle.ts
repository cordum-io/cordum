import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError, post } from "@/api/client";
import { logger } from "@/lib/logger";
import { queryKeys } from "@/lib/queryKeys";
import { useToastStore } from "@/state/toast";
import type { Bundle } from "@/api/generated/model/bundle";

export interface AddRuleToBundleInput {
  bundleId: string;
  ruleId: string;
}

export type AddRuleToBundleResult =
  | { ok: true; bundle: Bundle }
  | {
      ok: false;
      kind: "bundle_not_found" | "rule_not_found" | "validation" | "permission" | "network" | "unknown";
      error: string;
    };

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

// classifyAddRuleError disambiguates the two 404 paths via the body's
// {error: "bundle_not_found" | "rule_not_found"} field shipped by
// Backend 5c writeAddRuleToBundleError so the dashboard can present
// the right copy without sniffing the URL or status alone.
function classifyAddRuleError(err: ApiError): AddRuleToBundleResult {
  if (err.status === 404 && isPlainObject(err.body)) {
    const body = err.body as Record<string, unknown>;
    if (body.error === "bundle_not_found") {
      return { ok: false, kind: "bundle_not_found", error: err.message };
    }
    if (body.error === "rule_not_found") {
      return { ok: false, kind: "rule_not_found", error: err.message };
    }
  }
  if (err.status === 400) {
    return { ok: false, kind: "validation", error: err.message };
  }
  if (err.status === 401 || err.status === 403) {
    return { ok: false, kind: "permission", error: err.message };
  }
  if (err.status === 0 || err.status === 408) {
    return { ok: false, kind: "network", error: err.message };
  }
  return { ok: false, kind: "unknown", error: err.message };
}

/**
 * Bind an existing Rule into a Bundle's `rule_ids` set. Calls
 * `POST /api/v1/policy/bundles/{id}/rules` (Backend 5c). The endpoint
 * is idempotent — repeating with the same `rule_id` returns 200 with
 * the unchanged Bundle. Concurrent binds with distinct `rule_id`s
 * converge under Lua CAS without lost writes.
 *
 * 404 disambiguation: the response body includes
 * `error: "bundle_not_found"` vs `error: "rule_not_found"` so the
 * dashboard can present the right copy. We surface the discriminant
 * as a typed `kind` field on the result.
 */
export function useAddRuleToBundle() {
  const queryClient = useQueryClient();

  return useMutation<AddRuleToBundleResult, never, AddRuleToBundleInput>({
    mutationKey: ["policy-studio-rules", "add-to-bundle"],
    mutationFn: async ({ bundleId, ruleId }) => {
      try {
        const bundle = await post<Bundle>(
          `/policy/bundles/${encodeURIComponent(bundleId)}/rules`,
          { rule_id: ruleId },
        );
        return { ok: true, bundle };
      } catch (err) {
        if (err instanceof ApiError) {
          const classified = classifyAddRuleError(err);
          logger.warn("policy-studio-editor", "add-rule-to-bundle rejected", {
            bundleId,
            ruleId,
            status: err.status,
            kind: classified.ok ? "ok" : classified.kind,
          });
          return classified;
        }
        logger.error("policy-studio-editor", "add-rule-to-bundle threw non-ApiError", {
          bundleId,
          ruleId,
          error: err instanceof Error ? err.message : String(err),
        });
        return {
          ok: false,
          kind: "unknown",
          error: err instanceof Error ? err.message : "Add to bundle failed",
        };
      }
    },
    onSuccess: (result, { bundleId, ruleId }) => {
      if (!result.ok) return; // toast handled by the modal so it can show typed copy
      // Prefix-match invalidation — covers detail + list + versions
      // queries for every filter variant in one call.
      void queryClient.invalidateQueries({
        queryKey: queryKeys.bundleStudio.all(),
      });
      useToastStore.getState().addToast({
        type: "success",
        title: "Added to bundle",
        description: `${ruleId} is now part of ${bundleId}.`,
      });
    },
  });
}
