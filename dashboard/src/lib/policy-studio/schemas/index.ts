import { RuleType } from "@/api/generated/model/ruleType";
import inputSchema from "./generated/input.json";
import outputSchema from "./generated/output.json";
import velocitySchema from "./generated/velocity.json";
import edgeSchema from "./generated/edge.json";

// JSON Schema fragment (draft-07) for a single Rule envelope locked to a
// specific RuleType. Loaded statically from the generator output so Vite
// can bundle it into the lazy editor chunk; the live schema graph never
// fans out beyond what's imported here.
//
// Source of truth: `dashboard/scripts/policy-studio/rule-payloads.mjs` +
// `cordum/docs/api/openapi/cordum-api.yaml` (Rule envelope). Regenerate
// with `pnpm run generate-schemas`; CI's `pnpm run check-schemas` fails
// the PR on drift.
//
// Vite typing for `*.json` imports is `any`. We narrow at the boundary so
// callers see `RuleSchema`, never `any`.
export interface RuleSchema {
  $id: string;
  $schema: string;
  title: string;
  description: string;
  type: "object";
  additionalProperties: false;
  required: string[];
  properties: Record<string, unknown>;
}

const SCHEMA_BY_TYPE: Record<RuleType, RuleSchema> = {
  [RuleType.input]: inputSchema as RuleSchema,
  [RuleType.output]: outputSchema as RuleSchema,
  [RuleType.velocity]: velocitySchema as RuleSchema,
  [RuleType.edge]: edgeSchema as RuleSchema,
};

/**
 * Returns the JSON Schema for the given rule type, or null if the type
 * is not one of the four supported authoring types. Callers that don't
 * have a known type (UNKNOWN_RULE_TYPE sentinel) should refuse to mount
 * the editor rather than fall through to a default schema — the safe
 * Unknown fallback behavior added by task-15537d13 is preserved here.
 */
export function getRuleSchema(type: RuleType | string | null | undefined): RuleSchema | null {
  if (typeof type !== "string") return null;
  if (!Object.prototype.hasOwnProperty.call(SCHEMA_BY_TYPE, type)) return null;
  return SCHEMA_BY_TYPE[type as RuleType];
}
