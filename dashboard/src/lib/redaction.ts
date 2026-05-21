/*
 * EDGE-105 — client-side MCP redaction sanitizer (defense-in-depth).
 *
 * Backend Edge surfaces are already responsible for redacting prompts,
 * tool inputs, and tool results before they reach the dashboard
 * (input_redacted is the only field guaranteed safe to display per the
 * project_edge_redaction_contract memo).
 *
 * This module is the dashboard's belt-and-suspenders: even if a raw
 * sensitive field slips through, we never show it. The contract:
 *
 *   - When a `<name>_redacted` companion field is present, return it
 *     verbatim (server-side redaction is trusted; we do not re-shape).
 *   - When only the bare sensitive field is present, return a stable
 *     "[redacted by client sanitizer]" placeholder. Never return the
 *     bare value.
 *   - For non-sensitive fields, return their string representation.
 *
 * Sensitive field names are intentionally narrow — we only sanitize
 * fields we know carry user/tool secrets. New fields must be added
 * explicitly; we do not auto-redact unknown content.
 */

type JsonRecord = Record<string, unknown> | undefined | null;

const SENSITIVE_FIELDS = new Set<string>([
  "prompt",
  "tool_input",
  "tool_output",
  "result",
  "args",
  "arguments",
  "input",
  "output",
  "message",
  "content",
]);

const SANITIZED_PLACEHOLDER = "[redacted by client sanitizer]";
const ABSENT_PLACEHOLDER = "—";

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * sanitizeMCPField returns a display-safe string for one field of an MCP
 * event payload. Prefers the *_redacted suffix when present; otherwise
 * strips the bare value if the field name is on the sensitive list.
 */
export function sanitizeMCPField(
  raw: JsonRecord,
  redacted: JsonRecord,
  field: string,
): string {
  const redactedKey = `${field}_redacted`;
  if (isRecord(redacted) && redactedKey in redacted) {
    const value = redacted[redactedKey];
    return value === null || value === undefined ? ABSENT_PLACEHOLDER : String(value);
  }
  if (isRecord(raw) && redactedKey in raw) {
    const value = raw[redactedKey];
    return value === null || value === undefined ? ABSENT_PLACEHOLDER : String(value);
  }
  if (isRecord(raw) && field in raw) {
    if (SENSITIVE_FIELDS.has(field)) {
      return SANITIZED_PLACEHOLDER;
    }
    const value = raw[field];
    return value === null || value === undefined ? ABSENT_PLACEHOLDER : String(value);
  }
  return ABSENT_PLACEHOLDER;
}

/**
 * sanitizeMCPPayload returns a JSON-stringified, redacted view of an
 * entire payload object. Used by the MCP inspector for the "args" and
 * "result" code-block preview.
 *
 * - If `redacted` carries any *_redacted blob, the whole redacted
 *   object is serialized (server is the source of truth for redaction
 *   shape).
 * - Otherwise the raw payload is walked: sensitive fields become the
 *   placeholder, non-sensitive fields pass through.
 */
export function sanitizeMCPPayload(raw: unknown, redacted: unknown): string {
  if (isRecord(redacted) && Object.keys(redacted).some((k) => k.endsWith("_redacted"))) {
    try {
      return JSON.stringify(redacted, null, 2);
    } catch {
      return SANITIZED_PLACEHOLDER;
    }
  }
  if (raw === null || raw === undefined) {
    return ABSENT_PLACEHOLDER;
  }
  if (!isRecord(raw)) {
    return String(raw);
  }
  const sanitized: Record<string, unknown> = {};
  let strippedAny = false;
  for (const [key, value] of Object.entries(raw)) {
    if (SENSITIVE_FIELDS.has(key)) {
      sanitized[key] = SANITIZED_PLACEHOLDER;
      strippedAny = true;
      continue;
    }
    sanitized[key] = value;
  }
  if (strippedAny) {
    try {
      return JSON.stringify(sanitized, null, 2);
    } catch {
      return SANITIZED_PLACEHOLDER;
    }
  }
  try {
    return JSON.stringify(raw, null, 2);
  } catch {
    return SANITIZED_PLACEHOLDER;
  }
}
