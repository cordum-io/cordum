import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { renderWithProviders } from "../../test-utils/render";
import { server } from "../../test-utils/msw";
import PublishToBundleModal from "./PublishToBundleModal";

// Stub bundle list for the picker. Two scope variants prove the
// rendered metadata strip mirrors scope_binding faithfully.
function bundlesHandler() {
  return http.get("*/api/v1/policy/bundles", () =>
    HttpResponse.json({
      items: [
        {
          id: "bundle-acme-input",
          name: "Acme — input rules",
          rule_ids: ["rule.input.aws-secret"],
          scope_binding: { kind: "tenant", value: "acme" },
          versions: [],
        },
        {
          id: "bundle-edge-global",
          name: "Edge global",
          rule_ids: [],
          scope_binding: { kind: "global" },
          versions: [],
        },
      ],
      total: 2,
    }),
  );
}

describe("PublishToBundleModal — Phase 3E", () => {
  it("renders both bundles in the picker with name + scope + rule count", async () => {
    server.use(bundlesHandler());
    renderWithProviders(
      <PublishToBundleModal
        ruleId="rule.demo"
        open={true}
        onClose={() => {}}
      />,
    );
    expect(await screen.findByText("Acme — input rules")).not.toBeNull();
    expect(await screen.findByText("Edge global")).not.toBeNull();
    expect(screen.getByText(/Scope: tenant:acme/)).not.toBeNull();
    expect(screen.getByText(/Scope: global/)).not.toBeNull();
  });

  it("renders the empty state with a disabled '+ New bundle' CTA when the list is empty", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles", () =>
        HttpResponse.json({ items: [], total: 0 }),
      ),
    );
    renderWithProviders(
      <PublishToBundleModal
        ruleId="rule.demo"
        open={true}
        onClose={() => {}}
      />,
    );
    expect(await screen.findByText(/No bundles yet/i)).not.toBeNull();
    const cta = screen.getByRole("button", {
      name: /\+ New bundle \(Bundle Studio\)/i,
    });
    expect((cta as HTMLButtonElement).disabled).toBe(true);
  });

  it("renders the error banner when the bundle list endpoint 500s", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles", () =>
        HttpResponse.json({ error: "boom" }, { status: 500 }),
      ),
    );
    renderWithProviders(
      <PublishToBundleModal
        ruleId="rule.demo"
        open={true}
        onClose={() => {}}
      />,
    );
    expect(
      await screen.findByText(/Couldn['’]t load bundles/i),
    ).not.toBeNull();
  });

  it("disables Publish until a bundle is picked, then enables on selection", async () => {
    server.use(bundlesHandler());
    renderWithProviders(
      <PublishToBundleModal
        ruleId="rule.demo"
        open={true}
        onClose={() => {}}
      />,
    );
    const submit = (await screen.findByRole("button", {
      name: /Publish to bundle/i,
    })) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    const radio = await screen.findByDisplayValue("bundle-acme-input");
    fireEvent.click(radio);
    await waitFor(() => expect(submit.disabled).toBe(false));
  });

  it("on rule_not_found 404 shows the disambiguated 'rule not saved yet' copy", async () => {
    server.use(bundlesHandler());
    server.use(
      http.post("*/api/v1/policy/bundles/:id/rules", () =>
        HttpResponse.json({ error: "rule_not_found" }, { status: 404 }),
      ),
    );
    renderWithProviders(
      <PublishToBundleModal
        ruleId="rule.demo"
        open={true}
        onClose={() => {}}
      />,
    );
    const radio = await screen.findByDisplayValue("bundle-acme-input");
    fireEvent.click(radio);
    fireEvent.click(
      await screen.findByRole("button", { name: /Publish to bundle/i }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: /^Publish$/i }),
    );
    await waitFor(() =>
      expect(
        screen.getByText(/isn['’]t saved on the server yet/i),
      ).not.toBeNull(),
    );
  });

  it("on bundle_not_found 404 shows the disambiguated 'bundle deleted' copy", async () => {
    server.use(bundlesHandler());
    server.use(
      http.post("*/api/v1/policy/bundles/:id/rules", () =>
        HttpResponse.json({ error: "bundle_not_found" }, { status: 404 }),
      ),
    );
    renderWithProviders(
      <PublishToBundleModal
        ruleId="rule.demo"
        open={true}
        onClose={() => {}}
      />,
    );
    const radio = await screen.findByDisplayValue("bundle-acme-input");
    fireEvent.click(radio);
    fireEvent.click(
      await screen.findByRole("button", { name: /Publish to bundle/i }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: /^Publish$/i }),
    );
    await waitFor(() =>
      expect(
        screen.getByText(/was deleted while this dialog was open/i),
      ).not.toBeNull(),
    );
  });

  it("on success calls onSuccess + onClose", async () => {
    server.use(bundlesHandler());
    const onClose = vi.fn();
    const onSuccess = vi.fn();
    renderWithProviders(
      <PublishToBundleModal
        ruleId="rule.demo"
        open={true}
        onClose={onClose}
        onSuccess={onSuccess}
      />,
    );
    const radio = await screen.findByDisplayValue("bundle-acme-input");
    fireEvent.click(radio);
    fireEvent.click(
      await screen.findByRole("button", { name: /Publish to bundle/i }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: /^Publish$/i }),
    );
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
    expect(onClose).toHaveBeenCalled();
  });
});
