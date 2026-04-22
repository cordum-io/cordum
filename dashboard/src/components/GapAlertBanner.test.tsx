import React, { act } from "react";
import { MemoryRouter } from "react-router-dom";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GapAlertBanner } from "./GapAlertBanner";
import type { AuditVerifyResult } from "../hooks/useAuditChainVerify";
import { useVerificationStore } from "../state/verification";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { verifyState } = vi.hoisted(() => ({
  verifyState: {
    data: undefined as AuditVerifyResult | undefined,
    isLoading: false,
    isFetching: false,
    isError: false,
    error: null as unknown,
    dataUpdatedAt: 0,
  },
}));

vi.mock("../hooks/useAuditVerify", async () => {
  const actual = await vi.importActual<
    typeof import("../hooks/useAuditVerify")
  >("../hooks/useAuditVerify");
  return {
    ...actual,
    useAuditVerify: () => verifyState,
    useTriggerAuditVerify: () => () => {},
  };
});

interface Ctx {
  container: HTMLElement;
  root: Root;
}
const mounted: Ctx[] = [];
function render(ui: React.ReactElement): Ctx {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => root.render(<MemoryRouter>{ui}</MemoryRouter>));
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
  verifyState.data = undefined;
  verifyState.isLoading = false;
  verifyState.isFetching = false;
  verifyState.isError = false;
});

beforeEach(() => {
  useVerificationStore.setState({
    lastVerifiedAt: {},
    dismissedGapBanners: {},
  });
});

function compromised(): AuditVerifyResult {
  return {
    status: "compromised",
    total_events: 50,
    verified_events: 47,
    gaps: [
      { at_seq: 14300, type: "missing" },
      { at_seq: 14301, type: "hash_mismatch" },
    ],
    retention_boundary_seq: 14200,
    retention_window_hours: 168,
    first_seq: 14200,
    last_seq: 14420,
  };
}

describe("GapAlertBanner", () => {
  it("renders nothing while there is no verify data yet", () => {
    const ctx = render(<GapAlertBanner tenant="acme" />);
    expect(
      ctx.container.querySelector("[data-testid=gap-alert-banner]"),
    ).toBeNull();
  });

  it("renders nothing for an ok chain", () => {
    verifyState.data = {
      status: "ok",
      total_events: 10,
      verified_events: 10,
      gaps: [],
      retention_boundary_seq: 1,
      retention_window_hours: 168,
    };
    const ctx = render(<GapAlertBanner tenant="acme" />);
    expect(
      ctx.container.querySelector("[data-testid=gap-alert-banner]"),
    ).toBeNull();
  });

  it("renders an alert with seq reference + drill-down link when the chain is compromised", () => {
    verifyState.data = compromised();
    const ctx = render(<GapAlertBanner tenant="acme" />);
    const banner = ctx.container.querySelector<HTMLElement>(
      "[data-testid=gap-alert-banner]",
    );
    expect(banner).not.toBeNull();
    expect(banner?.getAttribute("role")).toBe("alert");
    expect(banner?.getAttribute("aria-live")).toBe("assertive");
    expect(ctx.container.textContent).toContain("#14300");
    expect(ctx.container.textContent).toContain("2 tamper signals");
    const drill = Array.from(
      ctx.container.querySelectorAll<HTMLAnchorElement>("a"),
    ).find((a) => a.getAttribute("href")?.includes("seqFrom=14300"));
    expect(drill).toBeDefined();
  });

  it("dismisses via the X button and remembers the dismissal in Zustand (session-scoped)", () => {
    verifyState.data = compromised();
    const ctx = render(<GapAlertBanner tenant="acme" />);
    const dismiss = ctx.container.querySelector<HTMLButtonElement>(
      "button[aria-label^='Dismiss audit chain']",
    );
    expect(dismiss).not.toBeNull();
    act(() => dismiss!.click());
    expect(
      ctx.container.querySelector("[data-testid=gap-alert-banner]"),
    ).toBeNull();
    expect(
      useVerificationStore.getState().dismissedGapBanners["acme"],
    ).toBe(true);
  });

  it("stays dismissed across remounts in the same session", () => {
    verifyState.data = compromised();
    useVerificationStore.setState({
      lastVerifiedAt: {},
      dismissedGapBanners: { acme: true },
    });
    const ctx = render(<GapAlertBanner tenant="acme" />);
    expect(
      ctx.container.querySelector("[data-testid=gap-alert-banner]"),
    ).toBeNull();
  });

  it("uses flex-wrap so the two action links never overflow at 375px", () => {
    verifyState.data = compromised();
    const ctx = render(<GapAlertBanner tenant="acme" />);
    const actions = ctx.container.querySelector<HTMLElement>(
      ".flex.flex-wrap.items-center.gap-3",
    );
    expect(actions).not.toBeNull();
  });

  it("uses no hardcoded hex colours — dark mode inherits via CSS vars", () => {
    verifyState.data = compromised();
    const ctx = render(<GapAlertBanner tenant="acme" />);
    const html = ctx.container.innerHTML.replace(/="[^"]*"/g, "");
    expect(html).not.toMatch(/#[0-9a-fA-F]{6}/);
  });
});
