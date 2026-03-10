import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { __policySectionInternal, PolicySection } from "./PolicySection";

describe("PolicySection", () => {
  it("toggles open state helper correctly", () => {
    expect(__policySectionInternal.toggleSectionOpen(true)).toBe(false);
    expect(__policySectionInternal.toggleSectionOpen(false)).toBe(true);
  });

  it("renders collapsed section semantics", () => {
    const markup = renderToStaticMarkup(
      <PolicySection title="Constraints" open={false}>
        <div>Body content</div>
      </PolicySection>,
    );
    expect(markup).toContain("aria-expanded=\"false\"");
    expect(markup).not.toContain("Body content");
  });
});
