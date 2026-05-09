import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { MemoryRouter, Routes, Route, useLocation } from "react-router-dom";
import { GovernOverviewRedirect } from "./App";

/**
 * Renders GovernOverviewRedirect under MemoryRouter at the given URL and
 * surfaces the resulting location (after the redirect) so each case can
 * assert pathname + search against the spec mapping.
 */
function LocationProbe() {
  const location = useLocation();
  return (
    <div data-testid="probe">
      {location.pathname}
      {location.search}
    </div>
  );
}

function renderRedirectAt(initialUrl: string): string {
  const { getByTestId } = render(
    <MemoryRouter initialEntries={[initialUrl]}>
      <Routes>
        <Route path="/govern/overview" element={<GovernOverviewRedirect />} />
        <Route path="*" element={<LocationProbe />} />
      </Routes>
    </MemoryRouter>,
  );
  return getByTestId("probe").textContent ?? "";
}

describe("GovernOverviewRedirect — /govern/overview ?tab= mapping (epic-d9a6c0a1 Dashboard 1)", () => {
  // Per QA reopen #1 rejectionDetails (task-5d354964): "table-driven cases for
  // every old tab value". Spec mapping in App.tsx GovernOverviewRedirect:
  //   input-rules        → /policies?type=input
  //   output-rules       → /policies?type=output
  //   velocity           → /policies?type=velocity
  //   velocity-rules     → /policies?type=velocity (legacy spelling)
  //   bundles            → /policies/bundles
  //   scope              → /policies/bundles?view=scope
  //   evaluation         → /policies/decisions  (preserves ?mode= when set)
  //   <missing|unknown>  → /policies (default Rules surface)
  it.each([
    ["input-rules — type=input", "/govern/overview?tab=input-rules", "/policies?type=input"],
    ["output-rules — type=output", "/govern/overview?tab=output-rules", "/policies?type=output"],
    ["velocity — type=velocity", "/govern/overview?tab=velocity", "/policies?type=velocity"],
    [
      "velocity-rules legacy spelling — type=velocity",
      "/govern/overview?tab=velocity-rules",
      "/policies?type=velocity",
    ],
    ["bundles — /policies/bundles", "/govern/overview?tab=bundles", "/policies/bundles"],
    [
      "scope — /policies/bundles?view=scope",
      "/govern/overview?tab=scope",
      "/policies/bundles?view=scope",
    ],
    [
      "evaluation — /policies/decisions",
      "/govern/overview?tab=evaluation",
      "/policies/decisions",
    ],
    [
      "evaluation+mode=replay — preserves mode",
      "/govern/overview?tab=evaluation&mode=replay",
      "/policies/decisions?mode=replay",
    ],
    [
      "evaluation+mode=simulator — preserves mode",
      "/govern/overview?tab=evaluation&mode=simulator",
      "/policies/decisions?mode=simulator",
    ],
    [
      "evaluation+mode=analytics — preserves mode",
      "/govern/overview?tab=evaluation&mode=analytics",
      "/policies/decisions?mode=analytics",
    ],
    ["missing tab — /policies default", "/govern/overview", "/policies"],
    [
      "unknown tab — /policies default",
      "/govern/overview?tab=does-not-exist",
      "/policies",
    ],
  ])("%s", (_label, initialUrl, expected) => {
    expect(renderRedirectAt(initialUrl)).toBe(expected);
  });

  it("preserves unrelated query params across the redirect", () => {
    // Bookmarks may include arbitrary trailing params (e.g. analytics
    // tracking). The redirect must not strip them.
    expect(renderRedirectAt("/govern/overview?tab=input-rules&utm_source=docs")).toBe(
      "/policies?utm_source=docs&type=input",
    );
  });
});
