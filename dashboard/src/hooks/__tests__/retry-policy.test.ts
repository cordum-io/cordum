import { describe, it, expect } from "vitest";
import { ApiError } from "../../api/client";
import { shouldRetry, retryDelay } from "../../api/retry";

describe("shouldRetry", () => {
  it("does not retry 401 Unauthorized", () => {
    const error = new ApiError(401, "Unauthorized");
    expect(shouldRetry(0, error)).toBe(false);
  });

  it("does not retry 403 Forbidden", () => {
    const error = new ApiError(403, "Forbidden");
    expect(shouldRetry(0, error)).toBe(false);
  });

  it("does not retry 404 Not Found", () => {
    const error = new ApiError(404, "Not Found");
    expect(shouldRetry(0, error)).toBe(false);
  });

  it("does not retry 400 Bad Request", () => {
    const error = new ApiError(400, "Bad Request");
    expect(shouldRetry(0, error)).toBe(false);
  });

  it("does not retry 409 Conflict", () => {
    const error = new ApiError(409, "Conflict");
    expect(shouldRetry(0, error)).toBe(false);
  });

  it("does not retry 410 Gone", () => {
    const error = new ApiError(410, "Gone");
    expect(shouldRetry(0, error)).toBe(false);
  });

  it("retries 500 Internal Server Error up to 2 times", () => {
    const error = new ApiError(500, "Internal Server Error");
    expect(shouldRetry(0, error)).toBe(true);
    expect(shouldRetry(1, error)).toBe(true);
    expect(shouldRetry(2, error)).toBe(false);
  });

  it("retries 429 Rate Limited up to 2 times", () => {
    const error = new ApiError(429, "Too Many Requests");
    expect(shouldRetry(0, error)).toBe(true);
    expect(shouldRetry(1, error)).toBe(true);
    expect(shouldRetry(2, error)).toBe(false);
  });

  it("retries 502 Bad Gateway up to 2 times", () => {
    const error = new ApiError(502, "Bad Gateway");
    expect(shouldRetry(0, error)).toBe(true);
    expect(shouldRetry(1, error)).toBe(true);
    expect(shouldRetry(2, error)).toBe(false);
  });

  it("retries 503 Service Unavailable up to 2 times", () => {
    const error = new ApiError(503, "Service Unavailable");
    expect(shouldRetry(0, error)).toBe(true);
    expect(shouldRetry(1, error)).toBe(true);
    expect(shouldRetry(2, error)).toBe(false);
  });

  it("retries network errors (plain Error) up to 2 times", () => {
    const error = new Error("Failed to fetch");
    expect(shouldRetry(0, error)).toBe(true);
    expect(shouldRetry(1, error)).toBe(true);
    expect(shouldRetry(2, error)).toBe(false);
  });
});

describe("retryDelay", () => {
  it("returns a value between BASE and BASE + JITTER for attempt 0", () => {
    for (let i = 0; i < 20; i++) {
      const delay = retryDelay(0);
      expect(delay).toBeGreaterThanOrEqual(1000);
      expect(delay).toBeLessThanOrEqual(1500);
    }
  });

  it("increases delay with attempt index", () => {
    // Attempt 0: 1000-1500ms, attempt 1: 2000-2500ms
    const delays0 = Array.from({ length: 20 }, () => retryDelay(0));
    const delays1 = Array.from({ length: 20 }, () => retryDelay(1));
    const avg0 = delays0.reduce((a, b) => a + b, 0) / delays0.length;
    const avg1 = delays1.reduce((a, b) => a + b, 0) / delays1.length;
    expect(avg1).toBeGreaterThan(avg0);
  });

  it("caps delay at MAX_BACKOFF + JITTER", () => {
    for (let i = 0; i < 20; i++) {
      const delay = retryDelay(10); // Very high attempt index
      expect(delay).toBeLessThanOrEqual(4500); // 4000 max + 500 jitter
    }
  });
});
