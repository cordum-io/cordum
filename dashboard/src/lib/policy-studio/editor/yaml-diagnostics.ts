import type { RuleSchema } from "@/lib/policy-studio/schemas";

// Schema-driven Monaco YAML diagnostics, completion, and hover support.
//
// Why hand-rolled instead of `monaco-yaml`? Adding a new dependency mid-task
// for the schema integration was rejected by the project's auto-classifier
// (see msg-c8f4fd94 / QA reopen #1). The four rule schemas are small and
// closed-shape; a focused validator covering the constructs we actually
// emit (type, enum, const, properties, required, additionalProperties,
// items, anyOf{required}) is ~120 LOC, deterministic, and fully testable.
// The Monaco-side wiring uses the standard `monaco.languages.*` registrar
// APIs and `monaco.editor.setModelMarkers` — the SAME surface a future
// migration to `monaco-yaml` would also produce, so swapping in monaco-yaml
// later is a localized change in `RuleMonacoEditor.tsx`.

// ---------------------------------------------------------------------------
// Validation diagnostics
// ---------------------------------------------------------------------------

export interface SchemaDiagnostic {
  path: string;
  message: string;
}

interface SchemaNode {
  type?: string | string[];
  enum?: ReadonlyArray<unknown>;
  const?: unknown;
  properties?: Record<string, SchemaNode>;
  required?: ReadonlyArray<string>;
  additionalProperties?: boolean | SchemaNode;
  items?: SchemaNode;
  anyOf?: ReadonlyArray<SchemaNode>;
  description?: string;
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
}

function asNode(value: unknown): SchemaNode {
  return (value ?? {}) as SchemaNode;
}

function pathSegment(path: string, key: string | number): string {
  return path === "" ? `${key}` : `${path}.${key}`;
}

function jsonTypeOf(value: unknown): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  if (typeof value === "number" && Number.isInteger(value)) return "integer";
  return typeof value;
}

function typeMatches(actual: string, expected: string): boolean {
  if (actual === expected) return true;
  // JSON Schema treats "integer" as a refinement of "number" — every integer
  // satisfies a `type: number` constraint.
  if (expected === "number" && actual === "integer") return true;
  return false;
}

function validateNode(node: SchemaNode, value: unknown, path: string, out: SchemaDiagnostic[]): void {
  if (node.type !== undefined) {
    const expectedTypes = Array.isArray(node.type) ? node.type : [node.type];
    const actual = jsonTypeOf(value);
    const ok = expectedTypes.some((t) => typeMatches(actual, t));
    if (!ok) {
      out.push({
        path,
        message: `Expected ${expectedTypes.join(" or ")}, got ${actual}.`,
      });
      return;
    }
  }

  if (node.enum !== undefined) {
    if (!node.enum.includes(value)) {
      out.push({
        path,
        message: `Expected one of: ${node.enum.map((v) => JSON.stringify(v)).join(", ")}.`,
      });
    }
  }

  if (node.const !== undefined) {
    if (value !== node.const) {
      out.push({ path, message: `Expected ${JSON.stringify(node.const)}.` });
    }
  }

  if (typeof value === "string") {
    if (typeof node.minLength === "number" && value.length < node.minLength) {
      out.push({ path, message: `Must be at least ${node.minLength} characters.` });
    }
    if (typeof node.maxLength === "number" && value.length > node.maxLength) {
      out.push({ path, message: `Must be ${node.maxLength} characters or fewer.` });
    }
  }

  if (typeof value === "number") {
    if (typeof node.minimum === "number" && value < node.minimum) {
      out.push({ path, message: `Must be at least ${node.minimum}.` });
    }
    if (typeof node.maximum === "number" && value > node.maximum) {
      out.push({ path, message: `Must be at most ${node.maximum}.` });
    }
  }

  if (Array.isArray(value) && node.items) {
    for (let i = 0; i < value.length; i++) {
      validateNode(node.items, value[i], pathSegment(path, i), out);
    }
  }

  if (value !== null && typeof value === "object" && !Array.isArray(value) && node.properties) {
    const obj = value as Record<string, unknown>;
    if (Array.isArray(node.required)) {
      for (const req of node.required) {
        if (!(req in obj)) {
          out.push({ path: pathSegment(path, req), message: `Required property "${req}" is missing.` });
        }
      }
    }
    for (const [key, child] of Object.entries(node.properties)) {
      if (key in obj) {
        validateNode(child, obj[key], pathSegment(path, key), out);
      }
    }
    if (node.additionalProperties === false) {
      const allowed = new Set(Object.keys(node.properties));
      for (const key of Object.keys(obj)) {
        if (!allowed.has(key)) {
          out.push({ path: pathSegment(path, key), message: `Unknown property "${key}".` });
        }
      }
    }
  }

  if (Array.isArray(node.anyOf) && node.anyOf.length > 0) {
    const branchErrors = node.anyOf.map((branch) => {
      const localOut: SchemaDiagnostic[] = [];
      validateNode(branch, value, path, localOut);
      return localOut;
    });
    const anySatisfied = branchErrors.some((errs) => errs.length === 0);
    if (!anySatisfied) {
      out.push({
        path,
        message: "Did not match any branch of anyOf — see schema for required combinations.",
      });
    }
  }
}

