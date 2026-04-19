import { describe, it, expect } from "vitest";
import {
  isRunVisibilityActive,
  isRunVisibilityTerminal,
  toRunVisibilityState,
} from "../lib/runVisibility";
import { resolveRunChatBanner } from "./RunDetailPage";

/**
 * Tests for RunDetailPage logic: skipped step detection, live vs historical indicator.
 */

function isRunning(status: string): boolean {
  return isRunVisibilityActive(status);
}

function isTerminal(status: string): boolean {
  return isRunVisibilityTerminal(status);
}

function stepClasses(status: string): string[] {
  const classes: string[] = [];
  if (status === "pending") classes.push("text-muted-foreground");
  if (status === "skipped")
    classes.push("text-muted-foreground", "line-through", "opacity-50");
  if (status !== "pending" && status !== "skipped")
    classes.push("text-foreground");
  return classes;
}

describe("Skipped step rendering", () => {
  it("applies line-through and opacity for skipped steps", () => {
    const classes = stepClasses("skipped");
    expect(classes).toContain("line-through");
    expect(classes).toContain("opacity-50");
    expect(classes).toContain("text-muted-foreground");
  });

  it("does not apply line-through for pending steps", () => {
    const classes = stepClasses("pending");
    expect(classes).not.toContain("line-through");
    expect(classes).toContain("text-muted-foreground");
  });

  it("does not apply line-through for succeeded steps", () => {
    const classes = stepClasses("succeeded");
    expect(classes).not.toContain("line-through");
    expect(classes).toContain("text-foreground");
  });
});

describe("Live vs historical indicator", () => {
  it("running is live", () => {
    expect(isRunning("running")).toBe(true);
    expect(isTerminal("running")).toBe(false);
  });

  it("pending/queued are live", () => {
    expect(isRunning("pending")).toBe(true);
    expect(isRunning("queued")).toBe(true);
    expect(toRunVisibilityState("pending")).toBe("queued");
  });

  it("succeeded/completed are terminal (historical)", () => {
    expect(isRunning("succeeded")).toBe(false);
    expect(isTerminal("succeeded")).toBe(true);
    expect(isTerminal("completed")).toBe(true);
    expect(toRunVisibilityState("succeeded")).toBe("completed");
  });

  it("failed is terminal", () => {
    expect(isTerminal("failed")).toBe(true);
  });

  it("cancelled is terminal", () => {
    expect(isTerminal("cancelled")).toBe(true);
  });

  it("timed_out is terminal", () => {
    expect(isTerminal("timed_out")).toBe(true);
  });

  it("denied/blocked are terminal governance outcomes", () => {
    expect(isTerminal("denied")).toBe(true);
    expect(isTerminal("blocked")).toBe(true);
    expect(isRunning("denied")).toBe(false);
    expect(toRunVisibilityState("denied")).toBe("blocked");
  });

  it("waiting maps to blocked (not live)", () => {
    expect(isRunning("waiting")).toBe(false);
    expect(isTerminal("waiting")).toBe(true);
    expect(toRunVisibilityState("waiting")).toBe("blocked");
  });
});

describe("resolveRunChatBanner", () => {
  it("shows a timeline fallback warning when chat fails but timeline data exists", () => {
    expect(resolveRunChatBanner({ status: 500 }, true)).toEqual({
      tone: "warning",
      message: "Chat unavailable, showing timeline events instead.",
    });
  });

  it("shows auth guidance for 401/403 chat failures without fallback data", () => {
    expect(resolveRunChatBanner({ status: 401 }, false)).toEqual({
      tone: "danger",
      message: "Chat unavailable — check your API key or permissions",
    });
    expect(resolveRunChatBanner({ status: 403 }, false)).toEqual({
      tone: "danger",
      message: "Chat unavailable — check your API key or permissions",
    });
  });

  it("shows the non-error fallback banner when only timeline events are available", () => {
    expect(resolveRunChatBanner(null, true)).toEqual({
      tone: "warning",
      message: "Showing timeline events (no chat messages)",
    });
  });
});

// ---------------------------------------------------------------------------
// mapStepStatus — waiting must NOT collapse to running
// ---------------------------------------------------------------------------

