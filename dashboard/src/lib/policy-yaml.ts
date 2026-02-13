import YAML from "yaml";

// ---------------------------------------------------------------------------
// Validate YAML (syntax only)
// ---------------------------------------------------------------------------

export interface YamlValidationResult {
  valid: boolean;
  errors: Array<{ line: number; message: string }>;
  parsed?: unknown;
}

export function validatePolicyYaml(yamlStr: string): YamlValidationResult {
  // Only validate YAML syntax; safety policy schema is enforced server-side.
  try {
    const parsed = YAML.parse(yamlStr);
    return { valid: true, errors: [], parsed };
  } catch (err) {
    const msg = err instanceof Error ? err.message : "Invalid YAML syntax";
    const lineMatch = msg.match(/at line (\d+)/);
    const line = lineMatch ? parseInt(lineMatch[1], 10) : 1;
    return { valid: false, errors: [{ line, message: msg }] };
  }
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
  try {
    const parsed = YAML.parse(yamlStr);
    return countRulesFromParsed(parsed);
  } catch {
    return 0;
  }
}
