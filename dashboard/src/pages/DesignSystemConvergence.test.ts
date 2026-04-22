import { describe, expect, it } from "vitest";
import jobDetailSource from "./JobDetailPage.tsx?raw";
import schemaDetailSource from "./SchemaDetailPage.tsx?raw";
import schemasPageSource from "./SchemasPage.tsx?raw";

describe("design-system convergence regressions", () => {
  it("keeps the schema surfaces off raw form controls", () => {
    expect(schemaDetailSource).not.toMatch(/<input\b/);
    expect(schemaDetailSource).not.toMatch(/<select\b/);
    expect(schemaDetailSource).not.toMatch(/type=\"checkbox\"/);
    expect(schemasPageSource).not.toMatch(/<input\b/);
  });

  it("keeps job detail status styling on shared tokens instead of page-local CSS vars", () => {
    expect(jobDetailSource).not.toMatch(/var\(--color-/);
  });
});
