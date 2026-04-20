import { describe, it, expect } from "vitest";
import {
  notificationChannelSchema,
  notificationRuleSchema,
  environmentSchema,
  generalConfigSchema,
} from "./settingsSchemas";

describe("notificationChannelSchema", () => {
  it("accepts valid channel", () => {
    const result = notificationChannelSchema.safeParse({
      name: "Slack alerts",
      type: "slack",
      config: { webhook: "https://hooks.slack.com/..." },
      enabled: true,
    });
    expect(result.success).toBe(true);
  });

  it("rejects empty name", () => {
    const result = notificationChannelSchema.safeParse({ name: "", type: "email" });
    expect(result.success).toBe(false);
  });

  it("rejects invalid type", () => {
    const result = notificationChannelSchema.safeParse({ name: "test", type: "sms" });
    expect(result.success).toBe(false);
  });
});

describe("notificationRuleSchema", () => {
  it("accepts valid rule", () => {
    const result = notificationRuleSchema.safeParse({
      eventPattern: "policy.*",
      channelIds: ["ch-1"],
      enabled: true,
    });
    expect(result.success).toBe(true);
  });

  it("rejects empty channelIds", () => {
    const result = notificationRuleSchema.safeParse({
      eventPattern: "policy.*",
      channelIds: [],
    });
    expect(result.success).toBe(false);
  });

  it("rejects empty event pattern", () => {
    const result = notificationRuleSchema.safeParse({
      eventPattern: "",
      channelIds: ["ch-1"],
    });
    expect(result.success).toBe(false);
  });
});

describe("environmentSchema", () => {
  it("accepts valid environment", () => {
    const result = environmentSchema.safeParse({
      name: "staging",
      endpoint: "https://staging.example.com",
    });
    expect(result.success).toBe(true);
  });

  it("accepts empty endpoint string", () => {
    const result = environmentSchema.safeParse({ name: "local", endpoint: "" });
    expect(result.success).toBe(true);
  });

  it("rejects invalid URL", () => {
    const result = environmentSchema.safeParse({ name: "bad", endpoint: "not-a-url" });
    expect(result.success).toBe(false);
  });

  it("rejects empty name", () => {
    const result = environmentSchema.safeParse({ name: "" });
    expect(result.success).toBe(false);
  });
});

describe("generalConfigSchema", () => {
  const validConfig = {
    safetyStance: "balanced" as const,
    approvalTimeoutMs: 600_000,
    autoDenyOnTimeout: false,
    logRetentionDays: 30,
    auditRetentionDays: 90,
    dlqRetentionDays: 14,
    rateLimitPerKey: 100,
    concurrentJobsLimit: 50,
    wsConnectionsLimit: 100,
    maintenanceMode: false,
  };

  it("accepts valid config", () => {
    expect(generalConfigSchema.safeParse(validConfig).success).toBe(true);
  });

  it("rejects approval timeout below 5 minutes", () => {
    const result = generalConfigSchema.safeParse({ ...validConfig, approvalTimeoutMs: 1000 });
    expect(result.success).toBe(false);
  });

  it("rejects log retention below 7 days", () => {
    const result = generalConfigSchema.safeParse({ ...validConfig, logRetentionDays: 1 });
    expect(result.success).toBe(false);
  });

  it("rejects invalid safety stance", () => {
    const result = generalConfigSchema.safeParse({ ...validConfig, safetyStance: "yolo" });
    expect(result.success).toBe(false);
  });
});
