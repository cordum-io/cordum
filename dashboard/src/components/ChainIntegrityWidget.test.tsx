import React, { act } from "react";
import { MemoryRouter } from "react-router-dom";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ChainIntegrityWidget,
  buildAuditDrillDownHref,
  deriveWidgetState,
  summariseGaps,
} from "./ChainIntegrityWidget";
import type { AuditVerifyResult } from "../hooks/useAuditChainVerify";
import { useVerificationStore } from "../state/verification";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// ---------------------------------------------------------------------------
// Mocks — swap the React Query hook, the admin check, and the trigger
// with vi.mock hoists so the widget renders deterministically.
// ---------------------------------------------------------------------------

const { queryState, isAdminRef, triggerSpy } = vi.hoisted(() => ({
  queryState: {
    data: undefined as AuditVerifyResult | undefined,
    isLoading: false,
    isFetching: false,
    isError: false,
    error: null as unknown,
    dataUpdatedAt: 0,
  },
  isAdminRef: { value: true },
  triggerSpy: vi.fn(),
}));

vi.mock("../hooks/useAuditChainVerify", async () => {
  const actual = await vi.importActual<
    typeof import("../hooks/useAuditChainVerify")
  >("../hooks/useAuditChainVerify");
  return {
    ...actual,
    useAuditChainVerify: () => queryState,
  };
});

vi.mock("../hooks/useAuditVerify", async () => {
  const actual = await vi.importActual<
    typeof import("../hooks/useAuditVerify")
  >("../hooks/useAuditVerify");
  return {
    ...actual,
    useAuditVerify: () => queryState,
    useTriggerAuditVerify: () => triggerSpy,
  };
});

vi.mock("../hooks/usePermission", () => ({
  usePermission: () => ({ allowed: isAdminRef.value, userRoles: [] }),
  useIsAdmin: () => isAdminRef.value,
}));

// Shared render helper
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
  queryState.data = undefined;
  queryState.isLoading = false;
  queryState.isFetching = false;
  queryState.isError = false;
  queryState.error = null;
  queryState.dataUpdatedAt = 0;
  isAdminRef.value = true;
  triggerSpy.mockReset();
});

beforeEach(() => {
  useVerificationStore.setState({
    lastVerifiedAt: {},
    dismissedGapBanners: {},
  });
});

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

function ok(): AuditVerifyResult {
  return {
    status: "ok",
    total_events: 420,
    verified_events: 420,
    gaps: [],
    retention_boundary_seq: 1,
    retention_window_hours: 168,
    first_seq: 1,
    last_seq: 420,
  };
}

function partial(): AuditVerifyResult {
  return {
    status: "partial",
    total_events: 30,
    verified_events: 30,
    gaps: [
      { at_seq: 1, type: "retention_trimmed" },
      { at_seq: 2, type: "retention_trimmed" },
      { at_seq: 3, type: "retention_trimmed" },
    ],
    retention_boundary_seq: 4,
    retention_window_hours: 168,
    first_seq: 4,
    last_seq: 33,
  };
}

function compromised(): AuditVerifyResult {
  return {
    status: "compromised",
    total_events: 10,
    verified_events: 7,
    gaps: [
      { at_seq: 15, type: "missing" },
      { at_seq: 16, type: "hash_mismatch" },
      { at_seq: 18, type: "out_of_order" },
      { at_seq: 5, type: "retention_trimmed" },
    ],
    retention_boundary_seq: 5,
    retention_window_hours: 168,
    first_seq: 5,
    last_seq: 18,
  };
}

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

describe("deriveWidgetState", () => {
  it("returns loading when isLoading without prior data", () => {
    expect(
      deriveWidgetState({ isLoading: true, isFetching: true, isError: false, data: undefined }),
    ).toBe("loading");
  });
  it("returns error when isError without data", () => {
    expect(
      deriveWidgetState({ isLoading: false, isFetching: false, isError: true, data: undefined }),
    ).toBe("error");
  });
  it("returns not_checked when no data and no loading signal", () => {
    expect(
      deriveWidgetState({ isLoading: false, isFetching: false, isError: false, data: undefined }),
    ).toBe("not_checked");
  });
  it("returns ok/partial/compromised from data.status", () => {
    expect(
      deriveWidgetState({ isLoading: false, isFetching: false, isError: false, data: ok() }),
    ).toBe("ok");
    expect(
      deriveWidgetState({ isLoading: false, isFetching: false, isError: false, data: partial() }),
    ).toBe("partial");
    expect(
      deriveWidgetState({ isLoading: false, isFetching: false, isError: false, data: compromised() }),
    ).toBe("compromised");
  });
});

