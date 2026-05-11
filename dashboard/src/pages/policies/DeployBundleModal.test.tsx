import { describe, it, expect } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { renderWithProviders } from "../../test-utils/render";
import { server } from "../../test-utils/msw";
import DeployBundleModal from "./DeployBundleModal";

// Bundle fixture stub for `useBundle` calls inside the modal — exposes a
// pre-set Metadata.EdgeMode so the EdgeMode-default-from-bundle case can
// assert.
function bundleHandler(edgeMode: string | undefined) {
  return http.get("*/api/v1/policy/bundles/:id", ({ params }) =>
    HttpResponse.json({
      id: String(params.id),
      name: `bundle-${String(params.id)}`,
      rule_ids: [],
      scope_binding: { kind: "global" },
      versions: [],
      ...(edgeMode ? { metadata: { edge_mode: edgeMode } } : {}),
    }),
  );
}

describe("DeployBundleModal — Dashboard 7", () => {
  it("renders all 5 scope kinds in the picker", async () => {
    server.use(bundleHandler(undefined));
    renderWithProviders(
      <DeployBundleModal bundleId="b-1" version="v1" open={true} onClose={() => {}} />,
    );
    const scopeSelect = await screen.findByLabelText("Scope kind");
    expect(scopeSelect).toBeTruthy();
    // 5 expected option values
    for (const v of ["global", "tenant", "workflow", "edge_fleet", "edge_user"]) {
      expect(scopeSelect.querySelector(`option[value="${v}"]`)).toBeTruthy();
    }
  });

  it("disables the scope-value input when scopeKind=global", async () => {
    server.use(bundleHandler(undefined));
    renderWithProviders(
      <DeployBundleModal bundleId="b-1" version="v1" open={true} onClose={() => {}} />,
    );
    const valueInput = (await screen.findByLabelText("Scope value")) as HTMLInputElement;
    expect(valueInput.disabled).toBe(true);
  });

  it("hides EdgeMode picker for non-edge scopes; shows it for edge_fleet/edge_user", async () => {
    server.use(bundleHandler("enforce"));
    renderWithProviders(
      <DeployBundleModal bundleId="b-1" version="v1" open={true} onClose={() => {}} />,
    );
    const scopeSelect = (await screen.findByLabelText("Scope kind")) as HTMLSelectElement;
    // Non-edge scopes hide the picker.
    fireEvent.change(scopeSelect, { target: { value: "tenant" } });
    expect(screen.queryByLabelText("Edge mode")).toBeNull();
    // edge_fleet → picker visible.
    fireEvent.change(scopeSelect, { target: { value: "edge_fleet" } });
    await waitFor(() => {
      expect(screen.queryByLabelText("Edge mode")).not.toBeNull();
    });
    // edge_user → picker visible.
    fireEvent.change(scopeSelect, { target: { value: "edge_user" } });
    await waitFor(() => {
      expect(screen.queryByLabelText("Edge mode")).not.toBeNull();
    });
    // Back to global → picker hidden.
    fireEvent.change(scopeSelect, { target: { value: "global" } });
    await waitFor(() => {
      expect(screen.queryByLabelText("Edge mode")).toBeNull();
    });
  });

  it("Deploy click → ConfirmDialog → confirm fires the mutation; onSuccess + onClose called", async () => {
    let deployBody: unknown = null;
    server.use(
      bundleHandler(undefined),
      http.post("*/api/v1/policy/bundles/:id/deploy", async ({ request }) => {
        deployBody = await request.json();
        return HttpResponse.json({});
      }),
    );
    let closed = false;
    let success = false;
    renderWithProviders(
      <DeployBundleModal
        bundleId="b-1"
        version="v3"
        open={true}
        onClose={() => {
          closed = true;
        }}
        onSuccess={() => {
          success = true;
        }}
      />,
    );

    // Pick tenant + fill value
    const scopeSelect = (await screen.findByLabelText("Scope kind")) as HTMLSelectElement;
    fireEvent.change(scopeSelect, { target: { value: "tenant" } });
    const valueInput = (await screen.findByLabelText("Scope value")) as HTMLInputElement;
    fireEvent.change(valueInput, { target: { value: "acme" } });

    // Click Deploy → ConfirmDialog opens
    const deployBtn = await screen.findByRole("button", {
      name: /^Deploy v3 to tenant:acme$/,
    });
    fireEvent.click(deployBtn);
    expect(
      await screen.findByText(/Deploy bundle-b-1 v3 to tenant:acme\?/),
    ).toBeTruthy();

    // Confirm → mutation fires
    const confirmBtn = screen.getAllByRole("button", { name: /^Deploy$/ }).pop()!;
    fireEvent.click(confirmBtn);
    await waitFor(() => expect(success).toBe(true));
    expect(closed).toBe(true);
    expect(deployBody).toEqual({
      version: "v3",
      scope: { kind: "tenant", value: "acme" },
    });
  });

  it("Cancel button closes without firing the mutation", async () => {
    let mutationFired = false;
    server.use(
      bundleHandler(undefined),
      http.post("*/api/v1/policy/bundles/:id/deploy", () => {
        mutationFired = true;
        return HttpResponse.json({});
      }),
    );
    let closed = false;
    renderWithProviders(
      <DeployBundleModal
        bundleId="b-1"
        version="v1"
        open={true}
        onClose={() => {
          closed = true;
        }}
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    expect(closed).toBe(true);
    expect(mutationFired).toBe(false);
  });
});
