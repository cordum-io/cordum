import { describe, it, expect } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { renderWithProviders } from "../../test-utils/render";
import { server } from "../../test-utils/msw";
import BundleDeploymentsTab from "./BundleDeploymentsTab";

describe("BundleDeploymentsTab — Dashboard 5 step 7", () => {
  it("renders the empty state when the bundle has no versions or deployments", async () => {
    const { findByText } = renderWithProviders(<BundleDeploymentsTab bundleId="b-1" />);
    expect(await findByText("No deployments yet")).toBeTruthy();
  });

  it("renders the scope×version matrix with active cells highlighted", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles/:id/versions", () =>
        HttpResponse.json({
          items: [
            { version: "v3", deployed_at: "2026-05-09T10:00:00Z" },
            { version: "v2", deployed_at: "2026-05-08T10:00:00Z" },
            { version: "v1", deployed_at: "2026-05-07T10:00:00Z" },
          ],
        }),
      ),
      http.get("*/api/v1/policy/bundles/:id/deployments", () =>
        HttpResponse.json({
          items: [
            {
              scope: "global",
              scope_kind: "global",
              version: "v3",
              active: true,
              deployed_at: "2026-05-09T10:00:00Z",
            },
            {
              scope: "tenant:acme",
              scope_kind: "tenant",
              scope_value: "acme",
              version: "v2",
              active: true,
              deployed_at: "2026-05-08T10:00:00Z",
            },
          ],
        }),
      ),
    );
    renderWithProviders(<BundleDeploymentsTab bundleId="b-1" />);

    // Column headers (versions newest-first).
    expect(await screen.findByRole("columnheader", { name: "v3" })).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: "v2" })).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: "v1" })).toBeTruthy();

    // Row headers (scopes alphabetical).
    expect(screen.getByRole("rowheader", { name: "global" })).toBeTruthy();
    expect(screen.getByRole("rowheader", { name: "tenant:acme" })).toBeTruthy();

    // Active cells expose "Rollback" button label; inactive cells expose "Promote".
    expect(
      screen.getByRole("button", { name: "Rollback global from v3" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Promote v2 to global" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Rollback tenant:acme from v2" }),
    ).toBeTruthy();
  });

  it("opens DeployBundleModal pre-filled when a non-active matrix cell is clicked (DoD #1)", async () => {
    // QA reopen #1 finding: deploy modal must open from BOTH Versions and
    // Deployments tabs. Previously the Deployments-tab matrix click went
    // straight to ConfirmDialog and never opened DeployBundleModal — this
    // missed the DoD's scope picker + edge-mode picker requirement.
    server.use(
      http.get("*/api/v1/policy/bundles/:id/versions", () =>
        HttpResponse.json({
          items: [{ version: "v2", deployed_at: "2026-05-09T10:00:00Z" }],
        }),
      ),
      http.get("*/api/v1/policy/bundles/:id/deployments", () =>
        HttpResponse.json({
          items: [
            {
              scope: "tenant:acme",
              scope_kind: "tenant",
              scope_value: "acme",
              version: "v1",
              active: true,
              deployed_at: "2026-05-08T10:00:00Z",
            },
          ],
        }),
      ),
      http.get("*/api/v1/policy/bundles/:id", ({ params }) =>
        HttpResponse.json({
          id: String(params.id),
          name: `bundle-${String(params.id)}`,
          rule_ids: [],
          scope_binding: { kind: "global" },
          versions: [],
        }),
      ),
    );
    renderWithProviders(<BundleDeploymentsTab bundleId="b-1" />);

    const promoteBtn = await screen.findByRole("button", {
      name: "Promote v2 to tenant:acme",
    });
    fireEvent.click(promoteBtn);

    // DeployBundleModal opens — its "Scope kind" select is the modal's
    // canonical affordance and ConfirmDialog has no such field, so its
    // presence proves the modal mounted.
    const scopeSelect = (await screen.findByLabelText(
      "Scope kind",
    )) as HTMLSelectElement;
    expect(scopeSelect).toBeTruthy();
    // Pre-filled with the cell's scope: tenant + acme.
    expect(scopeSelect.value).toBe("tenant");
    const valueInput = (await screen.findByLabelText(
      "Scope value",
    )) as HTMLInputElement;
    expect(valueInput.value).toBe("acme");
    // Modal header shows the prefilled version too.
    expect(
      await screen.findByRole("heading", { name: /v2/i }),
    ).toBeTruthy();
  });

  it("opens ConfirmDialog (rollback semantics) when an active matrix cell is clicked", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles/:id/versions", () =>
        HttpResponse.json({
          items: [{ version: "v1", deployed_at: "2026-05-08T10:00:00Z" }],
        }),
      ),
      http.get("*/api/v1/policy/bundles/:id/deployments", () =>
        HttpResponse.json({
          items: [
            {
              scope: "global",
              scope_kind: "global",
              version: "v1",
              active: true,
              deployed_at: "2026-05-08T10:00:00Z",
            },
          ],
        }),
      ),
    );
    renderWithProviders(<BundleDeploymentsTab bundleId="b-1" />);

    const rollbackBtn = await screen.findByRole("button", {
      name: "Rollback global from v1",
    });
    fireEvent.click(rollbackBtn);

    // ConfirmDialog opens with rollback semantics (no scope picker).
    await waitFor(() => {
      expect(screen.getByText(/Rollback global\?/)).toBeTruthy();
    });
    expect(
      screen.getByRole("button", { name: /^Rollback$/ }),
    ).toBeTruthy();
    // No "Scope kind" select present — the rollback path keeps the
    // simpler ConfirmDialog flow on purpose.
    expect(screen.queryByLabelText("Scope kind")).toBeNull();
  });
});
