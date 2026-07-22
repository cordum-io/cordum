import { describe, expect, it } from "vitest";

import { AlertSeverity, ErrorCode, errorCodeCategory, errorCodeLabel } from "./types";

// Wire contract: proto/cordum/agent/v1/job.proto enum ErrorCode and
// proto/cordum/agent/v1/alert.proto enum AlertSeverity in the pinned CAP
// dependency. The backend populates error_code_enum with these exact numeric
// values; if this table drifts from the proto, the dashboard mislabels errors.
const WIRE_ERROR_CODES: Record<string, number> = {
  UNSPECIFIED: 0,
  PROTOCOL_VERSION_MISMATCH: 100,
  PROTOCOL_MALFORMED_PACKET: 101,
  PROTOCOL_UNKNOWN_PAYLOAD: 102,
  PROTOCOL_SIGNATURE_INVALID: 103,
  PROTOCOL_SIGNATURE_MISSING: 104,
  JOB_TIMEOUT: 200,
  JOB_RESOURCE_EXHAUSTED: 201,
  JOB_PERMISSION_DENIED: 202,
  JOB_INVALID_INPUT: 203,
  JOB_NOT_FOUND: 204,
  JOB_DUPLICATE: 205,
  JOB_WORKER_UNAVAILABLE: 206,
  SAFETY_DENIED: 300,
  SAFETY_POLICY_VIOLATION: 301,
  SAFETY_RISK_TAG_BLOCKED: 302,
  TRANSPORT_PUBLISH_FAILED: 400,
  TRANSPORT_SUBSCRIBE_FAILED: 401,
  TRANSPORT_CONNECTION_LOST: 402,
};

describe("ErrorCode wire contract", () => {
  it("matches the CAP protocol ErrorCode enum name-for-name and value-for-value", () => {
    const declared = Object.fromEntries(
      Object.entries(ErrorCode).filter(([, v]) => typeof v === "number"),
    );
    expect(declared).toEqual(WIRE_ERROR_CODES);
  });

  it("labels wire values with the matching category and name", () => {
    expect(errorCodeLabel(200)).toBe("Job: Timeout");
    expect(errorCodeLabel(204)).toBe("Job: Not Found");
    expect(errorCodeLabel(102)).toBe("Protocol: Unknown Payload");
    expect(errorCodeLabel(104)).toBe("Protocol: Signature Missing");
    expect(errorCodeLabel(302)).toBe("Safety: Risk Tag Blocked");
    expect(errorCodeLabel(400)).toBe("Transport: Publish Failed");
    expect(errorCodeLabel(999)).toBe("Error 999");
  });

  it("categorizes each wire range for badge coloring", () => {
    expect(errorCodeCategory(103)).toBe("protocol");
    expect(errorCodeCategory(206)).toBe("job");
    expect(errorCodeCategory(302)).toBe("safety");
    expect(errorCodeCategory(402)).toBe("transport");
    expect(errorCodeCategory(1000)).toBe("unknown");
  });
});

describe("AlertSeverity wire contract", () => {
  it("matches the CAP protocol AlertSeverity enum", () => {
    expect(AlertSeverity.UNSPECIFIED).toBe(0);
    expect(AlertSeverity.INFO).toBe(1);
    expect(AlertSeverity.WARNING).toBe(2);
    expect(AlertSeverity.ERROR).toBe(3);
    expect(AlertSeverity.CRITICAL).toBe(4);
  });
});
