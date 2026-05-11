import { z } from "zod";
import { envelopeSchema } from "./envelope";

// Form-side mirror of `scripts/policy-studio/rule-payloads.mjs#output`.
// Output rules narrow DecisionType to the five outcomes meaningful for
// post-execution scanning (allow/deny/redact/quarantine/require_human);
// finding_types is a closed enum (secret_leak | pii | injection).

const stringList = z
  .array(z.string().trim().min(1).max(200))
  .max(50, "At most 50 entries")
  .optional();

const findingTypeEnum = z.enum(["secret_leak", "pii", "injection"]);

const outputDecisionTypeEnum = z.enum([
  "allow",
  "deny",
  "redact",
  "quarantine",
  "require_human",
]);

export const outputRuleFormSchema = envelopeSchema.extend({
  match: z
    .object({
      topics: stringList,
      tools: stringList,
      risk_tags: stringList,
      finding_types: z.array(findingTypeEnum).max(3, "At most 3 finding types").optional(),
    })
    .strict(),
  decide: z
    .object({
      type: outputDecisionTypeEnum,
      reason: z.string().trim().max(500, "Reason must be 500 characters or fewer").optional(),
      redact_strategy: z
        .string()
        .trim()
        .max(200, "Redact strategy must be 200 characters or fewer")
        .optional(),
    })
    .strict(),
});

export type OutputRuleFormValues = z.infer<typeof outputRuleFormSchema>;
