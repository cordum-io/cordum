import { describe, expect, it } from "vitest";
import { buildDLQEntryDetails, resolveDLQError } from "./DLQPage";

describe("DLQPage expanded row rendering", () => {
  describe("buildDLQEntryDetails", () => {
    it("includes all diagnostic fields when present", () => {
      const result = buildDLQEntryDetails({
        jobId: "job-abc",
        status: "DEAD",
        reasonCode: "MAX_RETRIES",
        lastState: "DISPATCHED",
        originalTopic: "job.billing.charge",
        attempts: 5,
        failedAt: "2026-03-01T12:00:00Z",
      });

      expect(result).toEqual({
        jobId: "job-abc",
        status: "DEAD",
        reasonCode: "MAX_RETRIES",
        lastState: "DISPATCHED",
        originalTopic: "job.billing.charge",
        attempts: 5,
        failedAt: "2026-03-01T12:00:00Z",
      });
    });

    it("falls back to retryCount when attempts is undefined", () => {
      const result = buildDLQEntryDetails({
        jobId: "job-xyz",
        retryCount: 3,
      });

      expect(result.attempts).toBe(3);
    });

    it("falls back to 0 when both attempts and retryCount are undefined", () => {
      const result = buildDLQEntryDetails({
        jobId: "job-bare",
      });

      expect(result.attempts).toBe(0);
    });

    it("falls back to createdAt when failedAt is undefined", () => {
      const result = buildDLQEntryDetails({
        jobId: "job-no-failed",
        createdAt: "2026-02-28T08:00:00Z",
      });

      expect(result.failedAt).toBe("2026-02-28T08:00:00Z");
    });

    it("prefers failedAt over createdAt when both present", () => {
      const result = buildDLQEntryDetails({
        jobId: "job-both",
        failedAt: "2026-03-01T12:00:00Z",
        createdAt: "2026-02-28T08:00:00Z",
      });

      expect(result.failedAt).toBe("2026-03-01T12:00:00Z");
    });

    it("handles completely sparse entry without crashing", () => {
      const result = buildDLQEntryDetails({ jobId: "job-sparse" });

      expect(result.jobId).toBe("job-sparse");
      expect(result.status).toBeUndefined();
      expect(result.reasonCode).toBeUndefined();
      expect(result.lastState).toBeUndefined();
      expect(result.originalTopic).toBeUndefined();
      expect(result.attempts).toBe(0);
      expect(result.failedAt).toBeUndefined();
    });

    it("serializes to valid JSON (no circular refs, no undefined poison)", () => {
      const result = buildDLQEntryDetails({
        jobId: "job-json",
        status: "DEAD",
        attempts: 2,
      });

      const json = JSON.stringify(result, null, 2);
      expect(() => JSON.parse(json)).not.toThrow();
      expect(json).toContain("job-json");
      expect(json).toContain('"attempts": 2');
    });
  });

  describe("resolveDLQError", () => {
    it("returns error when present", () => {
      expect(resolveDLQError({ error: "connection timeout" })).toBe("connection timeout");
    });

    it("falls back to reason when error is absent", () => {
      expect(resolveDLQError({ reason: "rate limited" })).toBe("rate limited");
    });

    it("falls back to reason when error is empty string", () => {
      expect(resolveDLQError({ error: "", reason: "quota exceeded" })).toBe("quota exceeded");
    });

    it("returns default message when both error and reason are absent", () => {
      expect(resolveDLQError({})).toBe("No error message");
    });

    it("returns default message when both are empty strings", () => {
      expect(resolveDLQError({ error: "", reason: "" })).toBe("No error message");
    });

    it("prefers error over reason when both present", () => {
      expect(resolveDLQError({ error: "primary", reason: "secondary" })).toBe("primary");
    });
  });
});
