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

// Source-regex assertions for redirect routes — these don't have a page
// component (just a <Navigate> child), so REGISTERED_ROUTES isn't the
// right shape. Each entry asserts the path is registered AND that the
// Navigate target matches the expected destination query string.
describe("App.tsx redirect routes (Edge deep-link + DLQ preservation)", () => {
  it("registers /edge/approvals redirecting to /approvals?lane=edge", () => {
    expect(
      appSource,
      "App.tsx must preserve /edge/approvals → /approvals?lane=edge for bookmarked Edge deep links.",
    ).toMatch(/path=["']\/edge\/approvals["']/);
    expect(
      appSource,
      "The /edge/approvals route must Navigate to /approvals?lane=edge (preserves Edge-scoped filter via querystring).",
    ).toMatch(/to=["']\/approvals\?lane=edge["']/);
  });

  it("registers /edge/audit redirecting to /audit?event_type_prefix=edge", () => {
    expect(
      appSource,
      "App.tsx must preserve /edge/audit → /audit?event_type_prefix=edge for bookmarked Edge deep links.",
    ).toMatch(/path=["']\/edge\/audit["']/);
    expect(
      appSource,
      "The /edge/audit route must Navigate to /audit?event_type_prefix=edge.",
    ).toMatch(/to=["']\/audit\?event_type_prefix=edge["']/);
  });

  it("preserves /dlq → /jobs?status=dlq redirect after Dead Letters sidebar entry removal", () => {
    // Dead Letters was removed from the Audit sidebar (task-266f21ad)
    // because DLQ content is folded into JobsPage as ?status=dlq
    // (task-0bcb9411). Bookmarked /dlq URLs must still resolve.
    expect(
      appSource,
      "App.tsx must still register /dlq even after the sidebar Dead Letters entry was removed.",
    ).toMatch(/path=["']\/dlq["']/);
    expect(
      appSource,
      "The /dlq route must still render DlqRouteRedirect → /jobs?status=dlq for bookmark preservation.",
    ).toContain("DlqRouteRedirect");
  });
});