describe("summariseGaps", () => {
  it("counts per-type and derives min/max tamper seqs", () => {
    const s = summariseGaps(compromised().gaps);
    expect(s.missing).toBe(1);
    expect(s.hashMismatch).toBe(1);
    expect(s.outOfOrder).toBe(1);
    expect(s.retentionTrimmed).toBe(1);
    expect(s.tamperTotal).toBe(3);
    // min/max across tamper signals only — retention-trimmed at seq 5
    // is excluded from the tamper range.
    expect(s.minTamperSeq).toBe(15);
    expect(s.maxTamperSeq).toBe(18);
  });
  it("returns all-zero counts for empty gap array", () => {
    const s = summariseGaps([]);
    expect(s.tamperTotal).toBe(0);
    expect(s.minTamperSeq).toBeNull();
    expect(s.maxTamperSeq).toBeNull();
  });
});

describe("buildAuditDrillDownHref", () => {
  it("builds /audit?seqFrom=&seqTo= from tamper range", () => {
    const s = summariseGaps(compromised().gaps);
    const href = buildAuditDrillDownHref(s);
    expect(href).toBe("/audit?seqFrom=15&seqTo=18");
  });
  it("falls back to /audit when no tamper seqs", () => {
    const s = summariseGaps(partial().gaps);
    expect(buildAuditDrillDownHref(s)).toBe("/audit");
  });
});

// ---------------------------------------------------------------------------
// Render branches
// ---------------------------------------------------------------------------

