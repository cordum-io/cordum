import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { Project } from "ts-morph";
import { describe, expect, it } from "vitest";

const HTTP_METHOD_PATTERN = /^\s{8}(get|put|post|delete|options|head|patch|trace):/gm;

function countGeneratedOperations(pathsText: string): number {
  return (pathsText.match(HTTP_METHOD_PATTERN) ?? []).length;
}

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
});
