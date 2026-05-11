import { describe, expect, it } from "vitest";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { GovernOverviewRedirect } from "@/App";

function makeRouter(path: string) {
  return createMemoryRouter(
    [
      { path: "/govern/overview", element: <GovernOverviewRedirect /> },
      { path: "*", element: null },
    ],
    { initialEntries: [path] },
  );
}

function renderRouter(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = makeRouter(path);
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router.state.location;
}

describe("GovernOverviewRedirect — /govern/overview → /policies/* mapping", () => {
  it("no tab → /policies", () => {
    const loc = renderRouter("/govern/overview");
    expect(loc.pathname).toBe("/policies");
    expect(new URLSearchParams(loc.search).get("type")).toBeNull();
  });

  it("?tab=input-rules → /policies?type=input", () => {
    const loc = renderRouter("/govern/overview?tab=input-rules");
    expect(loc.pathname).toBe("/policies");
    expect(new URLSearchParams(loc.search).get("type")).toBe("input");
  });

  it("?tab=output-rules → /policies?type=output", () => {
    const loc = renderRouter("/govern/overview?tab=output-rules");
    expect(loc.pathname).toBe("/policies");
    expect(new URLSearchParams(loc.search).get("type")).toBe("output");
  });

  it("?tab=velocity → /policies?type=velocity", () => {
    const loc = renderRouter("/govern/overview?tab=velocity");
    expect(loc.pathname).toBe("/policies");
    expect(new URLSearchParams(loc.search).get("type")).toBe("velocity");
  });

  it("?tab=velocity-rules → /policies?type=velocity", () => {
    const loc = renderRouter("/govern/overview?tab=velocity-rules");
    expect(loc.pathname).toBe("/policies");
    expect(new URLSearchParams(loc.search).get("type")).toBe("velocity");
  });

  it("?tab=bundles → /policies/bundles", () => {
    const loc = renderRouter("/govern/overview?tab=bundles");
    expect(loc.pathname).toBe("/policies/bundles");
    expect(new URLSearchParams(loc.search).get("view")).toBeNull();
  });

  it("?tab=scope → /policies/bundles?view=scope", () => {
    const loc = renderRouter("/govern/overview?tab=scope");
    expect(loc.pathname).toBe("/policies/bundles");
    expect(new URLSearchParams(loc.search).get("view")).toBe("scope");
  });

  it("?tab=evaluation → /policies/decisions", () => {
    const loc = renderRouter("/govern/overview?tab=evaluation");
    expect(loc.pathname).toBe("/policies/decisions");
    expect(new URLSearchParams(loc.search).get("mode")).toBeNull();
  });

  it("?tab=evaluation&mode=replay → /policies/decisions?mode=replay", () => {
    const loc = renderRouter("/govern/overview?tab=evaluation&mode=replay");
    expect(loc.pathname).toBe("/policies/decisions");
    expect(new URLSearchParams(loc.search).get("mode")).toBe("replay");
  });

  it("?tab=evaluation&mode=simulator → /policies/decisions?mode=simulator", () => {
    const loc = renderRouter("/govern/overview?tab=evaluation&mode=simulator");
    expect(loc.pathname).toBe("/policies/decisions");
    expect(new URLSearchParams(loc.search).get("mode")).toBe("simulator");
  });

  it("strips tab/mode but preserves other params", () => {
    const loc = renderRouter("/govern/overview?tab=input-rules&foo=bar");
    expect(loc.pathname).toBe("/policies");
    const params = new URLSearchParams(loc.search);
    expect(params.get("type")).toBe("input");
    expect(params.get("tab")).toBeNull();
    expect(params.get("foo")).toBe("bar");
  });
});
