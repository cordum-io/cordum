import { describe, expect, it } from "vitest";
import { ApiError } from "@/api/client";
import { __outputPolicyInternal } from "./useOutputPolicy";

describe("useOutputPolicy internals", () => {
  it("builds quarantined jobs query params with state, limit, and cursor", () => {
    const params = __outputPolicyInternal.buildQuarantineParams({
      limit: 50,
      cursor: 120,
    });

    expect(params).toContain("state=OUTPUT_QUARANTINED");
    expect(params).toContain("limit=50");
    expect(params).toContain("cursor=120");
  });

  it("parses output policy config from existing API payload shapes", () => {
    const parsed = __outputPolicyInternal.parseOutputPolicyConfig({
      output_safety: {
        enabled: false,
        scan_timeout_ms: 1400,
        max_payload_kb: 384,
        failure_action: "deny",
      },
      output_policy: {
        fail_mode: "closed",
        topic_overrides: [
          {
            topic_pattern: "finance.*",
            enabled: true,
            fail_mode: "closed",
            scanners: ["pii"],
          },
        ],
        scanner_overrides: [
          {
            id: "pii",
            enabled: false,
            action: "deny",
            enabled_types: ["email"],
          },
        ],
        custom_patterns: [
          {
            id: "cp-1",
            name: "Secret token",
            regex: "token",
            category: "secret",
            action: "quarantine",
            enabled: true,
          },
        ],
      },
    });

    expect(parsed.enabled).toBe(false);
    expect(parsed.failMode).toBe("closed");
    expect(parsed.failureAction).toBe("deny");
    expect(parsed.scanTimeoutMs).toBe(1400);
    expect(parsed.maxPayloadKb).toBe(384);
    expect(parsed.topicOverrides[0]).toMatchObject({
      topicPattern: "finance.*",
      failMode: "closed",
      scanners: ["pii"],
    });
    expect(parsed.scannerOverrides[0]).toMatchObject({
      id: "pii",
      enabled: false,
      action: "deny",
      enabledTypes: ["email"],
    });
    expect(parsed.customPatterns[0]).toMatchObject({
      id: "cp-1",
      name: "Secret token",
      action: "quarantine",
    });
  });

  it("merges output policy config while preserving compatibility keys", () => {
    const merged = __outputPolicyInternal.mergeOutputPolicyConfig(
      {
        existing_key: "keep",
        output_policy: {
          legacy_flag: true,
        },
      },
      {
        enabled: true,
        failMode: "open",
        scanTimeoutMs: 1800,
        maxPayloadKb: 1024,
        failureAction: "allow",
        topicOverrides: [
          {
            topicPattern: "ops.*",
            enabled: true,
            failMode: "open",
            scanners: ["pii", "toxicity"],
          },
        ],
        scannerOverrides: [
          {
            id: "pii",
            enabled: true,
            action: "quarantine",
            confidence: 85,
            enabledTypes: ["email"],
          },
        ],
        customPatterns: [
          {
            id: "cp-2",
            name: "Email-like",
            regex: "mail",
            category: "pii",
            action: "quarantine",
            enabled: true,
          },
        ],
      },
    );

    expect(merged.existing_key).toBe("keep");
    expect((merged.output_safety as Record<string, unknown>).enabled).toBe(true);
    expect((merged.output_policy as Record<string, unknown>).fail_mode).toBe("open");
    expect(merged.output_policy_enabled).toBe(true);
    expect(merged.output_policy_fail_mode).toBe("open");
    expect(merged.OUTPUT_POLICY_ENABLED).toBe("true");
    expect(
      Array.isArray(
        (merged.output_policy as Record<string, unknown>).scanner_overrides,
      ),
    ).toBe(true);
  });

  it("maps output policy API errors with status-aware guidance", () => {
    const validation = __outputPolicyInternal.describeOutputPolicyError(
      new ApiError(422, "unprocessable", { details: "fail_mode is invalid" }),
    );
    expect(validation).toContain("Validation failed");
    expect(validation).toContain("fail_mode is invalid");

    const conflict = __outputPolicyInternal.describeOutputPolicyError(
      new ApiError(409, "conflict", { message: "version mismatch" }),
    );
    expect(conflict).toContain("Conflict while updating output policy");
    expect(conflict).toContain("version mismatch");

    const generic = __outputPolicyInternal.describeOutputPolicyError(
      new Error("network timeout"),
    );
    expect(generic).toBe("network timeout");
  });

  // ---------------------------------------------------------------------------
  // Mutation safety: output policy config write idempotency
  // ---------------------------------------------------------------------------

  describe("output policy merge idempotency", () => {
    it("merge with identical config produces same output structure", () => {
      const config = {
        enabled: true,
        failMode: "open" as const,
        scanTimeoutMs: 2000,
        maxPayloadKb: 512,
        failureAction: "allow" as const,
        topicOverrides: [],
        scannerOverrides: [],
        customPatterns: [],
      };

      const merged1 = __outputPolicyInternal.mergeOutputPolicyConfig({}, config);
      const merged2 = __outputPolicyInternal.mergeOutputPolicyConfig(merged1, config);

      // Double-merge should produce structurally equivalent output
      expect(merged2.output_policy_enabled).toBe(merged1.output_policy_enabled);
      expect(merged2.output_policy_fail_mode).toBe(merged1.output_policy_fail_mode);
      expect(merged2.output_policy_scan_timeout_ms).toBe(merged1.output_policy_scan_timeout_ms);
    });

    it("parseOutputPolicyConfig returns defaults for empty/undefined input", () => {
      const defaults = __outputPolicyInternal.parseOutputPolicyConfig(undefined);
      expect(defaults.enabled).toBeDefined();
      expect(defaults.failMode).toBeDefined();
      expect(defaults.scanTimeoutMs).toBeDefined();
      expect(defaults.maxPayloadKb).toBeDefined();
    });

    it("parseOutputPolicyConfig round-trips through merge", () => {
      const original = {
        enabled: false,
        failMode: "closed" as const,
        scanTimeoutMs: 999,
        maxPayloadKb: 256,
        failureAction: "deny" as const,
        topicOverrides: [],
        scannerOverrides: [],
        customPatterns: [],
      };

      const merged = __outputPolicyInternal.mergeOutputPolicyConfig({}, original);
      const roundTripped = __outputPolicyInternal.parseOutputPolicyConfig(merged);

      expect(roundTripped.enabled).toBe(original.enabled);
      expect(roundTripped.failMode).toBe(original.failMode);
      expect(roundTripped.scanTimeoutMs).toBe(original.scanTimeoutMs);
      expect(roundTripped.maxPayloadKb).toBe(original.maxPayloadKb);
      expect(roundTripped.failureAction).toBe(original.failureAction);
    });
  });
});
