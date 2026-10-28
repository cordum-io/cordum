import { describe, expect, it } from "vitest";
import {
  environmentSchema,
  notificationChannelSchema,
  notificationRuleSchema,
} from "./settingsSchemas";
import {
  agentTaskSchema,
  packActionSchema,
  toolCallSchema,
} from "../components/workflow/job/schemas";
import {
  fanOutSchema,
  httpSchema,
  jobSchema,
  parallelSchema,
} from "../components/workflow-studio/configSchemas";

describe("Zod migration: settings records and defaults", () => {
  it("preserves omitted config and enabled defaults", () => {
    expect(
      notificationChannelSchema.parse({ name: "Alerts", type: "email" }),
    ).toEqual({
      name: "Alerts",
      type: "email",
      config: {},
      enabled: true,
    });
    expect(environmentSchema.parse({ name: "Local" })).toEqual({
      name: "Local",
      config: {},
    });
    expect(
      notificationRuleSchema.parse({
        eventPattern: "policy.*",
        channelIds: ["alerts"],
      }),
    ).toEqual({
      eventPattern: "policy.*",
      channelIds: ["alerts"],
      enabled: true,
    });
  });

  it("preserves explicit false instead of replacing it with a default", () => {
    expect(
      notificationChannelSchema.parse({
        name: "Alerts",
        type: "email",
        enabled: false,
      }).enabled,
    ).toBe(false);
    expect(
      notificationRuleSchema.parse({
        eventPattern: "policy.*",
        channelIds: ["alerts"],
        enabled: false,
      }).enabled,
    ).toBe(false);
  });

  it("retains arbitrary string keys and unknown config values", () => {
    const config = {
      text: "value",
      count: 2,
      enabled: false,
      nested: { values: [null, "text", 3] },
      empty: null,
      unset: undefined,
    };
    const parsedConfigs = [
      notificationChannelSchema.parse({
        name: "Alerts",
        type: "webhook",
        config,
      }).config,
      environmentSchema.parse({ name: "Local", config }).config,
    ];
    for (const parsed of parsedConfigs) {
      expect(parsed).toEqual(config);
      expect(Object.hasOwn(parsed, "unset")).toBe(true);
      expect(parsed.unset).toBeUndefined();
    }
  });

  it.each([{ config: null }, { config: [] }, { config: "not an object" }])(
    "rejects a non-record config: $config",
    ({ config }) => {
      expect(
        environmentSchema.safeParse({ name: "Local", config }).success,
      ).toBe(false);
      expect(
        notificationChannelSchema.safeParse({
          name: "Alerts",
          type: "email",
          config,
        }).success,
      ).toBe(false);
    },
  );
});

