import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DelegationView } from "@/api/types";
import DelegationsPage from "./DelegationsPage";

const { hookState } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

  return {
    hookState: {
      items: [] as DelegationView[],
      isLoading: false,
      isError: false,
      error: null as Error | null,
      refetch: vi.fn(),
      mutateAsync: vi.fn().mockResolvedValue({ jti: "del-2", cascadedCount: 0 }),
      isPending: false,
    },
  };
});

vi.mock("@/hooks/useDelegations", () => ({
  useAllDelegations: () => ({
    data: { pages: [{ items: hookState.items }] },
    isLoading: hookState.isLoading,
    isError: hookState.isError,
    error: hookState.error,
    refetch: hookState.refetch,
  }),
  useRevokeDelegation: () => ({
    mutateAsync: hookState.mutateAsync,
    isPending: hookState.isPending,
  }),
}));

vi.mock("@/components/delegations/DelegationChainViz", () => ({
  DelegationChainViz: ({ delegation }: { delegation: DelegationView }) => (
    <div data-testid="delegation-chain-viz">Delegation chain {delegation.jti}</div>
  ),
  countCascadeDescendants: () => 1,
  formatDelegationExpiry: () => "Expires soon",
  getDelegationNodeStatus: (delegation: DelegationView) =>
    delegation.revoked ? "revoked" : "active",
}));

function makeDelegation(overrides: Partial<DelegationView> = {}): DelegationView {
  return {
    jti: "del-1",
    issuer: "issuer-alpha",
    subject: "service-alpha",
    audience: "agent-alpha",
    allowedActions: ["read.logs", "jobs.submit"],
    allowedTopics: ["jobs.run"],
    chain: [],
    chainDepth: 1,
    issuedAt: "2026-04-21T08:00:00.000Z",
    expiresAt: "2026-04-21T10:00:00.000Z",
    revoked: false,
    ...overrides,
  };
}

function renderPage() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={["/delegations"]}>
        <DelegationsPage />
      </MemoryRouter>,
    );
  });

  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

function click(element: Element | null) {
  if (!element) throw new Error("Expected element before clicking");
  act(() => {
    element.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  });
}

function changeInput(element: HTMLInputElement, value: string) {
  act(() => {
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    setter?.call(element, value);
    element.dispatchEvent(new Event("input", { bubbles: true }));
    element.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

describe("DelegationsPage", () => {
  beforeEach(() => {
    hookState.items = [
      makeDelegation(),
      makeDelegation({
        jti: "del-2",
        issuer: "issuer-bravo",
        subject: "service-bravo",
        audience: "agent-bravo",
        allowedActions: ["review.approvals"],
        revoked: true,
      }),
    ];
    hookState.isLoading = false;
    hookState.isError = false;
    hookState.error = null;
    hookState.refetch = vi.fn();
    hookState.mutateAsync = vi.fn().mockResolvedValue({ jti: "del-2", cascadedCount: 0 });
    hookState.isPending = false;
  });

  it("filters delegations, opens the detail drawer, and supports row-level revocation", async () => {
    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("Delegations");
      expect(container.textContent).toContain("issuer-alpha");
      expect(container.textContent).toContain("issuer-bravo");

      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Revoked"),
        ) ?? null,
      );

      expect(container.textContent).toContain("issuer-bravo");
      expect(container.textContent).not.toContain("issuer-alpha");

      const searchInput = container.querySelector(
        'input[placeholder="Search by agent, topic, action, or token..."]',
      ) as HTMLInputElement | null;
      expect(searchInput).not.toBeNull();

      changeInput(searchInput!, "review");
      expect(container.textContent).toContain("issuer-bravo");

      click(container.querySelector("tbody tr"));

      expect(container.textContent).toContain("Delegation chain del-2");
      expect(container.textContent).toContain("service-bravo");

      click(
        container.querySelector('button[aria-label="Close delegation details"]'),
      );

      changeInput(searchInput!, "");
      click(
        container.querySelector('button[role="tab"][aria-label="All"]'),
      );

      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.trim() === "Revoke",
        ) ?? null,
      );

      await act(async () => {
        await Promise.resolve();
      });

      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.trim() === "Revoke delegation",
        ) ?? null,
      );

      expect(hookState.mutateAsync).toHaveBeenCalledWith({ jti: "del-1" });
    } finally {
      cleanup();
    }
  });
});