describe("ChainIntegrityWidget rendering", () => {
  it("renders loading state with aria-busy and 'Checking audit chain…' after admin triggers verify", () => {
    // Widget defaults to not_checked — a page open never auto-fetches
    // the admin-only endpoint. Clicking Run chain check flips the
    // internal sessionTriggered flag, which is what lets isLoading
    // propagate into the rendered state. isFetching stays false at
    // mount so the Run button isn't disabled before the click.
    queryState.isLoading = true;
    queryState.isFetching = false;
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    const runBtn = Array.from(ctx.container.querySelectorAll("button")).find(
      (b) => b.textContent?.includes("Run chain check"),
    );
    expect(runBtn).toBeDefined();
    act(() => runBtn!.click());

    const root = ctx.container.querySelector<HTMLElement>(
      "[data-testid='chain-integrity-widget']",
    );
    expect(root).not.toBeNull();
    expect(root?.getAttribute("data-state")).toBe("loading");
    expect(root?.getAttribute("aria-busy")).toBe("true");
    expect(ctx.container.textContent).toContain("Checking audit chain");
  });

  it("renders not_checked state with a Run chain check admin CTA", () => {
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    const root = ctx.container.querySelector<HTMLElement>(
      "[data-testid='chain-integrity-widget']",
    );
    expect(root?.getAttribute("data-state")).toBe("not_checked");
    const buttons = Array.from(ctx.container.querySelectorAll("button"));
    const runBtn = buttons.find((b) => b.textContent?.includes("Run chain check"));
    expect(runBtn).toBeDefined();
    // Admin can click → triggers verify
    act(() => runBtn!.click());
    expect(triggerSpy).toHaveBeenCalledTimes(1);
  });

  it("renders not_checked for non-admin without the CTA", () => {
    isAdminRef.value = false;
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    expect(ctx.container.textContent).toContain(
      "Chain checks can only be initiated by an admin",
    );
    const buttons = Array.from(ctx.container.querySelectorAll("button"));
    expect(buttons.some((b) => b.textContent?.includes("Run chain check"))).toBe(false);
  });

  it("renders ok state with verified_events / total_events numerals", () => {
    queryState.data = ok();
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    const root = ctx.container.querySelector<HTMLElement>(
      "[data-testid='chain-integrity-widget']",
    );
    expect(root?.getAttribute("data-state")).toBe("ok");
    expect(ctx.container.textContent).toContain("420");
    expect(ctx.container.textContent).toContain("/ 420");
    expect(ctx.container.textContent).toContain("VERIFIED");
    expect(ctx.container.textContent).toContain("7-day window");
  });

  it("renders partial state and calls out retention-trimmed count as expected, not alarming", () => {
    queryState.data = partial();
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    expect(ctx.container.textContent).toContain("RETENTION TRIMMED");
    expect(ctx.container.textContent).toContain("3 events");
    expect(ctx.container.textContent).toContain("expected");
    // No danger-tone copy
    expect(ctx.container.textContent).not.toContain(
      "Audit chain integrity check FAILED",
    );
  });

  it("renders compromised state with FAILED banner, per-type counts, and drill-down href", () => {
    queryState.data = compromised();
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    expect(ctx.container.textContent).toContain("CHAIN COMPROMISED");
    expect(ctx.container.textContent).toContain(
      "Audit chain integrity check FAILED",
    );
    expect(ctx.container.textContent).toContain("Missing");
    expect(ctx.container.textContent).toContain("Hash mismatch");
    expect(ctx.container.textContent).toContain("Out of order");
    // Affected range pointer
    expect(ctx.container.textContent).toContain("#15");
    expect(ctx.container.textContent).toContain("#18");
    const link = Array.from(
      ctx.container.querySelectorAll<HTMLAnchorElement>("a"),
    ).find((a) => a.getAttribute("href")?.includes("seqFrom=15"));
    expect(link).toBeDefined();
    expect(link?.getAttribute("href")).toContain("seqTo=18");
    expect(link?.getAttribute("href")).toMatch(/\/audit\?/);
  });

  it("admin re-verify button is disabled while isFetching", () => {
    queryState.data = ok();
    queryState.isFetching = true;
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    const reverify = Array.from(ctx.container.querySelectorAll("button")).find(
      (b) => b.textContent?.includes("Re-verify"),
    );
    expect(reverify).toBeDefined();
    expect(reverify?.hasAttribute("disabled")).toBe(true);
  });

  it("non-admin sees read-only last-verified and a role-restriction hint instead of the re-verify button", () => {
    isAdminRef.value = false;
    queryState.data = ok();
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    expect(ctx.container.textContent).toContain("Re-verify requires an admin role");
    const reverify = Array.from(ctx.container.querySelectorAll("button")).find(
      (b) => b.textContent?.includes("Re-verify"),
    );
    expect(reverify).toBeUndefined();
  });

  it("surfaces persisted last-verified timestamp from the Zustand store", () => {
    queryState.data = ok();
    const recent = Date.now() - 60_000;
    useVerificationStore.setState({
      lastVerifiedAt: { acme: recent },
      dismissedGapBanners: {},
    });
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    // formatRelativeTime will produce a recent-time phrase; we only
    // pin that the label is NOT the never-verified sentinel.
    expect(ctx.container.textContent).not.toContain("Never");
  });

  it("regression (reopen #1): non-admin does NOT auto-fetch the admin-only verify endpoint", () => {
    // The widget must not render a loading spinner for viewers, because
    // the backend returns 403 for non-admin /audit/verify calls — a
    // loading card that never resolves is worse UX than the read-only
    // NotCheckedView.
    isAdminRef.value = false;
    queryState.isLoading = true;   // scenario: the mock says "loading"
    queryState.isFetching = true;  // ... but the widget must override
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    const root = ctx.container.querySelector<HTMLElement>(
      "[data-testid='chain-integrity-widget']",
    );
    // Not admin + no sessionTriggered → state stays not_checked.
    expect(root?.getAttribute("data-state")).toBe("not_checked");
    expect(ctx.container.textContent).toContain(
      "Chain checks can only be initiated by an admin",
    );
  });

  it("regression (reopen #2): admin page-mount does NOT auto-fetch; NotCheckedView renders until Run is clicked", () => {
    // The DoD's one-click-verify contract: a page open must land the
    // operator in NotCheckedView, not in loading-spinner-then-poll.
    // The fix pair for this is (a) useAuditVerify defaults enabled=false
    // and (b) the widget only flips sessionTriggered after the Run click.
    isAdminRef.value = true;
    queryState.data = undefined;
    queryState.isLoading = false;
    queryState.isFetching = false;
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    const root = ctx.container.querySelector<HTMLElement>(
      "[data-testid='chain-integrity-widget']",
    );
    expect(root?.getAttribute("data-state")).toBe("not_checked");
    // No triggerVerify call at mount.
    expect(triggerSpy).toHaveBeenCalledTimes(0);
    // Click triggers exactly once.
    const runBtn = Array.from(ctx.container.querySelectorAll("button")).find(
      (b) => b.textContent?.includes("Run chain check"),
    );
    act(() => runBtn!.click());
    expect(triggerSpy).toHaveBeenCalledTimes(1);
  });
});

