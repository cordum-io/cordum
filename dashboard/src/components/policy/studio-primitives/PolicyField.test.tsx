import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { PolicyField } from "./PolicyField";

describe("PolicyField", () => {
  it("renders required marker, hint, help text, and validation error", () => {
    const markup = renderToStaticMarkup(
      <PolicyField
        inputId="rule-id"
        label="Rule ID"
        required
        helpText="Rule identifier help"
        hint="Use lowercase slug."
        error="Rule ID is required."
      >
        <input />
      </PolicyField>,
    );

    expect(markup).toContain("Rule ID");
    expect(markup).toContain("Rule identifier help");
    expect(markup).toContain("Use lowercase slug.");
    expect(markup).toContain("Rule ID is required.");
    expect(markup).toContain("(required)");
  });
});
