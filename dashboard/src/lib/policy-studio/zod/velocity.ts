import { z } from "zod";
import { envelopeSchema } from "./envelope";

// Form-side mirror of `scripts/policy-studio/rule-payloads.mjs#velocity`.
// Velocity rules always throttle; the form requires at least one of
// max_per_{minute,hour,day} so the throttle has a measurable trigger.

const stringList = z
  .array(z.string().trim().min(1).max(200))
  .max(50, "At most 50 entries")
  .optional();

const intMin1 = z
  .number({ invalid_type_error: "Enter a positive integer" })
  .int("Must be a whole number")
  .min(1, "Must be at least 1")
  .optional();

export const velocityRuleFormSchema = envelopeSchema.extend({
  match: z
    .object({
      tenants: stringList,
      topics: stringList,
      risk_tags: stringList,
    })
    .strict(),
  decide: z
    .object({
      type: z.literal("throttle"),
      max_per_minute: intMin1,
      max_per_hour: intMin1,
      max_per_day: intMin1,
      burst_limit: intMin1,
      reason: z.string().trim().max(500, "Reason must be 500 characters or fewer").optional(),
    })
    .strict()
    .superRefine((decide, ctx) => {
      if (
        decide.max_per_minute === undefined &&
        decide.max_per_hour === undefined &&
        decide.max_per_day === undefined
      ) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["max_per_minute"],
          message: "Set at least one of max_per_minute, max_per_hour, or max_per_day.",
        });
      }
    }),
});

export type VelocityRuleFormValues = z.infer<typeof velocityRuleFormSchema>;
