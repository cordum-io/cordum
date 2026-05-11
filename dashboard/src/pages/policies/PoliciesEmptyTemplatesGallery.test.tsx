import { describe, expect, it } from "vitest";
import { Route, Routes } from "react-router-dom";
import { renderWithProviders, screen } from "@/test-utils/render";
import { RULE_TEMPLATES } from "@/lib/policy-studio/templates";
import { PoliciesEmptyTemplatesGallery } from "./PoliciesEmptyTemplatesGallery";

function renderGallery() {
  return renderWithProviders(
    <Routes>
      <Route path="/policies" element={<PoliciesEmptyTemplatesGallery />} />
    </Routes>,
    { initialEntries: ["/policies"] },
  );
}

describe("PoliciesEmptyTemplatesGallery", () => {
  it("renders 6+ template cards spanning all four rule types (DoD #1)", () => {
    renderGallery();
    expect(RULE_TEMPLATES.length).toBeGreaterThanOrEqual(6);
    const types = new Set(RULE_TEMPLATES.map((t) => t.ruleType));
    expect(types.has("input")).toBe(true);
    expect(types.has("output")).toBe(true);
    expect(types.has("velocity")).toBe(true);
    expect(types.has("edge")).toBe(true);
    for (const template of RULE_TEMPLATES) {
      expect(
        document.querySelector(`[data-template-id="${template.id}"]`),
      ).not.toBeNull();
    }
  });

  it("each card is a router Link to /policies?new=true&type=<ruleType>&template=<id>&open=editor (DoD #2)", () => {
    renderGallery();
    for (const template of RULE_TEMPLATES) {
      const card = document.querySelector(
        `a[data-template-id="${template.id}"]`,
      );
      expect(card).not.toBeNull();
      const href = card!.getAttribute("href") ?? "";
      const url = new URL(href, "http://test/");
      expect(url.pathname).toBe("/policies");
      expect(url.searchParams.get("new")).toBe("true");
      expect(url.searchParams.get("type")).toBe(template.ruleType);
      expect(url.searchParams.get("template")).toBe(template.id);
      expect(url.searchParams.get("open")).toBe("editor");
    }
  });

  it("renders template label + description text so authors can scan-pick", () => {
    renderGallery();
    for (const template of RULE_TEMPLATES) {
      expect(screen.getByText(template.label)).not.toBeNull();
      expect(screen.getByText(template.description)).not.toBeNull();
    }
  });

  it("uses a section role with an accessible heading", () => {
    renderGallery();
    const region = document.querySelector(
      "section[aria-labelledby='policies-empty-templates-heading']",
    );
    expect(region).not.toBeNull();
    const heading = document.getElementById(
      "policies-empty-templates-heading",
    );
    expect(heading?.textContent ?? "").toMatch(/Start from a template/i);
  });

  it("each card exposes an aria-label that names the template + rule type for screen readers", () => {
    renderGallery();
    for (const template of RULE_TEMPLATES) {
      const card = document.querySelector(
        `a[data-template-id="${template.id}"]`,
      );
      expect(card).not.toBeNull();
      const label = card!.getAttribute("aria-label") ?? "";
      expect(label).toMatch(new RegExp(template.label));
    }
  });
});
