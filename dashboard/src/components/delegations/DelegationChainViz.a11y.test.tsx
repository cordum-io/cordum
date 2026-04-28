import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import axe from "axe-core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DelegationView } from "@/api/types";
import { DelegationChainViz } from "./DelegationChainViz";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState, motionState, navigateMock } = vi.hoisted(() => ({
  hookState: {
    mutateAsync: vi.fn().mockResolvedValue({ jti: "dlg-1", cascadedCount: 2 }),
    isPending: false,
  },
  motionState: {
    shouldAnimate: false,
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

function makeDelegation(): DelegationView {
  return {
    jti: "dlg-1",
    issuer: "agent-root",
    subject: "agent-middle",
    audience: "agent-leaf",
    allowedActions: ["approve", "deploy"],
    allowedTopics: ["jobs.run"],
    chain: [
      {
        agentId: "agent-root",
        issuedAt: "2026-04-21T00:00:00Z",
        expiresAt: "2099-04-21T01:00:00Z",
        jti: "dlg-root",
        issuedBy: "cordum",
      },
      {
        agentId: "agent-middle",
        issuedAt: "2026-04-21T00:10:00Z",
        expiresAt: "2099-04-21T01:00:00Z",
        jti: "dlg-1",
        parentJti: "dlg-root",
        issuedBy: "agent-root",
      },
    ],
    chainDepth: 2,
    issuedAt: "2026-04-21T00:10:00Z",
    expiresAt: "2099-04-21T01:00:00Z",
    revoked: false,
  };
}

async function renderViz() {
  await act(async () => {
    root.render(
      <MemoryRouter>
        <DelegationChainViz
          delegation={makeDelegation()}
          loadCascadeCount={async () => 2}
        />
      </MemoryRouter>,
    );
    await Promise.resolve();
  });
}

describe("DelegationChainViz accessibility", () => {
  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
  });

  it("exposes tree semantics, status labels, and revoke consequences for assistive tech", async () => {
    await renderViz();

    const tree = container.querySelector('[role="tree"]');
    expect(tree).toBeTruthy();
    const treeItems = container.querySelectorAll('[role="treeitem"]');
    expect(treeItems.length).toBe(3);
    expect(treeItems[0]?.getAttribute("aria-selected")).toBe("false");
    expect(treeItems[2]?.getAttribute("aria-selected")).toBe("true");
    expect(container.querySelector('[aria-label="Delegation status: active"]')).toBeTruthy();

    const revokeButton = container.querySelector(
      '[data-testid="delegation-revoke-trigger"]',
    ) as HTMLButtonElement | null;
    expect(revokeButton).not.toBeNull();
    const descriptionId = revokeButton?.getAttribute("aria-describedby");
    expect(descriptionId).toBeTruthy();
    const description = descriptionId
      ? container.querySelector(`#${descriptionId}`)
      : null;
    expect(description?.textContent).toContain("Revoking cascades to 2 downstream delegations.");
  });

  it("has zero axe-core violations for the rendered delegation tree", async () => {
    await renderViz();

    const results = await axe.run(container, {
      rules: {
        region: { enabled: false },
        "color-contrast": { enabled: false },
      },
    });

    expect(results.violations).toEqual([]);
  });
});
