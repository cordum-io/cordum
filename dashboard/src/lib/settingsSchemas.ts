import { z } from "zod";

// ---------------------------------------------------------------------------
// Notification Channel
// ---------------------------------------------------------------------------

export const notificationChannelSchema = z.object({
  name: z.string().min(1, "Name is required"),
  type: z.enum(["email", "slack", "webhook", "pagerduty"]),
  config: z.record(z.unknown()).default({}),
  enabled: z.boolean().default(true),
});

export type NotificationChannelForm = z.infer<typeof notificationChannelSchema>;

// ---------------------------------------------------------------------------
// Notification Rule
// ---------------------------------------------------------------------------

export const notificationRuleSchema = z.object({
  eventPattern: z.string().min(1, "Event pattern is required"),
  channelIds: z.array(z.string()).min(1, "At least one channel is required"),
  throttleMs: z.number().min(0).optional(),
  muteUntil: z.string().optional(),
  enabled: z.boolean().default(true),
});

export type NotificationRuleForm = z.infer<typeof notificationRuleSchema>;

// ---------------------------------------------------------------------------
// Environment
// ---------------------------------------------------------------------------

export const environmentSchema = z.object({
  name: z.string().min(1, "Name is required"),
  endpoint: z.string().url("Must be a valid URL").optional().or(z.literal("")),
  config: z.record(z.unknown()).default({}),
});

export type EnvironmentForm = z.infer<typeof environmentSchema>;

// ---------------------------------------------------------------------------
// General Config
// ---------------------------------------------------------------------------

export const generalConfigSchema = z.object({
  safetyStance: z.enum(["permissive", "balanced", "strict"]),
  approvalTimeoutMs: z.number().min(300_000, "Min 5 minutes").max(3_600_000, "Max 1 hour"),
  autoDenyOnTimeout: z.boolean(),
  logRetentionDays: z.number().min(7, "Min 7 days").max(365, "Max 365 days"),
  auditRetentionDays: z.number().min(7, "Min 7 days").max(365, "Max 365 days"),
  dlqRetentionDays: z.number().min(1, "Min 1 day").max(90, "Max 90 days"),
  rateLimitPerKey: z.number().min(1).max(10_000, "Max 10,000 req/min"),
  concurrentJobsLimit: z.number().min(1).max(1_000, "Max 1,000"),
  wsConnectionsLimit: z.number().min(1).max(500, "Max 500"),
  maintenanceMode: z.boolean(),
  maintenanceMessage: z.string().optional(),
});

export type GeneralConfigForm = z.infer<typeof generalConfigSchema>;
