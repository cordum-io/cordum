import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { EntitlementGate } from "./EntitlementGate";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    license: {} as any,
  },
}));

vi.mock("@/hooks/useLicense", () => ({
  useLicense: () => hookState.license,
}));

function render(ui: React.ReactNode) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => { root.render(ui); });
  return {
    container,
    cleanup: () => { act(() => root.unmount()); container.remove(); },
  };
}

describe("EntitlementGate", () => {
  beforeEach(() => {
    hookState.license = {
      data: {
        plan: "enterprise",
        entitlements: { saml: true, rbac: true, siemExport: true },
      },
      isLoading: false,
      isError: false,
    };
  });

  it("renders children when entitlement is true", () => {
    const { container, cleanup } = render(
      <EntitlementGate entitlement="saml" label="SAML SSO">
        <div data-testid="child">Gated content</div>
      </EntitlementGate>,
    );
    expect(container.textContent).toContain("Gated content");
    // Should NOT show upgrade prompt
    expect(container.textContent).not.toContain("View pricing");
    cleanup();
  });

  it("renders upgrade prompt when entitlement is false", () => {
    hookState.license.data.entitlements.saml = false;
    const { container, cleanup } = render(
      <EntitlementGate entitlement="saml" label="SAML SSO" description="Enterprise identity provider">
        <div>Hidden content</div>
      </EntitlementGate>,
    );
    expect(container.textContent).toContain("SAML SSO");
    expect(container.textContent).toContain("Enterprise identity provider");
    expect(container.textContent).toContain("View pricing");
    cleanup();
  });

  it("renders children during loading (fail open)", () => {
    hookState.license = { data: undefined, isLoading: true, isError: false };
    const { container, cleanup } = render(
      <EntitlementGate entitlement="saml" label="SAML SSO">
        <div>Loading content</div>
      </EntitlementGate>,
    );
    expect(container.textContent).toContain("Loading content");
    expect(container.textContent).not.toContain("View pricing");
    cleanup();
  });

  it("renders children on error (fail open)", () => {
    hookState.license = { data: undefined, isLoading: false, isError: true };
    const { container, cleanup } = render(
      <EntitlementGate entitlement="saml" label="SAML SSO">
        <div>Error fallback</div>
      </EntitlementGate>,
    );
    expect(container.textContent).toContain("Error fallback");
    cleanup();
  });

  it("shows current plan in upgrade prompt", () => {
    hookState.license.data.plan = "community";
    hookState.license.data.entitlements = {};
    const { container, cleanup } = render(
      <EntitlementGate entitlement="rbac" label="Advanced RBAC">
        <div>RBAC content</div>
      </EntitlementGate>,
    );
    expect(container.textContent).toContain("community");
    expect(container.textContent).toContain("Advanced RBAC");
    cleanup();
  });

  it("supports array of entitlement keys (any match grants)", () => {
    hookState.license.data.entitlements = { siemExport: false, auditExport: true };
    const { container, cleanup } = render(
      <EntitlementGate entitlement={["siemExport", "auditExport"]} label="Audit Export">
        <div>Export content</div>
      </EntitlementGate>,
    );
    expect(container.textContent).toContain("Export content");
    expect(container.textContent).not.toContain("View pricing");
    cleanup();
  });

  it("renders inline mode correctly", () => {
    hookState.license.data.entitlements = {};
    const { container, cleanup } = render(
      <EntitlementGate entitlement="legalHold" label="Legal Hold" description="Immutable retention" inline>
        <div>Hold content</div>
      </EntitlementGate>,
    );
    expect(container.textContent).toContain("Legal Hold");
    expect(container.textContent).toContain("Immutable retention");
    expect(container.textContent).toContain("Upgrade");
    // Inline mode doesn't show "View pricing" — shows "Upgrade" button
    expect(container.textContent).not.toContain("View pricing");
    cleanup();
  });
});
