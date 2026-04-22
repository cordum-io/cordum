import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";
import { ExpiryBanner } from "./ExpiryBanner";
import { TierBadge } from "./TierBadge";
import { TierLimitBar, usageRatio } from "./TierLimitBar";
import { UpgradePrompt, shouldShowUpgradePrompt } from "./UpgradePrompt";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function renderNode(node: React.ReactNode) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(<>{node}</>);
  });

  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

describe("license UI components", () => {
  it("renders plan badges for all supported tiers", () => {
    const { container, cleanup } = renderNode(
      <div>
        <TierBadge plan="Community" />
        <TierBadge plan="Team" />
        <TierBadge plan="Enterprise" />
      </div>,
    );

    try {
      expect(container.textContent).toContain("Community");
      expect(container.textContent).toContain("Team");
      expect(container.textContent).toContain("Enterprise");
    } finally {
      cleanup();
    }
  });

  it("shows usage summaries and upgrade prompts at the 80% threshold", () => {
    expect(usageRatio({ current: 8, allowed: 10 })).toBe(0.8);
    expect(shouldShowUpgradePrompt({ current: 7, allowed: 10 })).toBe(false);
    expect(shouldShowUpgradePrompt({ current: 8, allowed: 10 })).toBe(true);

    const { container, cleanup } = renderNode(
      <div>
        <TierLimitBar label="Workers" metric={{ current: 8, allowed: 10, registered: 8, connected: 7 }} />
        <UpgradePrompt label="Workers" metric={{ current: 8, allowed: 10 }} plan="Team" />
      </div>,
    );

    try {
      expect(container.textContent).toContain("Workers");
      expect(container.textContent).toContain("8 / 10");
      expect(container.textContent).toContain("Workers nearing its tier limit");
      expect(container.textContent).toContain("You are using 8 of 10 workers on Team.");
    } finally {
      cleanup();
    }
  });

  it("renders urgency copy for warning and degraded expiry states", () => {
    const { container, cleanup } = renderNode(
      <div>
        <ExpiryBanner status="warning" expiresAt="2026-04-20T00:00:00Z" />
        <ExpiryBanner status="degraded" expiresAt="2026-04-01T00:00:00Z" />
      </div>,
    );

    try {
      expect(container.textContent).toContain("License renewal window is open");
      expect(container.textContent).toContain("Break-glass mode active");
      expect(container.textContent).toContain("break-glass admin access available");
    } finally {
      cleanup();
    }
  });
});
