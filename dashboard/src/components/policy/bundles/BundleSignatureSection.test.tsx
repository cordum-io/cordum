import React, { act } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { BundleSignatureSection } from "./BundleSignatureSection";
import type { PolicyBundle } from "@/api/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface Ctx {
  container: HTMLElement;
  root: Root;
}
const mounted: Ctx[] = [];
function render(ui: React.ReactElement): Ctx {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => root.render(ui));
  const ctx = { container, root };
  mounted.push(ctx);
  return ctx;
}
afterEach(() => {
  while (mounted.length > 0) {
    const ctx = mounted.pop();
    if (!ctx) continue;
    act(() => ctx.root.unmount());
    ctx.container.remove();
  }
});

function base(overrides: Partial<PolicyBundle> = {}): PolicyBundle {
  return {
    id: "secops/demo",
    name: "Demo",
    rules: [],
    ...overrides,
  };
}

describe("BundleSignatureSection", () => {
  it("renders signed cryptographic metadata (algorithm, key_id, hash, signed bytes) when bundle.signed is true", () => {
    const ctx = render(
      <BundleSignatureSection
        bundle={base({
          signed: true,
          signature: {
            algorithm: "ed25519",
            key_id: "prod-key-01",
            value: "v-sig",
            hash: "abcd".repeat(16),
            signed_bytes: 4321,
          },
        })}
      />,
    );
    const section = ctx.container.querySelector<HTMLElement>(
      "[data-testid=bundle-signature-section]",
    );
    expect(section?.getAttribute("data-signed")).toBe("true");
    const body = ctx.container.textContent ?? "";
    expect(body).toContain("Algorithm");
    expect(body).toContain("ed25519");
    expect(body).toContain("prod-key-01");
    expect(body).toContain("4,321");
    expect(body).toContain("bytes");
    // The hash is truncated for display but the underlying value
    // remains in the title attribute for copy.
    const copyButtons = ctx.container.querySelectorAll(
      "button[aria-label^='Copy']",
    );
    expect(copyButtons.length).toBeGreaterThan(0);
  });

  it("renders a warning callout when bundle is unsigned", () => {
    const ctx = render(<BundleSignatureSection bundle={base({ signed: false })} />);
    const section = ctx.container.querySelector<HTMLElement>(
      "[data-testid=bundle-signature-section]",
    );
    expect(section?.getAttribute("data-signed")).toBe("false");
    const body = ctx.container.textContent ?? "";
    expect(body).toContain("This bundle is not signed");
    expect(body).toContain("strict mode");
  });

  it("falls back to a muted 'unknown' state when signature info is absent", () => {
    const ctx = render(<BundleSignatureSection bundle={base()} />);
    const section = ctx.container.querySelector<HTMLElement>(
      "[data-testid=bundle-signature-section]",
    );
    expect(section?.getAttribute("data-signed")).toBe("unknown");
    const body = ctx.container.textContent ?? "";
    expect(body).toContain("Signature status is not available");
  });
});
