import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import type { PolicyBundle } from "@/api/types";
import { BundleListItem } from "./BundleListItem";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function baseBundle(overrides: Partial<PolicyBundle> = {}): PolicyBundle {
  return {
    id: "secops/demo",
    name: "Demo",
    rules: [],
    status: "published",
    enabled: true,
    ...overrides,
  };
}

function render(bundle: PolicyBundle, canPublish = true) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const onOpen = vi.fn();
  act(() => {
    root.render(<BundleListItem bundle={bundle} canPublish={canPublish} onOpen={onOpen} />);
  });
  return {
    container,
    onOpen,
    unmount: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

describe("BundleListItem shadow badge", () => {
  it("renders 'No shadow' by default", () => {
    const { container, unmount } = render(baseBundle());
    expect(container.querySelector("[data-testid=bundle-no-shadow-badge]")).not.toBeNull();
    expect(container.querySelector("[data-testid=bundle-shadow-badge]")).toBeNull();
    unmount();
  });

  it("renders the amber Shadow badge when a shadow summary is present", () => {
    const { container, unmount } = render(
      baseBundle({
        shadow: {
          shadow_bundle_id: "shadow-abc",
          bundle_id: "secops/demo",
          tenant_id: "default",
          created_at: "2026-04-18T00:00:00Z",
          activated_at: "2026-04-18T00:00:00Z",
        },
      }),
    );
    const badge = container.querySelector("[data-testid=bundle-shadow-badge]") as HTMLButtonElement;
    expect(badge).not.toBeNull();
    expect(badge.textContent?.toLowerCase()).toContain("shadow");
    expect(badge.getAttribute("aria-label")).toContain("Shadow");
    unmount();
  });

  it("clicking the shadow badge opens the bundle detail", () => {
    const { container, onOpen, unmount } = render(
      baseBundle({
        shadow: {
          shadow_bundle_id: "shadow-abc",
          bundle_id: "secops/demo",
          tenant_id: "default",
          created_at: "2026-04-18T00:00:00Z",
          activated_at: "2026-04-18T00:00:00Z",
        },
      }),
    );
    const badge = container.querySelector("[data-testid=bundle-shadow-badge]") as HTMLButtonElement;
    act(() => {
      badge.click();
    });
    expect(onOpen).toHaveBeenCalledWith("secops/demo");
    unmount();
  });
});

describe("BundleListItem signature badge", () => {
  it("renders the signed badge when bundle.signed is true", () => {
    const { container, unmount } = render(
      baseBundle({
        signed: true,
        signature: {
          algorithm: "ed25519",
          key_id: "key-1",
          value: "sig-abc",
          hash: "hash-abc",
          signed_bytes: 100,
        },
      }),
    );
    const badge = container.querySelector(
      "[data-testid=bundle-signature-badge]",
    );
    expect(badge).not.toBeNull();
    expect(badge?.getAttribute("data-signature-state")).toBe("signed");
    unmount();
  });

  it("renders the unsigned badge when bundle.signed is false", () => {
    const { container, unmount } = render(baseBundle({ signed: false }));
    const badge = container.querySelector(
      "[data-testid=bundle-signature-badge]",
    );
    expect(badge?.getAttribute("data-signature-state")).toBe("unsigned");
    unmount();
  });

  it("renders the unknown badge when bundle.signed is undefined (graceful fallback)", () => {
    const { container, unmount } = render(baseBundle());
    const badge = container.querySelector(
      "[data-testid=bundle-signature-badge]",
    );
    expect(badge?.getAttribute("data-signature-state")).toBe("unknown");
    unmount();
  });
});