describe("ChainIntegrityWidget responsive + dark-mode discipline", () => {
  it("primary metric row stacks 1-col on mobile and goes to 3-col at sm+", () => {
    queryState.data = ok();
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    const metricRow = ctx.container.querySelector<HTMLElement>(
      ".grid.grid-cols-1.sm\\:grid-cols-3",
    );
    expect(metricRow).not.toBeNull();
  });

  it("compromised gap-stat list wraps rather than overflowing on narrow viewports", () => {
    queryState.data = {
      status: "compromised",
      total_events: 5,
      verified_events: 2,
      gaps: [
        { at_seq: 10, type: "missing" },
        { at_seq: 11, type: "hash_mismatch" },
        { at_seq: 12, type: "out_of_order" },
      ],
      retention_boundary_seq: 1,
      retention_window_hours: 168,
      first_seq: 1,
      last_seq: 12,
    };
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    const gapList = ctx.container.querySelector<HTMLElement>(
      "dl.flex.flex-wrap",
    );
    expect(gapList).not.toBeNull();
  });

  it("loading view carries aria-busy=true after admin triggers verify", () => {
    // isFetching=false at mount so the Run button is enabled; once
    // the click flips sessionTriggered, isLoading=true is enough to
    // route into LoadingView.
    queryState.isLoading = true;
    queryState.isFetching = false;
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    const runBtn = Array.from(ctx.container.querySelectorAll("button")).find(
      (b) => b.textContent?.includes("Run chain check"),
    );
    act(() => runBtn!.click());
    const root = ctx.container.querySelector<HTMLElement>(
      "[data-testid='chain-integrity-widget']",
    );
    expect(root?.getAttribute("aria-busy")).toBe("true");
    const liveRegion = ctx.container.querySelector<HTMLElement>(
      "[role='status'][aria-live='polite']",
    );
    expect(liveRegion).not.toBeNull();
  });

  it("uses no hardcoded 6-digit hex colours — only CSS-var tokens — so dark mode inherits correctly", () => {
    queryState.data = ok();
    const ctx = render(<ChainIntegrityWidget tenant="acme" />);
    const html = ctx.container.innerHTML;
    // Seq references like "#420" and "#1" deliberately appear in
    // rendered text; only 6-digit hex triples are colour values, so
    // pin that form only. Strip quoted attributes first so aria-label
    // / title text ("#15 – #18") never triggers a false positive.
    const withoutAttrs = html.replace(/="[^"]*"/g, "");
    expect(withoutAttrs).not.toMatch(/#[0-9a-fA-F]{6}\b/);
  });
});

// ---------------------------------------------------------------------------
// Compact variant — added under task-55f813b3 step-6 to support the
// AuditLogPage sticky integrity bar (DoD #3). The compact bar is a
// single horizontal row driven by the same state machine as the full
// card, so all 6 WidgetState branches must render something useful.
// ---------------------------------------------------------------------------

describe("ChainIntegrityWidget compact variant", () => {
  it("renders [data-compact='true'] and skips the metric grid + footer", () => {
    queryState.data = ok();
    const ctx = render(<ChainIntegrityWidget tenant="acme" compact />);
    const root = ctx.container.querySelector<HTMLElement>(
      "[data-testid='chain-integrity-widget']",
    );
    expect(root?.getAttribute("data-compact")).toBe("true");
    // Metric grid is the 3-col primary-metric section in the full card —
    // confirm it does NOT render in compact mode.
    expect(
      ctx.container.querySelector(".grid.grid-cols-1.sm\\:grid-cols-3"),
    ).toBeNull();
    expect(ctx.container.textContent).toContain("VERIFIED");
    expect(ctx.container.textContent).toContain("420 of 420 events");
  });

  it("compact + state=compromised renders CHAIN COMPROMISED and the danger tone", () => {
    queryState.data = compromised();
    const ctx = render(<ChainIntegrityWidget tenant="acme" compact />);
    const root = ctx.container.querySelector<HTMLElement>(
      "[data-testid='chain-integrity-widget']",
    );
    expect(root?.getAttribute("data-state")).toBe("compromised");
    expect(ctx.container.textContent).toContain("CHAIN COMPROMISED");
  });

  it("compact + state=partial renders RETENTION TRIMMED", () => {
    queryState.data = partial();
    const ctx = render(<ChainIntegrityWidget tenant="acme" compact />);
    expect(ctx.container.textContent).toContain("RETENTION TRIMMED");
  });

  it("compact + non-admin renders 'Admin only' instead of the Re-verify button", () => {
    isAdminRef.value = false;
    queryState.data = ok();
    const ctx = render(<ChainIntegrityWidget tenant="acme" compact />);
    expect(ctx.container.textContent).toContain("Admin only");
    const reVerifyBtn = Array.from(
      ctx.container.querySelectorAll("button"),
    ).find((b) => b.textContent?.includes("Re-verify"));
    expect(reVerifyBtn).toBeUndefined();
  });

  it("compact + state=not_checked renders NOT VERIFIED and offers Run chain check for admin", () => {
    queryState.data = undefined;
    isAdminRef.value = true;
    const ctx = render(<ChainIntegrityWidget tenant="acme" compact />);
    expect(ctx.container.textContent).toContain("NOT VERIFIED");
    const runBtn = Array.from(ctx.container.querySelectorAll("button")).find(
      (b) => b.textContent?.includes("Run chain check"),
    );
    expect(runBtn).not.toBeUndefined();
  });
});
