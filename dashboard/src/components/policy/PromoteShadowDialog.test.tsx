import { act } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { shadowState, activateState, deactivateState } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  return {
    shadowState: {
      data: null as {
        shadow_bundle_id: string;
        bundle_id: string;
        tenant_id: string;
        content: string;
        created_at: string;
        activated_at: string;
      } | null,
      isLoading: false,
      isError: false,
      error: null as Error | null,
    },
    activateState: {
      mutateAsync: vi.fn().mockResolvedValue(undefined),
      isPending: false,
    },
    deactivateState: {
      mutateAsync: vi.fn().mockResolvedValue(undefined),
      isPending: false,
    },
  };
});

vi.mock("@/hooks/useShadowPolicy", () => ({
  useShadowPolicy: () => shadowState,
  useDeactivateShadow: () => deactivateState,
}));
vi.mock("@/hooks/usePolicies", () => ({
  useUpdatePolicyBundle: () => activateState,
}));
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

import { PromoteShadowDialog } from "./PromoteShadowDialog";

function render(open = true) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const onClose = vi.fn();
  act(() => {
    root.render(
      <PromoteShadowDialog
        open={open}
        bundleID="secops/b"
        bundleName="secops-primary"
        activeContent={"version: 1\nrules: []\n"}
        onClose={onClose}
      />,
    );
  });
  return {
    container,
    onClose,
    unmount: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

describe("PromoteShadowDialog", () => {
  beforeEach(() => {
    shadowState.data = null;
    shadowState.isLoading = false;
    shadowState.isError = false;
    activateState.mutateAsync = vi.fn().mockResolvedValue(undefined);
    activateState.isPending = false;
    deactivateState.mutateAsync = vi.fn().mockResolvedValue(undefined);
    deactivateState.isPending = false;
  });

  it("renders nothing when closed", () => {
    const { container, unmount } = render(false);
    expect(container.querySelector("[role=dialog]")).toBeNull();
    unmount();
  });

  it("warns when no shadow is active", () => {
    const { container, unmount } = render();
    expect(container.textContent).toContain("No shadow policy is active");
    unmount();
  });

  it("disables confirm until the bundle name is typed", () => {
    shadowState.data = {
      shadow_bundle_id: "shadow-abc",
      bundle_id: "secops/b",
      tenant_id: "default",
      content: "version: 2\nrules: [deny]\n",
      created_at: "2026-04-18T00:00:00Z",
      activated_at: "2026-04-18T00:00:00Z",
    };

    const { container, unmount } = render();
    const button = container.querySelector(
      "[data-testid=promote-confirm-button]",
    ) as HTMLButtonElement;
    expect(button.disabled).toBe(true);

    const input = container.querySelector("#promote-confirm") as HTMLInputElement;
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype,
        "value",
      )?.set;
      setter?.call(input, "secops-primary");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(button.disabled).toBe(false);
    unmount();
  });

  it("renders diff highlights (added/removed)", () => {
    shadowState.data = {
      shadow_bundle_id: "shadow-abc",
      bundle_id: "secops/b",
      tenant_id: "default",
      content: "version: 1\nrules: [deny]\n",
      created_at: "2026-04-18T00:00:00Z",
      activated_at: "2026-04-18T00:00:00Z",
    };

    const { container, unmount } = render();
    const pre = container.querySelector("pre");
    expect(pre).not.toBeNull();
    expect(pre!.textContent).toContain("rules: []");
    expect(pre!.textContent).toContain("rules: [deny]");
    unmount();
  });
});
