import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: () => ({
      matches: false,
      media: "",
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
});

import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const mockSimulateMutate = vi.fn();
const mockExplainMutate = vi.fn();

vi.mock("../../hooks/usePolicies", () => ({
  useSimulatePolicy: () => ({
    mutate: mockSimulateMutate,
    data: null,
    isPending: false,
    isError: false,
    error: null,
  }),
  useExplainPolicy: () => ({
    mutate: mockExplainMutate,
    data: null,
    isPending: false,
    isError: false,
    error: null,
  }),
}));

vi.mock("../../hooks/useAuth", () => ({
  useAuth: () => ({
    tenantId: "t1",
    user: { id: "user-1" },
    principalId: "principal-1",
  }),
}));

import { PolicySimulator } from "./PolicySimulator";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;
let queryClient: QueryClient;

function render(props: { bundleId: string; mode?: "simulate" | "explain" }) {
  act(() => {
    root.render(
      React.createElement(
        QueryClientProvider,
        { client: queryClient },
        React.createElement(
          MemoryRouter,
          null,
          React.createElement(PolicySimulator, props),
        ),
      ),
    );
  });
}

function getInputByPlaceholder(placeholder: string): HTMLInputElement | null {
  return container.querySelector(`input[placeholder="${placeholder}"]`);
}

function getButtonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll("button")).find((b) =>
    b.textContent?.includes(text),
  );
}

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  mockSimulateMutate.mockClear();
  mockExplainMutate.mockClear();
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  queryClient.clear();
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("PolicySimulator", () => {
  it("renders form inputs: topic, capability, requires, risk tags", () => {
    render({ bundleId: "b1" });
    expect(getInputByPlaceholder("job.example.task")).not.toBeNull();
    expect(getInputByPlaceholder("e.g. shell")).not.toBeNull();
    expect(getInputByPlaceholder("e.g. file_write, network")).not.toBeNull();
    expect(getInputByPlaceholder("e.g. destructive, pii, external")).not.toBeNull();
  });

  it("renders Simulate heading in simulate mode", () => {
    render({ bundleId: "b1", mode: "simulate" });
    expect(container.textContent).toContain("Simulate Policy Evaluation");
  });

  it("renders Explain heading in explain mode", () => {
    render({ bundleId: "b1", mode: "explain" });
    expect(container.textContent).toContain("Explain Policy Decision");
  });

  it("shows validation error when topic is empty on Test click", () => {
    render({ bundleId: "b1" });

    const testBtn = getButtonByText("Test");
    expect(testBtn).toBeDefined();

    act(() => {
      testBtn!.click();
    });

    expect(container.textContent).toContain("Topic is required");
    expect(mockSimulateMutate).not.toHaveBeenCalled();
  });

  it("calls simulate.mutate with correct payload when topic is filled", () => {
    render({ bundleId: "b1" });

    const topicInput = getInputByPlaceholder("job.example.task")!;
    act(() => {
      const nativeInputValueSetter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!;
      nativeInputValueSetter.call(topicInput, "job.test.run");
      topicInput.dispatchEvent(new Event("input", { bubbles: true }));
    });

    const testBtn = getButtonByText("Test");
    act(() => {
      testBtn!.click();
    });

    expect(mockSimulateMutate).toHaveBeenCalledTimes(1);
    const payload = mockSimulateMutate.mock.calls[0][0];
    expect(payload.bundleId).toBe("b1");
    expect(payload.request.topic).toBe("job.test.run");
  });

  it("calls explain.mutate in explain mode", () => {
    render({ bundleId: "b1", mode: "explain" });

    const topicInput = getInputByPlaceholder("job.example.task")!;
    act(() => {
      const nativeInputValueSetter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!;
      nativeInputValueSetter.call(topicInput, "job.test.explain");
      topicInput.dispatchEvent(new Event("input", { bubbles: true }));
    });

    const explainBtn = getButtonByText("Explain");
    act(() => {
      explainBtn!.click();
    });

    expect(mockExplainMutate).toHaveBeenCalledTimes(1);
    expect(mockExplainMutate.mock.calls[0][0].request.topic).toBe("job.test.explain");
  });

  it("renders Test button in simulate mode and Explain button in explain mode", () => {
    render({ bundleId: "b1", mode: "simulate" });
    expect(getButtonByText("Test")).toBeDefined();

    act(() => root.unmount());
    container.remove();
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);

    render({ bundleId: "b1", mode: "explain" });
    expect(getButtonByText("Explain")).toBeDefined();
  });

  it("renders Metadata section with Add button", () => {
    render({ bundleId: "b1" });
    expect(container.textContent).toContain("Metadata");
    expect(container.textContent).toContain("No metadata entries");

    const addBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.trim() === "Add",
    );
    expect(addBtn).toBeDefined();
  });
});
