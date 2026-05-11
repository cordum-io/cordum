import { z } from "zod";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";

// Envelope (id/name/type/scope/status/version/audit) is the part of a Rule
// shared across all four authoring types. The form view exposes the
// edit-meaningful subset (name, description, scope, status); id/version/
// audit/type are managed by the page or pinned by the per-type schema.
//
// `value` rules: required when kind is non-global; the global case keeps
// `value` optional+empty so a draft round-trips cleanly through the
// canonical Rule envelope.

export const scopeKindEnum = z.enum([
  RuleScopeKind.global,
  RuleScopeKind.tenant,
  RuleScopeKind.workflow,
  RuleScopeKind.edge_fleet,
  RuleScopeKind.edge_user,
]);

export const statusEnum = z.enum([RuleStatus.draft, RuleStatus.published, RuleStatus.deprecated]);

export const envelopeSchema = z
  .object({
    name: z.string().trim().min(1, "Name is required").max(200, "Name must be 200 characters or fewer"),
    description: z.string().trim().max(1000, "Description must be 1000 characters or fewer").optional(),
    scope: z
      .object({
        kind: scopeKindEnum,
        value: z.string().trim().max(200, "Scope value must be 200 characters or fewer").optional(),
      })
      .superRefine((scope, ctx) => {
        if (scope.kind !== RuleScopeKind.global) {
          if (!scope.value || scope.value.length === 0) {
            ctx.addIssue({
              code: z.ZodIssueCode.custom,
              path: ["value"],
              message: `Scope "${scope.kind}" requires a value (e.g. tenant id, workflow id).`,
            });
          }
        }
      }),
    status: statusEnum,
  })
  .strict();

export type EnvelopeFormValues = z.infer<typeof envelopeSchema>;
