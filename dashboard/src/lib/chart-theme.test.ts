import { describe, it, expect } from "vitest";
import {
  chartColors,
  resolveChartColor,
  gradientId,
  gradientFill,
  axisTickStyle,
  gridProps,
  barDefaults,
  decisionLabels,
  getDecisionLabel,
} from "./chart-theme";

describe("chartColors", () => {
  it("contains all 5 safety decision colors", () => {
    expect(chartColors.allow).toBe("#1f7a57");
    expect(chartColors.deny).toBe("#7c3aed");
    expect(chartColors.require_approval).toBe("#c58a1c");
    expect(chartColors.allow_with_constraints).toBe("#0f7f7a");
    expect(chartColors.throttle).toBe("#d4833a");
  });

  it("includes cordum and muted", () => {
    expect(chartColors.cordum).toBeDefined();
    expect(chartColors.muted).toBeDefined();
  });
});

describe("resolveChartColor", () => {
  it("resolves a semantic key to hex color", () => {
    expect(resolveChartColor("allow")).toBe("#1f7a57");
    expect(resolveChartColor("deny")).toBe("#7c3aed");
  });

  it("passes through a raw hex color unchanged", () => {
    expect(resolveChartColor("#FF0000")).toBe("#FF0000");
    expect(resolveChartColor("rgb(0,0,0)")).toBe("rgb(0,0,0)");
  });

  it("returns the key itself if not in palette", () => {
    expect(resolveChartColor("unknown-key")).toBe("unknown-key");
  });
});

describe("gradientId / gradientFill", () => {
  it("produces correct gradient ID", () => {
    expect(gradientId("allowed")).toBe("grad-allowed");
    expect(gradientId("denied")).toBe("grad-denied");
  });

  it("produces correct gradient fill URL", () => {
    expect(gradientFill("allowed")).toBe("url(#grad-allowed)");
  });
});

describe("axisTickStyle", () => {
  it("has font size 10", () => {
    expect(axisTickStyle.fontSize).toBe(10);
  });

  it("includes mono font family", () => {
    expect(axisTickStyle.fontFamily).toContain("JetBrains Mono");
  });

  it("has muted fill color", () => {
    expect(axisTickStyle.fill).toBe("#5a6a70");
  });
});

describe("gridProps", () => {
  it("has dashed stroke", () => {
    expect(gridProps.strokeDasharray).toBe("3 3");
  });

  it("has low-opacity stroke", () => {
    expect(gridProps.stroke).toContain("rgba");
  });
});

describe("barDefaults", () => {
  it("has rounded top corners", () => {
    expect(barDefaults.radius).toEqual([3, 3, 0, 0]);
  });

  it("has max bar size", () => {
    expect(barDefaults.maxBarSize).toBe(32);
  });
});

describe("getDecisionLabel", () => {
  it("maps known decisions to labels", () => {
    expect(getDecisionLabel("allow")).toBe("Allow");
    expect(getDecisionLabel("deny")).toBe("Deny");
    expect(getDecisionLabel("require_approval")).toBe("Approval");
    expect(getDecisionLabel("allow_with_constraints")).toBe("Constrained");
    expect(getDecisionLabel("throttle")).toBe("Throttle");
  });

  it("returns the raw string for unknown decisions", () => {
    expect(getDecisionLabel("unknown")).toBe("unknown");
    expect(getDecisionLabel("")).toBe("");
  });
});

describe("decisionLabels", () => {
  it("has entries for all 5 decision types", () => {
    expect(Object.keys(decisionLabels)).toHaveLength(5);
    expect(decisionLabels).toHaveProperty("allow");
    expect(decisionLabels).toHaveProperty("deny");
    expect(decisionLabels).toHaveProperty("require_approval");
    expect(decisionLabels).toHaveProperty("allow_with_constraints");
    expect(decisionLabels).toHaveProperty("throttle");
  });
});