function mapStepStatus(status?: string): string {
  switch (status) {
    case "completed":
      return "succeeded";
    case "succeeded":
      return "succeeded";
    case "queued":
      return "pending";
    case "running":
      return "running";
    case "waiting":
      return "waiting";
    case "quarantined":
    case "output_quarantined":
      return "quarantined";
    case "denied":
    case "blocked":
    case "failed":
    case "timed_out":
      return "failed";
    case "cancelled":
      return "skipped";
    default:
      return "pending";
  }
}

describe("mapStepStatus — approval-waiting preservation", () => {
  it("preserves waiting as distinct from running", () => {
    expect(mapStepStatus("waiting")).toBe("waiting");
    expect(mapStepStatus("running")).toBe("running");
    expect(mapStepStatus("waiting")).not.toBe("running");
  });

  it("maps succeeded, failed, cancelled correctly", () => {
    expect(mapStepStatus("succeeded")).toBe("succeeded");
    expect(mapStepStatus("failed")).toBe("failed");
    expect(mapStepStatus("denied")).toBe("failed");
    expect(mapStepStatus("blocked")).toBe("failed");
    expect(mapStepStatus("cancelled")).toBe("skipped");
    expect(mapStepStatus("timed_out")).toBe("failed");
  });

  it("maps queued/completed aliases", () => {
    expect(mapStepStatus("queued")).toBe("pending");
    expect(mapStepStatus("completed")).toBe("succeeded");
  });

  it("maps quarantined states", () => {
    expect(mapStepStatus("quarantined")).toBe("quarantined");
    expect(mapStepStatus("output_quarantined")).toBe("quarantined");
  });

  it("defaults to pending for unknown", () => {
    expect(mapStepStatus(undefined)).toBe("pending");
    expect(mapStepStatus("")).toBe("pending");
  });
});

// Regression tests for issue #168: chat send error handling.
import { resolveChatSendErrorMessage } from "./RunDetailPage";

describe("resolveChatSendErrorMessage", () => {
  it("maps 401/403 to a credential-specific message", () => {
    expect(resolveChatSendErrorMessage({ status: 401 })).toMatch(
      /API key|permissions/i,
    );
    expect(resolveChatSendErrorMessage({ status: 403 })).toMatch(
      /API key|permissions/i,
    );
  });

  it("maps 404 to endpoint-not-available", () => {
    expect(resolveChatSendErrorMessage({ status: 404 })).toMatch(
      /endpoint not available/i,
    );
  });

  it("maps 413 to payload-too-large", () => {
    expect(resolveChatSendErrorMessage({ status: 413 })).toMatch(/too large/i);
  });

  it("maps 429 to rate-limit phrasing", () => {
    expect(resolveChatSendErrorMessage({ status: 429 })).toMatch(
      /too many requests|slow down/i,
    );
  });

  it("maps any 5xx to service-unavailable", () => {
    expect(resolveChatSendErrorMessage({ status: 500 })).toMatch(
      /service is unavailable/i,
    );
    expect(resolveChatSendErrorMessage({ status: 502 })).toMatch(
      /service is unavailable/i,
    );
    expect(resolveChatSendErrorMessage({ status: 503 })).toMatch(
      /service is unavailable/i,
    );
  });

  it("falls back to a generic message for non-numeric or missing status", () => {
    expect(resolveChatSendErrorMessage(new Error("network down"))).toBe(
      "Unable to send chat message",
    );
    expect(resolveChatSendErrorMessage(null)).toBe(
      "Unable to send chat message",
    );
    expect(resolveChatSendErrorMessage({ status: "boom" })).toBe(
      "Unable to send chat message",
    );
    expect(resolveChatSendErrorMessage({ status: 418 })).toBe(
      "Unable to send chat message",
    );
  });

  it("every mapped status message starts with 'Send failed'", () => {
    // Keeps the toast visually distinct from the load banner messages so users
    // can tell whether the send or the load failed.
    for (const status of [401, 403, 404, 413, 429, 500, 503]) {
      expect(resolveChatSendErrorMessage({ status })).toMatch(/^Send failed/);
    }
  });
});
