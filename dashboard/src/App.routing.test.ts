import { describe, expect, it } from "vitest";
import appSource from "./App.tsx?raw";

// Regression guard for missing-route bugs.
//
// Page-level tests (e.g. GovernanceVerificationPage.test.tsx) render the
// component inside a hand-rolled <Routes> block and never import App.tsx, so
// they cannot detect when a page exists but was never wired into the
// production router. This source-regex gate covers that gap by asserting
// that App.tsx both lazy-imports the component and registers the route.
//
// To add a route to the guard, append to REGISTERED_ROUTES with a short
// `why` that points to the originating task or surface.

const REGISTERED_ROUTES: ReadonlyArray<{
  path: string;
  componentName: string;
  why: string;
}> = [
  {
    path: "/govern/verification",
    componentName: "GovernanceVerificationPage",
    why:
      "task-14d012e6 ChainIntegrityWidget page; the page test renders <Routes> in isolation, so missing App.tsx registration produced a silent 404 in prod.",
  },
];

describe("App.tsx route registration (regression guard)", () => {
  for (const { path: routePath, componentName, why } of REGISTERED_ROUTES) {
    it(`registers ${routePath} -> ${componentName}`, () => {
      const escaped = routePath.replace(/\//g, "\\/");
      expect(
        appSource,
        `App.tsx must register a <Route path="${routePath}" /> entry.\nWhy: ${why}`,
      ).toMatch(new RegExp(`path=["']${escaped}["']`));
      expect(
        appSource,
        `App.tsx must lazy-import / reference ${componentName}.\nWhy: ${why}`,
      ).toContain(componentName);
    });
  }
});
