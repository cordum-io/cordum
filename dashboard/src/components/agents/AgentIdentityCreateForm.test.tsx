/*
 * Focused tests for the empty-state Create-Identity form added for issue
 * #314. Covers:
 *   - heartbeat-based pre-fill (display name, pool, description hint)
 *   - fallback prettifier when no heartbeat is available
 *   - mutation invocation with the operator-edited body
 *   - error surface from the mutation hook
 */
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Worker } from "@/api/types";
import { renderWithProviders } from "@/test-utils/render";
import AgentIdentityCreateForm from "./AgentIdentityCreateForm";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

/* ------------------------------------------------------------------ */
/* Mocks                                                               */
/* ------------------------------------------------------------------ */

const { mutationState } = vi.hoisted(() => ({
  mutationState: {
    mutate: vi.fn(),
    isPending: false as boolean,
    isError: false as boolean,
    error: null as Error | null,
  },
}));

vi.mock("@/hooks/useAgentIdentities", () => ({
  useCreateAgentIdentity: () => mutationState,
}));

/* ------------------------------------------------------------------ */
/* Helpers                                                             */
/* ------------------------------------------------------------------ */

function render(ui: ReactNode) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root.render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
  });
  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

function getInput(container: HTMLElement, label: string): HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement {
  const labels = Array.from(container.querySelectorAll("label"));
  for (const lbl of labels) {
    if (lbl.textContent?.includes(label)) {
      const input = lbl.querySelector("input, textarea, select");
      if (input) return input as HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement;
    }
  }
  throw new Error(`No input found under label containing "${label}"`);
}

function makeHeartbeat(over: Partial<Worker> = {}): Worker {
  return {
    id: "support-triage-bot",
    name: "Support Triage Bot",
    pool: "support-llm",
    capabilities: [],
    status: "online",
    activeJobs: 0,
    capacity: 4,
    type: "llm",
    ...over,
  };
}

beforeEach(() => {
  mutationState.mutate = vi.fn();
  mutationState.isPending = false;
  mutationState.isError = false;
  mutationState.error = null;
});

/* ------------------------------------------------------------------ */
/* Tests                                                               */
/* ------------------------------------------------------------------ */

describe("AgentIdentityCreateForm", () => {
  // Accessibility gate for this new customer-visible surface: zero WCAG 2
  // A/AA violations. Uses the shared renderWithProviders + runAxe per the
  // dashboard testing guidelines (CodeRabbit #314 review).
  it("has no WCAG 2 A/AA violations", async () => {
    await renderWithProviders(
      <AgentIdentityCreateForm
        agentId="support-triage-bot"
        heartbeat={makeHeartbeat()}
        defaultOwner="alice"
      />,
      { runAxe: true },
    );
  });

  it("pre-fills name and pool from the worker heartbeat", () => {
    const { container, cleanup } = render(
      <AgentIdentityCreateForm
        agentId="support-triage-bot"
        heartbeat={makeHeartbeat()}
        defaultOwner="alice"
      />,
    );
    try {
      const name = getInput(container, "Display name") as HTMLInputElement;
      const owner = getInput(container, "Owner") as HTMLInputElement;
      expect(name.value).toBe("Support Triage Bot");
      expect(owner.value).toBe("alice");
      // Pool surfaced as a pre-fill hint so the operator knows what's
      // being attached to the identity.
      expect(container.textContent).toContain("allowed_pools=[support-llm]");
    } finally {
      cleanup();
    }
  });

  it("falls back to a deterministic prettifier when no heartbeat is available", () => {
    const { container, cleanup } = render(
      <AgentIdentityCreateForm agentId="support-triage-bot" />,
    );
    try {
      const name = getInput(container, "Display name") as HTMLInputElement;
      // "support-triage-bot" -> "Support Triage Bot"
      expect(name.value).toBe("Support Triage Bot");
    } finally {
      cleanup();
    }
  });

  it("submits the mutation with the edited body", () => {
    const { container, cleanup } = render(
      <AgentIdentityCreateForm
        agentId="bot"
        // heartbeat WITHOUT a display name forces the prettify(agentId)
        // fallback — useful so the test asserts the prettifier output too.
        heartbeat={makeHeartbeat({ id: "bot", name: "", pool: "p" })}
        defaultOwner="alice"
      />,
    );
    try {
      // Operator edits the risk tier before saving.
      const tier = getInput(container, "Risk tier") as unknown as HTMLSelectElement;
      act(() => {
        tier.value = "high";
        tier.dispatchEvent(new Event("change", { bubbles: true }));
      });
      const form = container.querySelector("form")!;
      act(() => {
        form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      });
      expect(mutationState.mutate).toHaveBeenCalledTimes(1);
      const [body] = mutationState.mutate.mock.calls[0];
      // agent_id must link the identity to this worker, else the panel never
      // resolves and audit attribution stays on the raw id (#314 regression).
      expect(body.agent_id).toBe("bot");
      expect(body.name).toBe("Bot");
      expect(body.owner).toBe("alice");
      expect(body.risk_tier).toBe("high");
      expect(body.allowed_pools).toEqual(["p"]);
    } finally {
      cleanup();
    }
  });

  it("surfaces mutation errors above the submit button", () => {
    mutationState.isError = true;
    mutationState.error = new Error("agent identity already exists");
    const { container, cleanup } = render(
      <AgentIdentityCreateForm agentId="bot" defaultOwner="alice" />,
    );
    try {
      expect(container.textContent).toContain("agent identity already exists");
    } finally {
      cleanup();
    }
  });

  it("disables the submit button while the mutation is in flight", () => {
    mutationState.isPending = true;
    const { container, cleanup } = render(
      <AgentIdentityCreateForm agentId="bot" defaultOwner="alice" />,
    );
    try {
      const submit = Array.from(container.querySelectorAll("button")).find(
        (b) => b.getAttribute("type") === "submit",
      );
      expect(submit).not.toBeNull();
      expect(submit?.disabled).toBe(true);
      expect(submit?.textContent).toContain("Creating");
    } finally {
      cleanup();
    }
  });
});
