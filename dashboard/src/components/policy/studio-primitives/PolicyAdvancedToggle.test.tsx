import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import {
  __policyAdvancedToggleInternal,
  PolicyAdvancedToggle,
} from "./PolicyAdvancedToggle";

describe("PolicyAdvancedToggle", () => {
  it("computes next toggle state deterministically", () => {
    expect(__policyAdvancedToggleInternal.nextAdvancedOpenState(false)).toBe(true);
    expect(__policyAdvancedToggleInternal.nextAdvancedOpenState(true)).toBe(false);
  });

  it("renders configured count badge with aria-pressed state", () => {
    const markup = renderToStaticMarkup(
      <PolicyAdvancedToggle open configuredCount={3} onToggle={() => {}} />,
    );
    expect(markup).toContain("3 configured");
    expect(markup).toContain("aria-pressed=\"true\"");
  });
});
