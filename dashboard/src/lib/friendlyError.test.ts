import { describe, it, expect, vi } from "vitest";
import { ApiError } from "@/api/client";
import { friendlyError } from "./friendlyError";

vi.mock("./logger", () => ({
  logger: { error: vi.fn(), warn: vi.fn(), info: vi.fn(), debug: vi.fn() },
}));

describe("friendlyError", () => {
  it("maps 400 to input validation message", () => {
    const result = friendlyError(new ApiError(400, "Bad Request"), "test");
    expect(result.title).toBe("Invalid request");
    expect(result.description).toContain("Check your input");
  });

  it("maps 401 to session expired with login action", () => {
    const result = friendlyError(new ApiError(401, "Unauthorized"), "test");
    expect(result.title).toBe("Session expired");
    expect(result.action?.href).toBe("/login");
  });

  it("maps 403 to permission denied", () => {
    const result = friendlyError(new ApiError(403, "Forbidden"), "test");
    expect(result.title).toBe("Permission denied");
  });

  it("maps 404 to not found", () => {
    const result = friendlyError(new ApiError(404, "Not Found"), "test");
    expect(result.title).toBe("Not found");
  });

  it("maps 429 to rate limit", () => {
    const result = friendlyError(new ApiError(429, "Too Many"), "test");
    expect(result.title).toBe("Too many requests");
    expect(result.description).toContain("Wait");
  });

  it("maps 500 to server error with health action", () => {
    const result = friendlyError(new ApiError(500, "Internal"), "test");
    expect(result.title).toBe("Server error");
    expect(result.action?.href).toBe("/settings/health");
  });

  it("maps 502/503/504 to service unavailable with health action", () => {
    for (const status of [502, 503, 504]) {
      const result = friendlyError(new ApiError(status, "Unavailable"), "test");
      expect(result.action?.href).toBe("/settings/health");
    }
  });

  it("uses structured error code from body when available", () => {
    const err = new ApiError(400, "Conflict", { error: "POLICY_RULE_CONFLICT" });
    const result = friendlyError(err, "test");
    expect(result.title).toBe("Policy rule conflict");
    expect(result.description).toContain("conflicts");
  });

  it("maps approval conflict codes from the backend code field", () => {
    const retryable = friendlyError(
      new ApiError(409, "Conflict", { code: "approval_retryable_lock", retryable: true }),
      "test",
    );
    expect(retryable.title).toBe("Approval is updating");
    expect(retryable.description).toContain("Wait a moment");

    const staleSnapshot = friendlyError(
      new ApiError(409, "Conflict", { code: "approval_stale_snapshot" }),
      "test",
    );
    expect(staleSnapshot.title).toBe("Policy snapshot changed");

    const notActionable = friendlyError(
      new ApiError(409, "Conflict", { code: "approval_not_actionable" }),
      "test",
    );
    expect(notActionable.title).toBe("Approval can’t be decided");
  });

  it("formats validation errors array from body", () => {
    const err = new ApiError(422, "Validation", {
      errors: [
        { field: "topic", message: "required" },
        { field: "priority", message: "invalid value" },
      ],
    });
    const result = friendlyError(err, "test");
    expect(result.title).toBe("Validation failed");
    expect(result.description).toContain("topic: required");
    expect(result.description).toContain("priority: invalid value");
  });

  it("handles network errors (Failed to fetch)", () => {
    const result = friendlyError(new TypeError("Failed to fetch"), "test");
    expect(result.title).toBe("Unable to connect");
    expect(result.description).toContain("network");
  });

  it("handles timeout errors", () => {
    const result = friendlyError(new Error("Connection timed out"), "test");
    expect(result.title).toBe("Request timed out");
    expect(result.action?.href).toBe("/settings/health");
  });

  it("handles unknown errors gracefully", () => {
    const result = friendlyError("string error", "test");
    expect(result.title).toBe("Something went wrong");
    expect(result.description).toContain("try again");
  });

  it("handles null/undefined errors", () => {
    const result = friendlyError(null, "test");
    expect(result.title).toBe("Something went wrong");
  });

  it("maps unknown status codes to generic message", () => {
    const result = friendlyError(new ApiError(418, "I'm a teapot"), "test");
    expect(result.title).toContain("418");
    expect(result.description).toContain("unexpected");
  });

  it("does not throw for any error type", () => {
    // Smoke test: no input should cause friendlyError itself to throw.
    expect(() => friendlyError(new ApiError(500, "boom"), "ctx")).not.toThrow();
    expect(() => friendlyError(new TypeError("fail"), "ctx")).not.toThrow();
    expect(() => friendlyError(new Error("timeout"), "ctx")).not.toThrow();
    expect(() => friendlyError(undefined, "ctx")).not.toThrow();
    expect(() => friendlyError({}, "ctx")).not.toThrow();
    expect(() => friendlyError(42, "ctx")).not.toThrow();
  });
});
