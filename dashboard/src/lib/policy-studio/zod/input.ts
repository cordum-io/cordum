import { z } from "zod";
import { envelopeSchema } from "./envelope";

// Form-side mirror of `scripts/policy-studio/rule-payloads.mjs#input`.
// The form view binds string-array fields as comma-separated input that
// is parsed/normalized at the form boundary, so the Zod arrays accept
// already-split string arrays here. Empty arrays are dropped on conversion
// to NormalizedRule so the persisted match payload stays minimal.

const stringList = z
  .array(z.string().trim().min(1).max(200))
  .max(50, "At most 50 entries")
  .optional();

const decisionTypeEnum = z.enum([
  "allow",
  "deny",
  "require_human",
  "throttle",
  "allow_with_constraints",
  "quarantine",
  "redact",
]);

export const inputRuleFormSchema = envelopeSchema.extend({
  match: z
    .object({
      topics: stringList,
      tools: stringList,
      risk_tags: stringList,
      content_pattern: z
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
        ),
    })
    .strict(),
  decide: z
    .object({
      type: decisionTypeEnum,
      reason: z.string().trim().max(500, "Reason must be 500 characters or fewer").optional(),
    })
    .strict(),
});

export type InputRuleFormValues = z.infer<typeof inputRuleFormSchema>;
