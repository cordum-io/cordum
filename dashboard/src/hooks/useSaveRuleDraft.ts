import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError, apiClient } from "@/api/client";
import { logger } from "@/lib/logger";
import { queryKeys } from "@/lib/queryKeys";
import { useToastStore } from "@/state/toast";
import type { Rule } from "@/api/generated/model/rule";
import type { NormalizedRule } from "./useRulesList";
import { ruleHasKnownType } from "./useRule";

// Discriminated input. Drawer chooses create-vs-update based on whether
// the original loaded rule was a real server row (update) or a fresh
// draft (create). The `ifMatch` value is the original-loaded version,
// not the version currently in the form — the form's version may be
// stale by the time the user clicks Save.
export type SaveRuleDraftInput =
  | { mode: "create"; rule: NormalizedRule }
  | { mode: "update"; rule: NormalizedRule; ifMatch: string };

export type SaveRuleDraftResult =
  | { ok: true; rule: Rule }
  | {
      ok: false;
      kind: "stale";
      currentVersion: string;
      currentAuditHash: string;
    }
  | {
      ok: false;
      kind: "validation" | "permission" | "network" | "unknown";
      error: string;
    };

// toUnifiedRule strips the NormalizedRule extensions (UNKNOWN_RULE_TYPE
// sentinel + firing_last_7d) and rejects unknown-type rules — the
// server's Rule.Validate() would 400 anyway, but failing fast in the
// hook gives the drawer a precise typed error without a roundtrip. The
// version+audit fields are server-managed: stripped on create, sent
// untouched on update so the server can compare audit hashes.
function toUnifiedRule(input: NormalizedRule, mode: "create" | "update"): Rule | null {
  if (!ruleHasKnownType(input)) return null;
  if (mode === "create") {
    // The server owns version + audit on create; sending them gets a
    // 400 per Backend 5c rejectClientManagedFieldsOnCreate. Strip both.
    return {
      id: input.id,
      name: input.name,
      type: input.type,
      scope: input.scope,
      status: input.status,
      version: "",
      audit: { created_at: "", created_by: "" },
      match: input.match,
      decide: input.decide,
      ...(input.description !== undefined ? { description: input.description } : {}),
    };
  }
  // Update: send everything; the server rewrites version + audit on
  // every successful PUT. Path id wins over body id, so a renamed body
  // doesn't rename the rule.
  return {
    id: input.id,
    name: input.name,
    type: input.type,
    scope: input.scope,
    status: input.status,
    version: input.version,
    audit: input.audit,
    match: input.match,
    decide: input.decide,
    ...(input.description !== undefined ? { description: input.description } : {}),
  };
}

// classifyApiError maps an ApiError into a SaveRuleDraftResult error
// branch. Stale-version detection is the load-bearing path for D3E
// DoD #3 — the drawer renders a reload banner from
// `currentVersion + currentAuditHash` instead of overwriting newer
// server state with the user's stale draft.
function classifyApiError(err: ApiError): SaveRuleDraftResult {
  if (err.status === 409 && isPlainObject(err.body)) {
    const body = err.body as Record<string, unknown>;
    if (body.error === "stale_version") {
      return {
        ok: false,
        kind: "stale",
        currentVersion: typeof body.current_version === "string" ? body.current_version : "",
        currentAuditHash:
          typeof body.current_audit_hash === "string" ? body.current_audit_hash : "",
      };
    }
    // 409 on duplicate id (create path). Treat as validation so the
    // drawer can highlight the id field rather than a generic banner.
    return { ok: false, kind: "validation", error: err.message };
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

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

export interface UseSaveRuleDraftReturn {
  isPending: boolean;
  mutateAsync: (input: SaveRuleDraftInput) => Promise<SaveRuleDraftResult>;
}

/**
 * Phase 3E save-draft mutation. POST /policy/rules creates a new Rule
 * (server sets version=v1 + audit + status=draft); PUT /policy/rules/{id}
 * updates an existing Rule with mandatory `If-Match: <version>`
 * optimistic concurrency. Stale-version 409s surface as a typed result
 * so the drawer renders a reload banner without overwriting newer
 * server state. Backend 5c contract: cordum/docs/api/policy-rules-write.md.
 *
 * Routes through `apiClient` directly rather than the orval-generated
 * `updatePolicyRule(id, rule)` because orval doesn't auto-emit header
 * parameters and the PUT requires `If-Match`. Backend 5d follow-up may
 * replace this with a generated header-aware hook.
 */
export function useSaveRuleDraft(): UseSaveRuleDraftReturn {
  const queryClient = useQueryClient();

  const mutation = useMutation<SaveRuleDraftResult, never, SaveRuleDraftInput>({
    mutationKey: ["policy-studio-rules", "save-draft"],
    mutationFn: async (input) => {
      const wireRule = toUnifiedRule(input.rule, input.mode);
      if (!wireRule) {
        return {
          ok: false,
          kind: "validation",
          error: "Rule type is not recognized. Pick a valid type before saving.",
        };
      }
      try {
        if (input.mode === "create") {
          const persisted = await apiClient<Rule>({
            url: "/api/v1/policy/rules",
            method: "POST",
            data: wireRule,
          });
          return { ok: true, rule: persisted };
        }
        const persisted = await apiClient<Rule>({
          url: `/api/v1/policy/rules/${encodeURIComponent(wireRule.id)}`,
          method: "PUT",
          headers: { "If-Match": input.ifMatch },
          data: wireRule,
        });
        return { ok: true, rule: persisted };
      } catch (err) {
        if (err instanceof ApiError) {
          const classified = classifyApiError(err);
          logger.warn("policy-studio-editor", "save-draft rejected", {
            mode: input.mode,
            ruleId: wireRule.id,
            status: err.status,
            kind: classified.ok ? "ok" : classified.kind,
          });
          return classified;
        }
        // Non-ApiError (programming bug, etc.) — log + surface as unknown.
        logger.error("policy-studio-editor", "save-draft threw non-ApiError", {
          mode: input.mode,
          ruleId: wireRule.id,
          error: err instanceof Error ? err.message : String(err),
        });
        return {
          ok: false,
          kind: "unknown",
          error: err instanceof Error ? err.message : "Save failed",
        };
      }
    },
    onSuccess: (result, input) => {
      if (!result.ok) {
        // 409-stale or typed error — toast handled by the drawer so the
        // reload banner can take user attention. No success invalidation.
        return;
      }
      void queryClient.invalidateQueries({
        queryKey: queryKeys.policyStudioRules.all(),
      });
      // Pre-warm the detail cache so the drawer's useRule sees the v(N+1)
      // envelope without a roundtrip.
      queryClient.setQueryData(
        queryKeys.policyStudioRules.detail(result.rule.id),
        result.rule,
      );
      useToastStore.getState().addToast({
        type: "success",
        title: input.mode === "create" ? "Rule created" : "Rule saved",
        description: `Now at version ${result.rule.version}`,
      });
    },
  });

  return {
    isPending: mutation.isPending,
    mutateAsync: mutation.mutateAsync,
  };
}
