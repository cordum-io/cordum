import YAML from "yaml";

// ---------------------------------------------------------------------------
// Validate YAML (syntax only)
// ---------------------------------------------------------------------------

export interface PolicyYamlParseError {
  line: number;
  column: number;
  message: string;
}

export interface PolicyYamlParseResult {
  valid: boolean;
  errors: PolicyYamlParseError[];
  parsed?: unknown;
}

function parseLineAndColumn(msg: string): { line: number; column: number } {
  const lineCol = msg.match(/line\s+(\d+),\s*column\s+(\d+)/i);
  if (lineCol) {
    return {
      line: parseInt(lineCol[1], 10),
      column: parseInt(lineCol[2], 10),
    };
  }
  const lineOnly = msg.match(/line\s+(\d+)/i);
  return { line: lineOnly ? parseInt(lineOnly[1], 10) : 1, column: 1 };
}

function toParseError(error: unknown): PolicyYamlParseError {
  if (typeof error === "object" && error !== null) {
    const withLinePos = error as {
      message?: unknown;
      linePos?: Array<{ line: number; col: number }>;
    };
    const msg = typeof withLinePos.message === "string"
      ? withLinePos.message
      : "Invalid YAML syntax";
    const firstPos = withLinePos.linePos?.[0];
    if (firstPos) {
      return { line: firstPos.line, column: firstPos.col, message: msg };
    }
    const fallback = parseLineAndColumn(msg);
    return { line: fallback.line, column: fallback.column, message: msg };
  }
  const msg = error instanceof Error ? error.message : "Invalid YAML syntax";
  const fallback = parseLineAndColumn(msg);
  return { line: fallback.line, column: fallback.column, message: msg };
}

export function parsePolicyYaml(yamlStr: string): PolicyYamlParseResult {
  if (!yamlStr.trim()) {
    return { valid: true, errors: [], parsed: {} };
  }
  const doc = YAML.parseDocument(yamlStr, { prettyErrors: false });
  const docErrors = doc.errors ?? [];
  if (docErrors.length > 0) {
    return {
      valid: false,
      errors: docErrors.map((err) => toParseError(err)),
    };
  }
  try {
    const parsed = doc.toJS();
    return { valid: true, errors: [], parsed };
  } catch (err) {
    return { valid: false, errors: [toParseError(err)] };
  }
}

export function summarizePolicyYamlErrors(
  errors: PolicyYamlParseError[],
  maxItems = 2,
): string | undefined {
  if (errors.length === 0) return undefined;
  const segments = errors
    .slice(0, maxItems)
    .map((error) => `line ${error.line}, column ${error.column}: ${error.message}`);
  if (errors.length > maxItems) {
    segments.push(`+${errors.length - maxItems} additional issue(s)`);
  }
  return segments.join(" | ");
}

export interface YamlValidationResult extends PolicyYamlParseResult {}

export function validatePolicyYaml(yamlStr: string): YamlValidationResult {
  // Only validate YAML syntax; safety policy schema is enforced server-side.
  return parsePolicyYaml(yamlStr);
}

export function stringifyPolicyYaml(value: unknown): string {
  return YAML.stringify(value, { lineWidth: 0 });
}

function countRulesFromParsed(parsed: unknown): number {
  if (!parsed || typeof parsed !== "object") return 0;
  const root = parsed as Record<string, unknown>;
  if (Array.isArray(root.rules)) {
    return root.rules.length;
  }
  if (root.tenants && typeof root.tenants === "object") {
    let total = 0;
    for (const tenant of Object.values(root.tenants as Record<string, unknown>)) {
      if (!tenant || typeof tenant !== "object") continue;
      const t = tenant as Record<string, unknown>;
      if (Array.isArray(t.deny_topics)) total += t.deny_topics.length;
      if (Array.isArray(t.allow_topics)) total += t.allow_topics.length;
    }
    return total;
  }
  return 0;
}

export function countRulesFromYaml(yamlStr: string): number {
  const parsed = parsePolicyYaml(yamlStr);
  if (!parsed.valid) {
    return 0;
  }
  return countRulesFromParsed(parsed.parsed);
}
