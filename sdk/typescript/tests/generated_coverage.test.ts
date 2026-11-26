import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { Project, type SourceFile } from "ts-morph";
import { describe, expect, it } from "vitest";

const HTTP_METHOD_PATTERN = /^\s{8}(get|put|post|delete|options|head|patch|trace):/gm;

function countGeneratedOperations(pathsText: string): number {
  return (pathsText.match(HTTP_METHOD_PATTERN) ?? []).length;
}

function specOperationIds(text: string): string[] {
  const ids = [...text.matchAll(/^\s*operationId:\s*["']?([A-Za-z0-9_.-]+)["']?\s*$/gm)]
    .map((match) => {
      if (!match[1]) throw new Error("Missing operationId capture");
      return match[1];
    });
  expect(ids.length).toBeGreaterThan(0);
  expect(new Set(ids).size).toBe(ids.length);
  return ids;
}

function pathOperationIds(pathsText: string): string[] {
  return [...pathsText.matchAll(
    /^\s{8}(?:get|put|post|delete|options|head|patch|trace): operations\["([^"]+)"\];$/gm,
  )].map((match) => {
    if (!match[1]) throw new Error("Missing operationId capture");
    return match[1];
  });
}

function operationTypeIds(sourceFile: SourceFile): string[] {
  return sourceFile.getInterfaceOrThrow("operations").getProperties()
    .map((property) => property.getName().replace(/^["']|["']$/g, ""));
}

function diffIds(expected: string[], actual: string[]): { missing: string[]; extra: string[] } {
  return {
    missing: [...new Set(expected)].filter((id) => !actual.includes(id)).sort(),
    extra: [...new Set(actual)].filter((id) => !expected.includes(id)).sort(),
  };
}

function loadSchema(text = readFileSync(resolve("src", "_generated", "schema.d.ts"), "utf8")) {
  return new Project({ useInMemoryFileSystem: true }).createSourceFile("schema.d.ts", text);
}

const specIds = specOperationIds(readFileSync(
  resolve("..", "..", "docs", "api", "openapi", "cordum-api.yaml"), "utf8",
));
const regressionIds = ["ingestEdgeLLMEvents", "resolveMemoryResource"];

describe("generated schema coverage", () => {
  it("tracks every operationId from the OpenAPI spec", () => {
    const specPath = resolve("..", "..", "docs", "api", "openapi", "cordum-api.yaml");
    const schemaPath = resolve("src", "_generated", "schema.d.ts");

    const operationCount = (readFileSync(specPath, "utf8").match(/operationId:/g) ?? []).length;

    const project = new Project({ useInMemoryFileSystem: false });
    const sourceFile = project.addSourceFileAtPath(schemaPath);
    const pathsInterface = sourceFile.getInterfaceOrThrow("paths");

    expect(countGeneratedOperations(pathsInterface.getText())).toBe(operationCount);
  });

  it("matches spec ids in both generated paths and operation types", () => {
    const schema = loadSchema();
    expect(diffIds(specIds, pathOperationIds(schema.getInterfaceOrThrow("paths").getText())))
      .toEqual({ missing: [], extra: [] });
    expect(diffIds(specIds, operationTypeIds(schema))).toEqual({ missing: [], extra: [] });
  });

  it.each(regressionIds)("includes the %s regression in every API surface", (id) => {
    const schema = loadSchema();
    expect(specIds).toContain(id);
    expect(pathOperationIds(schema.getInterfaceOrThrow("paths").getText())).toContain(id);
    expect(operationTypeIds(schema)).toContain(id);
  });

  it.each(regressionIds)("detects removal of %s from paths and types", (id) => {
    const original = readFileSync(resolve("src", "_generated", "schema.d.ts"), "utf8");
    const changed = original.replace(
      new RegExp(`^( {8})(get|put|post|delete|options|head|patch|trace): operations\\["${id}"\\];$`, "m"),
      "$1$2?: never;",
    );
    expect(changed).not.toBe(original);
    const schema = loadSchema(changed);
    schema.getInterfaceOrThrow("operations").getPropertyOrThrow(id).remove();
    const pathsText = schema.getInterfaceOrThrow("paths").getText();
    expect(diffIds(specIds, pathOperationIds(pathsText))).toEqual({ missing: [id], extra: [] });
    expect(diffIds(specIds, operationTypeIds(schema))).toEqual({ missing: [id], extra: [] });
    expect(countGeneratedOperations(pathsText)).toBe(specIds.length - 1);
  });
});
