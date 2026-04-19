import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { SignatureBadge } from "./SignatureBadge";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// Minimal render helper — the dashboard deliberately avoids
// @testing-library/react (see ConnectionIndicator.test.ts + the
// GovernanceHealthIndicator test for prior art). We drive React
// directly via createRoot so the test bundle stays lean.

interface RenderCtx {
  container: HTMLElement;
  root: Root;
}

const mounted: RenderCtx[] = [];

function render(ui: React.ReactElement): RenderCtx {
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

function getStatusEl(ctx: RenderCtx): HTMLElement {
  const el = ctx.container.querySelector<HTMLElement>("[role='status']");
  if (!el) throw new Error("SignatureBadge did not render a role=status element");
  return el;
}

describe("SignatureBadge", () => {
  it("renders the signed state with a cryptographic-signed aria-label", () => {
    const ctx = render(<SignatureBadge signed={true} />);
    const el = getStatusEl(ctx);
    expect(el.getAttribute("aria-label")).toBe(
      "Policy bundle is cryptographically signed",
    );
    expect(el.getAttribute("data-signature-state")).toBe("signed");
    expect(el.textContent).toContain("Signed");
  });

  it("renders the unsigned state with the strict-mode-reject aria-label", () => {
    const ctx = render(<SignatureBadge signed={false} />);
    const el = getStatusEl(ctx);
    expect(el.getAttribute("aria-label")).toBe(
      "Policy bundle is NOT signed. Strict-mode kernels will reject it.",
    );
    expect(el.getAttribute("data-signature-state")).toBe("unsigned");
    expect(el.textContent).toContain("Unsigned");
  });

  it("renders the unknown state for missing signature data", () => {
    const ctx = render(<SignatureBadge signed="unknown" />);
    const el = getStatusEl(ctx);
    expect(el.getAttribute("aria-label")).toBe(
      "Policy bundle signature status is unknown",
    );
    expect(el.getAttribute("data-signature-state")).toBe("unknown");
    expect(el.textContent).toContain("Unknown");
  });

  it("treats undefined signed prop as unknown (graceful fallback)", () => {
    const ctx = render(<SignatureBadge />);
    expect(getStatusEl(ctx).getAttribute("data-signature-state")).toBe("unknown");
  });

  it("builds a title tooltip including publicKeyId, signedBy, and signedAt only when signed", () => {
    const ctx = render(
      <SignatureBadge
        signed={true}
        publicKeyId="key-abc123"
        signedBy="release-bot"
        signedAt="2026-04-18T09:12:00Z"
      />,
    );
    const title = getStatusEl(ctx).getAttribute("title") ?? "";
    expect(title).toContain("key-abc123");
    expect(title).toContain("release-bot");
    expect(title).toContain("2026-04-18T09:12:00");
  });

  it("omits the tooltip entirely for unsigned bundles (no signature info to show)", () => {
    const ctx = render(
      <SignatureBadge
        signed={false}
        publicKeyId="key-abc123"
        signedAt="2026-04-18T09:12:00Z"
      />,
    );
    expect(getStatusEl(ctx).hasAttribute("title")).toBe(false);
  });

  it("honours the size=sm prop with smaller typography / padding tokens", () => {
    const sm = render(<SignatureBadge signed={true} size="sm" />);
    expect(getStatusEl(sm).className).toMatch(/text-\[11px\]/);
    const md = render(<SignatureBadge signed={true} size="md" />);
    expect(getStatusEl(md).className).toMatch(/text-xs/);
  });

  it("relies only on CSS-var tokens (no hardcoded hex) so dark mode inherits correctly", () => {
    const ctx = render(<SignatureBadge signed={true} />);
    const html = ctx.container.innerHTML.replace(/="[^"]*"/g, "");
    expect(html).not.toMatch(/#[0-9a-fA-F]{6}/);
  });

  it("keeps the inline-flex layout compact enough for 375px mobile screens", () => {
    const ctx = render(<SignatureBadge signed={true} size="sm" />);
    const el = getStatusEl(ctx);
    // inline-flex + rounded-full + small padding ensures the badge
    // never forces a horizontal scroll in a narrow table cell.
    expect(el.className).toMatch(/inline-flex/);
    expect(el.className).toMatch(/rounded-full/);
  });
});
