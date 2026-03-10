import { describe, expect, it } from "vitest";
import { ApiError } from "@/api/client";
import { createEmptyGlobalInputRule } from "@/components/policy/global/GlobalRuleEditorDrawer";
import { __policyStudioGlobalInternal } from "./usePolicyStudioGlobal";

describe("usePolicyStudioGlobal internals", () => {
  it("normalizes CRLF YAML text consistently", () => {
    const input = "rules:\r\n  - id: rule-1\r\n";
    expect(__policyStudioGlobalInternal.normalizeYaml(input)).toBe("rules:\n  - id: rule-1");
  });

  it("maps 409 API errors to conflict classification", () => {
    const error = new ApiError(409, "conflict", { message: "etag mismatch" });
    const mapped = __policyStudioGlobalInternal.mapPolicyStudioError(error, "save");
    expect(mapped.code).toBe("conflict");
    expect(mapped.details).toContain("etag mismatch");
  });

  it("maps 400 API errors to yaml_validation classification", () => {
    const error = new ApiError(400, "bad request", { error: "invalid yaml" });
    const mapped = __policyStudioGlobalInternal.mapPolicyStudioError(error, "save");
    expect(mapped.code).toBe("yaml_validation");
    expect(mapped.details).toContain("invalid yaml");
  });

  it("maps simulate validation failures with simulation-specific guidance", () => {
    const error = new ApiError(422, "invalid simulation request", {
      error: "topic is required",
    });
    const mapped = __policyStudioGlobalInternal.mapPolicyStudioError(error, "simulate");
    expect(mapped.code).toBe("yaml_validation");
    expect(mapped.message).toContain("Simulation request failed validation");
    expect(mapped.details).toContain("topic is required");
  });

  it("summarizes parse issues with line context", () => {
    const summary = __policyStudioGlobalInternal.describeParseIssues([
      { severity: "error", path: "$.rules[0]", message: "bad key", line: 4, column: 7 },
    ]);
    expect(summary).toContain("line 4, col 7");
    expect(summary).toContain("bad key");
  });

  it("maps unknown errors with action context", () => {
    const mapped = __policyStudioGlobalInternal.mapPolicyStudioError(
      new Error("boom"),
      "simulate",
    );
    expect(mapped.code).toBe("unknown");
    expect(mapped.message).toContain("running simulation");
  });

  it("maps load failures to request_failed classification", () => {
    const mapped = __policyStudioGlobalInternal.mapLoadError(
      new ApiError(503, "unavailable", { message: "bundle endpoint unavailable" }),
    );
    expect(mapped.code).toBe("request_failed");
    expect(mapped.status).toBe(503);
    expect(mapped.details).toContain("bundle endpoint unavailable");
  });

  it("reorders input rules deterministically", () => {
    const rule1 = createEmptyGlobalInputRule(1);
    const rule2 = createEmptyGlobalInputRule(2);
    const rule3 = createEmptyGlobalInputRule(3);
    rule1.id = "r1";
    rule2.id = "r2";
    rule3.id = "r3";

    const moved = __policyStudioGlobalInternal.moveRule(
      [rule1, rule2, rule3],
      0,
      2,
    );
    expect(moved.map((rule) => rule.id)).toEqual(["r2", "r3", "r1"]);
  });
});