describe("Zod migration: workflow numeric form input", () => {
  it("returns parsed numbers from browser string inputs", () => {
    expect(
      agentTaskSchema.parse({
        label: "Agent",
        topic: "job.agent",
        maxInputTokens: "42",
        deadlineMs: "1500",
        retryMax: "2",
      }),
    ).toEqual({
      label: "Agent",
      topic: "job.agent",
      maxInputTokens: 42,
      deadlineMs: 1500,
      retryMax: 2,
    });
    expect(
      packActionSchema.parse({
        label: "Pack",
        packId: "example",
        retryMax: "3",
      }).retryMax,
    ).toBe(3);
    expect(
      toolCallSchema.parse({
        label: "Tool",
        capability: "lookup",
        retryMax: "4",
        labels: { env: "test" },
      }),
    ).toEqual({
      label: "Tool",
      capability: "lookup",
      retryMax: 4,
      labels: { env: "test" },
    });
    expect(
      jobSchema.parse({ label: "Job", topic: "job.test", retryMax: "5" })
        .retryMax,
    ).toBe(5);
  });

  it("keeps omitted optional numeric fields absent", () => {
    expect(
      agentTaskSchema.parse({ label: "Agent", topic: "job.agent" }),
    ).toEqual({ label: "Agent", topic: "job.agent" });
    expect(
      packActionSchema.parse({ label: "Pack", packId: "example" }),
    ).toEqual({ label: "Pack", packId: "example" });
    expect(
      toolCallSchema.parse({ label: "Tool", capability: "lookup" }),
    ).toEqual({ label: "Tool", capability: "lookup" });
  });

  it.each([{ value: "" }, { value: null }])(
    "retains minimum-zero coercion for $value",
    ({ value }) => {
      expect(
        agentTaskSchema.parse({
          label: "Agent",
          topic: "job.agent",
          deadlineMs: value,
          retryMax: value,
        }),
      ).toMatchObject({ deadlineMs: 0, retryMax: 0 });
      expect(
        packActionSchema.parse({
          label: "Pack",
          packId: "example",
          retryMax: value,
        }).retryMax,
      ).toBe(0);
      expect(
        toolCallSchema.parse({
          label: "Tool",
          capability: "lookup",
          retryMax: value,
        }).retryMax,
      ).toBe(0);
    },
  );

  it.each([{ value: "" }, { value: null }])(
    "rejects minimum-one coercion for $value",
    ({ value }) => {
      expect(
        fanOutSchema.safeParse({ label: "Fan out", parallelism: value })
          .success,
      ).toBe(false);
      expect(
        parallelSchema.safeParse({
          label: "Parallel",
          parallelSteps: ["step-1"],
          completionStrategy: "n_of_m",
          requiredCount: value,
        }).success,
      ).toBe(false);
    },
  );

  it.each([{ value: "-1" }, { value: "1.5" }, { value: "not a number" }])(
    "rejects invalid retry input $value",
    ({ value }) => {
      expect(
        agentTaskSchema.safeParse({
          label: "Agent",
          topic: "job.agent",
          retryMax: value,
        }).success,
      ).toBe(false);
      expect(
        packActionSchema.safeParse({
          label: "Pack",
          packId: "example",
          retryMax: value,
        }).success,
      ).toBe(false);
      expect(
        toolCallSchema.safeParse({
          label: "Tool",
          capability: "lookup",
          retryMax: value,
        }).success,
      ).toBe(false);
    },
  );

  it.each([
    { value: Number.MAX_SAFE_INTEGER + 1 },
    { value: Infinity },
    { value: -Infinity },
    { value: NaN },
  ])("rejects unsafe or non-finite numeric values $value", ({ value }) => {
    expect(
      agentTaskSchema.safeParse({
        label: "Agent",
        topic: "job.agent",
        deadlineMs: value,
      }).success,
    ).toBe(false);
    expect(
      jobSchema.safeParse({ label: "Job", topic: "job.test", retryMax: value })
        .success,
    ).toBe(false);
  });
});

describe("Zod migration: workflow preprocessing and validation", () => {
  it("turns a single trimmed selected step into an array and parses counts", () => {
    expect(
      parallelSchema.parse({
        label: "Parallel",
        parallelSteps: " step-1 ",
        completionStrategy: "n_of_m",
        requiredCount: "1",
        parallelism: "2",
      }),
    ).toEqual({
      label: "Parallel",
      parallelSteps: ["step-1"],
      completionStrategy: "n_of_m",
      requiredCount: 1,
      parallelism: 2,
    });
  });

  it("preserves array order and existing array entry text", () => {
    expect(
      parallelSchema.parse({
        label: "Parallel",
        parallelSteps: [" step-2 ", "step-1"],
        completionStrategy: "all",
      }).parallelSteps,
    ).toEqual([" step-2 ", "step-1"]);
  });

  it.each([
    { value: undefined },
    { value: null },
    { value: "" },
    { value: "   " },
    { value: [] },
  ])("rejects an empty selected-step input $value", ({ value }) => {
    const result = parallelSchema.safeParse({
      label: "Parallel",
      parallelSteps: value,
      completionStrategy: "all",
    });
    expect(result.success).toBe(false);
    if (!result.success)
      expect(result.error.issues[0].message).toBe(
        "Select at least one child step",
      );
  });

  it.each(["javascript:alert(1)", "data:text/plain,unsafe"])(
    "retains HTTP URL scheme rejection for %s",
    (url) => {
      const result = httpSchema.safeParse({
        label: "HTTP",
        method: "GET",
        url,
      });
      expect(result.success).toBe(false);
      if (!result.success)
        expect(result.error.issues[0].message).toBe("Invalid URL scheme");
    },
  );
});
