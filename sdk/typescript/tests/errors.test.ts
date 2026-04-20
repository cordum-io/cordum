import { describe, expect, it } from "vitest";
import {
  AuthenticationError,
  AuthorizationError,
  ConflictError,
  CordumError,
  NetworkError,
  NotFoundError,
  RateLimitError,
  RetryExhaustedError,
  ServerError,
  TimeoutError,
  ValidationError,
  isCordumError,
  parseRetryAfterHeader,
  raiseForStatus,
} from "../src/errors.js";

async function getThrown(response: Response, requestId = "req-123") {
  try {
    await raiseForStatus(response, requestId);
    throw new Error("Expected raiseForStatus to throw");
  } catch (error) {
    return error;
  }
}

describe("errors", () => {
  it("maps 401 to AuthenticationError and preserves instanceof", async () => {
    const error = await getThrown(new Response(JSON.stringify({ error: { message: "Unauthorized" } }), {
      status: 401,
      headers: { "content-type": "application/json" },
    }));

    expect(error).toBeInstanceOf(AuthenticationError);
    expect(error).toBeInstanceOf(CordumError);
    expect(error).toBeInstanceOf(Error);
    expect(isCordumError(error)).toBe(true);
  });

  it("maps 403/404/409/500 classes correctly", async () => {
    await expect(getThrown(new Response("Forbidden", { status: 403 }))).resolves.toBeInstanceOf(AuthorizationError);
    await expect(getThrown(new Response("Missing", { status: 404 }))).resolves.toBeInstanceOf(NotFoundError);
    await expect(getThrown(new Response("Conflict", { status: 409 }))).resolves.toBeInstanceOf(ConflictError);
    await expect(getThrown(new Response("Boom", { status: 500 }))).resolves.toBeInstanceOf(ServerError);
  });

  it("parses validation field errors from the gateway envelope", async () => {
    const error = await getThrown(new Response(JSON.stringify({
      error: {
        code: "validation_failed",
        message: "Validation failed",
        details: {
          fields: {
            email: ["required"],
            role: "invalid",
          },
        },
      },
    }), {
      status: 422,
      headers: { "content-type": "application/json" },
    }));

    expect(error).toBeInstanceOf(ValidationError);
    expect((error as ValidationError).fieldErrors).toEqual({
      email: ["required"],
      role: ["invalid"],
    });
    expect((error as ValidationError).code).toBe("validation_failed");
  });

  it("parses Retry-After headers from numeric seconds and HTTP-date", async () => {
    expect(parseRetryAfterHeader("2")).toBe(2000);
    expect(parseRetryAfterHeader("Wed, 21 Oct 2015 07:28:00 GMT", Date.parse("Wed, 21 Oct 2015 07:27:58 GMT"))).toBe(2000);

    const error = await getThrown(new Response(JSON.stringify({ message: "Slow down" }), {
      status: 429,
      headers: {
        "content-type": "application/json",
        "retry-after": "2",
      },
    }));

    expect(error).toBeInstanceOf(RateLimitError);
    expect((error as RateLimitError).retryAfterMs).toBe(2000);
  });

  it("falls back to text payload when JSON is malformed", async () => {
    const error = await getThrown(new Response("not-json", {
      status: 503,
      headers: { "content-type": "application/json" },
    }));

    expect(error).toBeInstanceOf(ServerError);
    expect((error as ServerError).payload).toBe("not-json");
    expect(error).toBeInstanceOf(Error);
    expect((error as Error).message).toBe("not-json");
  });

  it("provides a stable string primitive for logging", () => {
    const error = new NetworkError("connection failed", {
      status: 0,
      requestId: "req-xyz",
      code: "network_error",
    });

    expect(String(error)).toContain("NetworkError connection failed");
    expect(`${error}`).toContain("requestId=req-xyz");
  });

  it("constructs timeout and retry exhausted wrappers", () => {
    const timeout = new TimeoutError("timed out", { requestId: "req-timeout" });
    expect(timeout).toBeInstanceOf(TimeoutError);
    expect(timeout).toBeInstanceOf(CordumError);

    const exhausted = new RetryExhaustedError("retries exhausted", {
      requestId: "req-retry",
      lastError: timeout,
    });
    expect(exhausted.cause).toBe(timeout);
    expect(exhausted.lastError).toBe(timeout);
  });
});
