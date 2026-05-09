import { describe, it, expect } from "vitest";
import {
  workerStatusVariant,
  jobStatusVariant,
  evalScoreVariant,
  decisionVariant,
} from "./badgeVariants";

describe("workerStatusVariant", () => {
  it("maps online + active -> success", () => {
    expect(workerStatusVariant("online")).toBe("success");
    expect(workerStatusVariant("active")).toBe("success");
  });
  it("maps draining -> warning", () => {
    expect(workerStatusVariant("draining")).toBe("warning");
  });
  it("maps offline + error -> danger", () => {
    expect(workerStatusVariant("offline")).toBe("danger");
    expect(workerStatusVariant("error")).toBe("danger");
  });
  it("falls back to default for unknown / nullish", () => {
    expect(workerStatusVariant("garbage")).toBe("default");
    expect(workerStatusVariant("")).toBe("default");
    expect(workerStatusVariant(undefined)).toBe("default");
    expect(workerStatusVariant(null)).toBe("default");
  });
});

describe("jobStatusVariant", () => {
  it("maps succeeded -> success", () => {
    expect(jobStatusVariant("succeeded")).toBe("success");
  });
  it("maps running + dispatched -> info (in-flight)", () => {
    expect(jobStatusVariant("running")).toBe("info");
    expect(jobStatusVariant("dispatched")).toBe("info");
  });
  it("maps failed + timeout -> danger", () => {
    expect(jobStatusVariant("failed")).toBe("danger");
    expect(jobStatusVariant("timeout")).toBe("danger");
  });
  it("maps denied -> governance (policy-blocked)", () => {
    expect(jobStatusVariant("denied")).toBe("governance");
  });
  it("maps pending + approval_required + output_quarantined -> warning", () => {
    expect(jobStatusVariant("pending")).toBe("warning");
    expect(jobStatusVariant("approval_required")).toBe("warning");
    expect(jobStatusVariant("output_quarantined")).toBe("warning");
  });
  it("falls back to default for unknown / nullish", () => {
    expect(jobStatusVariant("garbage")).toBe("default");
    expect(jobStatusVariant(undefined)).toBe("default");
  });
});

describe("evalScoreVariant", () => {
  it("returns default for null / undefined", () => {
    expect(evalScoreVariant(null)).toBe("default");
    expect(evalScoreVariant(undefined)).toBe("default");
  });
  it("returns success at >=95 threshold", () => {
    expect(evalScoreVariant(95)).toBe("success");
    expect(evalScoreVariant(100)).toBe("success");
  });
  it("returns warning between 80 and 94.99", () => {
    expect(evalScoreVariant(80)).toBe("warning");
    expect(evalScoreVariant(94.9)).toBe("warning");
  });
  it("returns danger below 80", () => {
    expect(evalScoreVariant(79.9)).toBe("danger");
    expect(evalScoreVariant(0)).toBe("danger");
  });
});

describe("decisionVariant (case-insensitive)", () => {
  it("maps allow + safety_allow -> success (UPPERCASE and lowercase)", () => {
    expect(decisionVariant("allow")).toBe("success");
    expect(decisionVariant("ALLOW")).toBe("success");
    expect(decisionVariant("safety_allow")).toBe("success");
    expect(decisionVariant("SAFETY_ALLOW")).toBe("success");
  });
  it("maps deny + safety_deny -> governance", () => {
    expect(decisionVariant("deny")).toBe("governance");
    expect(decisionVariant("DENY")).toBe("governance");
    expect(decisionVariant("safety_deny")).toBe("governance");
  });
  it("maps require_approval + safety_require_approval -> warning", () => {
    expect(decisionVariant("require_approval")).toBe("warning");
    expect(decisionVariant("REQUIRE_APPROVAL")).toBe("warning");
    expect(decisionVariant("safety_require_approval")).toBe("warning");
  });
  it("maps throttle + safety_throttle -> info", () => {
    expect(decisionVariant("throttle")).toBe("info");
    expect(decisionVariant("safety_throttle")).toBe("info");
  });
  it("maps constrain + allow_with_constraints -> info", () => {
    expect(decisionVariant("constrain")).toBe("info");
    expect(decisionVariant("CONSTRAIN")).toBe("info");
    expect(decisionVariant("allow_with_constraints")).toBe("info");
  });
  it("maps evaluate -> info", () => {
    expect(decisionVariant("evaluate")).toBe("info");
  });
  it("maps redact -> warning", () => {
    expect(decisionVariant("redact")).toBe("warning");
  });
  it("maps pending + recorded -> info", () => {
    expect(decisionVariant("pending")).toBe("info");
    expect(decisionVariant("PENDING")).toBe("info");
    expect(decisionVariant("recorded")).toBe("info");
  });
  it("falls back to default for unknown / null / empty", () => {
    expect(decisionVariant("garbage")).toBe("default");
    expect(decisionVariant("")).toBe("default");
    expect(decisionVariant(null)).toBe("default");
    expect(decisionVariant(undefined)).toBe("default");
  });
});
