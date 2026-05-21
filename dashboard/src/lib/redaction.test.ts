import { describe, expect, it } from "vitest";
import { sanitizeMCPField, sanitizeMCPPayload } from "./redaction";

describe("sanitizeMCPField (defense-in-depth)", () => {
  it("returns the *_redacted suffix verbatim when present (server-side redaction trusted)", () => {
    const out = sanitizeMCPField(
      { prompt: "RAW SECRET sk-leaked" },
      { prompt_redacted: "[REDACTED:secret]" },
      "prompt",
    );
    expect(out).toBe("[REDACTED:secret]");
  });

  it("strips a bare field when no *_redacted counterpart is present", () => {
    const out = sanitizeMCPField(
      { prompt: "RAW SECRET sk-leaked" },
      undefined,
      "prompt",
    );
    expect(out).toBe("[redacted by client sanitizer]");
  });

  it("returns '—' for absent fields with no redacted counterpart", () => {
    const out = sanitizeMCPField(undefined, undefined, "prompt");
    expect(out).toBe("—");
  });

  it("returns the value verbatim when neither sensitive-field name nor bare-field heuristic applies", () => {
    const out = sanitizeMCPField(
      { duration_ms: 42 },
      undefined,
      "duration_ms",
    );
    expect(out).toBe("42");
  });

  it("strips bare tool_input even when value contains an opaque secret-looking string", () => {
    const out = sanitizeMCPField(
      { tool_input: "Authorization: Bearer sk-real-leak-1234" },
      undefined,
      "tool_input",
    );
    expect(out).toBe("[redacted by client sanitizer]");
    expect(out).not.toContain("sk-real-leak-1234");
    expect(out).not.toContain("Bearer");
  });
});

describe("sanitizeMCPPayload", () => {
  it("returns the entire *_redacted payload object when a matching redacted blob exists", () => {
    const raw = { tool_input: "RAW", duration_ms: 42 };
    const redacted = { tool_input_redacted: "[REDACTED:tool_input]" };
    const out = sanitizeMCPPayload(raw, redacted);
    expect(out).toContain("REDACTED:tool_input");
    expect(out).not.toContain("RAW");
  });

  it("returns '[redacted by client sanitizer]' for a raw payload that contains a sensitive field with no redacted counterpart", () => {
    const out = sanitizeMCPPayload({ prompt: "RAW SECRET" }, undefined);
    expect(out).toContain("[redacted by client sanitizer]");
    expect(out).not.toContain("RAW SECRET");
  });

  it("returns '—' for null/undefined input", () => {
    expect(sanitizeMCPPayload(undefined, undefined)).toBe("—");
    expect(sanitizeMCPPayload(null, null)).toBe("—");
  });

  it("preserves non-sensitive payload values when no sensitive field is present", () => {
    const out = sanitizeMCPPayload({ tool_name: "Read", path_class: "public" }, undefined);
    expect(out).toContain("Read");
    expect(out).toContain("public");
  });
});
