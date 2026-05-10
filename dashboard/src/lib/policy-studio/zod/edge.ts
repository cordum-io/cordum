import { z } from "zod";
import { envelopeSchema } from "./envelope";

// Form-side mirror of `scripts/policy-studio/rule-payloads.mjs#edge`.
// Edge rules narrow DecisionType to the four outcomes that map to
// hook-time decisions (allow/deny/require_human/allow_with_constraints).

const stringList = z
  .array(z.string().trim().min(1).max(200))
  .max(50, "At most 50 entries")
  .optional();

const edgeDecisionTypeEnum = z.enum([
  "allow",
  "deny",
  "require_human",
  "allow_with_constraints",
]);

const optionalRegex = z
  .string()
  .trim()
  .max(1000, "Pattern must be 1000 characters or fewer")
  .optional()
  .refine(
    (value) => {
      if (!value) return true;
      try {
        // eslint-disable-next-line no-new
        new RegExp(value);
        return true;
      } catch {
        return false;
      }
    },
    { message: "Pattern must be a valid regular expression" },
  );

export const edgeRuleFormSchema = envelopeSchema.extend({
  match: z
    .object({
      tools: stringList,
      command_pattern: optionalRegex,
      path_pattern: optionalRegex,
      risk_tags: stringList,
    })
    .strict(),
  decide: z
    .object({
      type: edgeDecisionTypeEnum,
      reason: z.string().trim().max(500, "Reason must be 500 characters or fewer").optional(),
    })
    .strict(),
});

export type EdgeRuleFormValues = z.infer<typeof edgeRuleFormSchema>;