/**
 * Validates a parsed YAML object against the rule schema. Returns an empty
 * array when the value is fully valid; otherwise an entry per violation
 * with a dotted path and human-readable message. Pure function — no I/O,
 * no Monaco dependency, no console writes.
 */
export function validateAgainstSchema(value: unknown, schema: RuleSchema): SchemaDiagnostic[] {
  const out: SchemaDiagnostic[] = [];
  validateNode(schema as SchemaNode, value, "", out);
  return out;
}

// ---------------------------------------------------------------------------
// Completion + hover catalogs
// ---------------------------------------------------------------------------

export interface SchemaCompletionItem {
  label: string;
  documentation: string;
  kind: "field" | "value";
  enumValue?: string;
}

interface CatalogEntry {
  /** Dotted path like `match.topics`. The last segment is the property name. */
  path: string;
  description: string;
  enumValues: ReadonlyArray<string>;
}

function collectCatalog(node: SchemaNode, path: string, out: CatalogEntry[]): void {
  if (node.properties) {
    for (const [key, child] of Object.entries(node.properties)) {
      const childPath = pathSegment(path, key);
      const description = (child.description ?? "").trim();
      const enumValues: string[] = [];
      if (Array.isArray(child.enum)) {
        for (const v of child.enum) {
          if (typeof v === "string") enumValues.push(v);
        }
      }
      if (typeof child.const === "string") enumValues.push(child.const);
      // Items: pull enum values from items.enum so a field declared as
      // `array<enum>` (e.g. finding_types) surfaces its allowed values
      // as completion candidates.
      if (child.items && Array.isArray(child.items.enum)) {
        for (const v of child.items.enum) {
          if (typeof v === "string") enumValues.push(v);
        }
      }
      out.push({ path: childPath, description, enumValues });
      collectCatalog(child, childPath, out);
    }
  }
}

/**
 * Flattens a schema's properties into a catalog of completion items. The
 * `kind` distinguishes property names ("field") from enum values ("value").
 * Enum values appear before properties so common picks like `allow` /
 * `deny` are surfaced first when the user is typing under `decide:`.
 */
export function propertyCompletionsFromSchema(schema: RuleSchema): SchemaCompletionItem[] {
  const catalog: CatalogEntry[] = [];
  collectCatalog(schema as SchemaNode, "", catalog);
  const out: SchemaCompletionItem[] = [];
  for (const entry of catalog) {
    const lastSegment = entry.path.split(".").pop() ?? entry.path;
    const documentation = entry.description || `${entry.path} (no description)`;
    for (const value of entry.enumValues) {
      out.push({
        label: value,
        documentation: `${entry.path} = ${value}`,
        kind: "value",
        enumValue: value,
      });
    }
    out.push({
      label: lastSegment,
      documentation,
      kind: "field",
    });
  }
  return out;
}

/**
 * Returns the description for a property named `word`, walking the schema
 * looking for any property of that name. Returns null when the word is
 * not a known property — the Monaco hover provider then returns null and
 * the user sees no popup (rather than a misleading default message).
 */
export function hoverDocumentationFor(schema: RuleSchema, word: string): string | null {
  const target = word.trim();
  if (!target) return null;
  const stack: SchemaNode[] = [schema as SchemaNode];
  while (stack.length > 0) {
    const node = stack.pop() as SchemaNode;
    if (node.properties) {
      for (const [key, child] of Object.entries(node.properties)) {
        if (key === target) {
          const description = (child.description ?? "").trim();
          if (description) return description;
        }
        stack.push(asNode(child));
      }
    }
    if (node.items) stack.push(asNode(node.items));
    if (Array.isArray(node.anyOf)) {
      for (const branch of node.anyOf) stack.push(asNode(branch));
    }
  }
  return null;
}
