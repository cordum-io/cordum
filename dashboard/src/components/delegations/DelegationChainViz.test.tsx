import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DelegationView } from "@/api/types";
import {
  DelegationChainViz,
  formatDelegationExpiry,
  getDelegationNodeStatus,
} from "./DelegationChainViz";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState, motionState, navigateMock } = vi.hoisted(() => ({
  hookState: {
    mutateAsync: vi.fn().mockResolvedValue({ jti: "dlg-1", cascadedCount: 0 }),
    isPending: false,
  },
  motionState: {
    shouldAnimate: true,
  },
  navigateMock: vi.fn(),
}));

vi.mock("@/hooks/useDelegations", () => ({
  useRevokeDelegation: () => ({
    mutateAsync: hookState.mutateAsync,
    isPending: hookState.isPending,
  }),
}));

vi.mock("@/hooks/useMotionConfig", () => ({
  useMotionConfig: () => ({
    shouldAnimate: motionState.shouldAnimate,
  }),
}));

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return {
    ...actual,
    useNavigate: () => navigateMock,
  };
});

let container: HTMLDivElement;
let root: Root;

function render(node: React.ReactElement) {
  act(() => {
    root.render(<MemoryRouter>{node}</MemoryRouter>);
  });
}

function makeDelegation(overrides: Partial<DelegationView> = {}): DelegationView {
  return {
    jti: "dlg-1",
    issuer: "agent-a",
    subject: "agent-b",
    audience: "agent-c",
    allowedActions: ["approve", "deploy", "read", "write", "zap"],
    allowedTopics: ["job.alpha"],
    chain: [
      {
        agentId: "agent-a",
        issuedAt: "2026-04-21T00:00:00Z",
        expiresAt: "2026-04-21T01:00:00Z",
        jti: "dlg-root",
        issuedBy: "cordum",
      },
      {
        agentId: "agent-b",
        issuedAt: "2026-04-21T00:10:00Z",
        expiresAt: "2026-04-21T01:00:00Z",
        jti: "dlg-1",
        parentJti: "dlg-root",
        issuedBy: "agent-a",
      },
    ],
    chainDepth: 2,
    issuedAt: "2026-04-21T00:10:00Z",
    expiresAt: "2099-04-21T01:00:00Z",
    revoked: false,
    ...overrides,
  };
}

beforeEach(() => {
  hookState.mutateAsync.mockClear();
  hookState.isPending = false;
  motionState.shouldAnimate = true;
  navigateMock.mockReset();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

describe("DelegationChainViz", () => {
  it("renders a 3-link chain as a vertical tree of cards", () => {
    render(<DelegationChainViz delegation={makeDelegation()} />);

    expect(container.querySelector('[data-testid="delegation-chain-tree"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="delegation-node-agent-a"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="delegation-node-agent-b"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="delegation-node-agent-c"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="delegation-connector-0"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="delegation-connector-1"]')).toBeTruthy();
  });

  it("covers active, revoked, and expired delegation statuses", () => {
    expect(getDelegationNodeStatus(makeDelegation())).toBe("active");
    expect(getDelegationNodeStatus(makeDelegation({ revoked: true }))).toBe("revoked");
    expect(
      getDelegationNodeStatus(
        makeDelegation({ expiresAt: "2020-04-21T01:00:00Z", revoked: false }),
      ),
    ).toBe("expired");
    expect(
      formatDelegationExpiry("2020-04-21T01:00:00Z", "expired", Date.parse("2026-04-21T00:00:00Z")),
    ).toContain("Expired");
  });

  it("opens the revoke dialog and executes the revoke mutation", async () => {
    render(
      <DelegationChainViz
        delegation={makeDelegation()}
        loadCascadeCount={async () => 2}
      />,
    );

    const trigger = container.querySelector(
      '[data-testid="delegation-revoke-trigger"]',
    ) as HTMLButtonElement;
    expect(trigger).toBeTruthy();

    await act(async () => {
      trigger.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });

    expect(container.textContent).toContain(
      "Revoking this will cascade to 2 downstream delegations. Proceed?",
    );

    const confirm = Array.from(container.querySelectorAll("button")).find((button) =>
      button.textContent?.includes("Revoke delegation"),
    ) as HTMLButtonElement | undefined;
    expect(confirm).toBeTruthy();

    await act(async () => {
      confirm!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });

    expect(hookState.mutateAsync).toHaveBeenCalledWith({ jti: "dlg-1" });
  });

  it("truncates scope labels to top three actions plus overflow chip", () => {
    render(<DelegationChainViz delegation={makeDelegation()} />);

    expect(container.textContent).toContain("approve");
    expect(container.textContent).toContain("deploy");
    expect(container.textContent).toContain("read");
    expect(container.textContent).toContain("+2 more");
  });

  it("navigates to the clicked agent detail page", () => {
    render(<DelegationChainViz delegation={makeDelegation()} />);

    const nav = container.querySelector(
      '[data-testid="delegation-node-nav-agent-b"]',
    ) as HTMLButtonElement;
    expect(nav).toBeTruthy();

    act(() => {
      nav.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    expect(navigateMock).toHaveBeenCalledWith("/agents/agent-b");
  });

  it("shows the direct-call placeholder when the chain is empty", () => {
    render(<DelegationChainViz delegation={makeDelegation({ chain: [] })} />);

    expect(container.querySelector('[data-testid="delegation-direct-call"]')).toBeTruthy();
    expect(container.textContent).toContain("Direct call");
  });

  it("marks the tree as reduced-motion when animations are disabled", () => {
    motionState.shouldAnimate = false;
    render(<DelegationChainViz delegation={makeDelegation()} />);

    const tree = container.querySelector(
      '[data-testid="delegation-chain-tree"]',
    ) as HTMLElement;
    expect(tree.dataset.motionMode).toBe("reduced");
    const firstItem = tree.querySelector("li") as HTMLElement;
    expect(firstItem.style.animationDelay).toBe("");
  });
});
