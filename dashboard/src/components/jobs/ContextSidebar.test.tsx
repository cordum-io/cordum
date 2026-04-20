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
import type { Job, SafetyDecision } from "../../api/types";

// ---------------------------------------------------------------------------
// Mock state
// ---------------------------------------------------------------------------

const { mockMemoryData, mockClipboard, mockAddToast } = vi.hoisted(() => ({
  mockMemoryData: { data: undefined as unknown, isLoading: false, isError: false },
  mockClipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
  mockAddToast: vi.fn(),
}));

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("../../hooks/useMemory", () => ({
  useMemory: () => mockMemoryData,
}));

vi.mock("../../hooks/useJobArtifacts", () => ({
  useJobArtifacts: () => ({ data: [], isLoading: false, isError: false }),
}));

vi.mock("../../state/toast", () => {
  const store = { addToast: mockAddToast };
  const useToastStore = Object.assign(
    (selector: (s: typeof store) => unknown) => selector(store),
    { getState: () => store },
  );
  return { useToastStore };
});

vi.mock("../../state/config", () => ({
  useConfigStore: Object.assign(
    (selector: (s: Record<string, unknown>) => unknown) =>
      selector({ apiBaseUrl: "/api/v1", apiKey: "", user: null, principalRole: "" }),
    { getState: () => ({ apiBaseUrl: "/api/v1", apiKey: "", user: null, principalRole: "" }) },
  ),
}));

vi.mock("../../lib/logger", () => ({
  logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

// Stub navigator.clipboard
Object.defineProperty(navigator, "clipboard", {
  value: mockClipboard,
  writable: true,
});

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "j-test-12345678",
    type: "default",
    topic: "sys.job.submit",
    status: "succeeded",
    pool: "default-pool",
    capabilities: [],
    riskTags: [],
    metadata: {},
    createdAt: "2026-02-13T12:00:00.000Z",
    updatedAt: "2026-02-13T12:05:00.000Z",
    ...overrides,
  };
}

let ContextSidebar: React.ComponentType<{ job: Job; decisions?: SafetyDecision[] | null }>;

beforeEach(async () => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);

  mockMemoryData.data = undefined;
  mockMemoryData.isLoading = false;
  mockMemoryData.isError = false;
  vi.clearAllMocks();

  const mod = await import("./ContextSidebar");
  ContextSidebar = mod.ContextSidebar;
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render(job: Job, decisions?: SafetyDecision[] | null) {
  act(() => {
    root.render(<ContextSidebar job={job} decisions={decisions} />);
  });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("ContextSidebar", () => {
  it("renders all 5 tab buttons", () => {
    render(makeJob());
    const buttons = container.querySelectorAll("[role='tab']");
    expect(buttons).toHaveLength(5);
    const labels = Array.from(buttons).map((b) => b.textContent);
    expect(labels).toEqual(["Input", "Output", "Artifacts", "Memory", "Raw"]);
  });

  it("auto-selects Input tab for running job", () => {
    render(makeJob({ status: "running" }));
    const selected = container.querySelector("[aria-selected='true']");
    expect(selected?.textContent).toBe("Input");
  });

  it("auto-selects Output tab for succeeded job", () => {
    render(makeJob({ status: "succeeded", resultPtr: "ptr-123" }));
    // succeeded → output, but only if resultPtr exists; fallback is input → raw
    // Actually the auto-select logic always returns "output" for succeeded regardless of resultPtr
    const selected = container.querySelector("[aria-selected='true']");
    expect(selected?.textContent).toBe("Output");
  });

  it("auto-selects Output tab for quarantined job", () => {
    render(makeJob({ status: "output_quarantined" }));
    const selected = container.querySelector("[aria-selected='true']");
    expect(selected?.textContent).toBe("Output");
  });

  it("auto-selects Input tab for failed job", () => {
    render(makeJob({ status: "failed" }));
    const selected = container.querySelector("[aria-selected='true']");
    expect(selected?.textContent).toBe("Input");
  });

  it("auto-selects Raw tab when no contextPtr", () => {
    render(makeJob({ status: "pending", contextPtr: undefined }));
    const selected = container.querySelector("[aria-selected='true']");
    expect(selected?.textContent).toBe("Raw");
  });

  it("clicking a tab switches content", () => {
    render(makeJob({ status: "running" }));
    // Initially on Input tab
    expect(container.querySelector("[aria-selected='true']")?.textContent).toBe("Input");

    // Click Raw tab
    const rawTab = Array.from(container.querySelectorAll("[role='tab']")).find(
      (b) => b.textContent === "Raw",
    ) as HTMLButtonElement;
    act(() => rawTab.click());

    expect(container.querySelector("[aria-selected='true']")?.textContent).toBe("Raw");
    expect(container.textContent).toContain("Copy JSON");
  });

  it("Raw tab renders job JSON", () => {
    render(makeJob({ status: "succeeded" }));
    // Switch to Raw tab
    const rawTab = Array.from(container.querySelectorAll("[role='tab']")).find(
      (b) => b.textContent === "Raw",
    ) as HTMLButtonElement;
    act(() => rawTab.click());

    expect(container.textContent).toContain("j-test-12345678");
    expect(container.textContent).toContain("sys.job.submit");
  });

  it("Copy JSON button calls clipboard API", async () => {
    render(makeJob());
    // Switch to Raw tab
    const rawTab = Array.from(container.querySelectorAll("[role='tab']")).find(
      (b) => b.textContent === "Raw",
    ) as HTMLButtonElement;
    act(() => rawTab.click());

    const copyBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent === "Copy JSON",
    ) as HTMLButtonElement;
    await act(async () => copyBtn.click());

    expect(mockClipboard.writeText).toHaveBeenCalledTimes(1);
    const arg = mockClipboard.writeText.mock.calls[0][0] as string;
    expect(arg).toContain("j-test-12345678");
  });

  it("Output tab shows 'Job still in progress' for running jobs", () => {
    render(makeJob({ status: "running" }));
    // Switch to Output tab
    const outputTab = Array.from(container.querySelectorAll("[role='tab']")).find(
      (b) => b.textContent === "Output",
    ) as HTMLButtonElement;
    act(() => outputTab.click());

    expect(container.textContent).toContain("Job still in progress");
  });

  it("Output tab shows quarantine banner for quarantined jobs", () => {
    render(makeJob({
      status: "output_quarantined",
      output_safety: { decision: "QUARANTINE", redacted_ptr: "ptr-redacted" },
    }));

    expect(container.textContent).toContain("quarantined");
  });

  it("Input tab shows empty state when no contextPtr", () => {
    render(makeJob({ status: "failed", contextPtr: undefined }));
    // Switch to Input tab
    const inputTab = Array.from(container.querySelectorAll("[role='tab']")).find(
      (b) => b.textContent === "Input",
    ) as HTMLButtonElement;
    act(() => inputTab.click());

    expect(container.textContent).toContain("No input context recorded");
  });

  it("renders chat-style entries when memory has entries", () => {
    mockMemoryData.data = {
      pointer: "ptr-1",
      key: "ctx",
      kind: "context",
      size_bytes: 100,
      base64: "",
      entries: [
        { id: "e1", role: "user", content: "Hello world", timestamp: "2026-02-13T12:00:00.000Z" },
        { id: "e2", role: "assistant", content: "Hi there" },
      ],
    };

    render(makeJob({ status: "running", contextPtr: "ptr-1" }));

    expect(container.textContent).toContain("user");
    expect(container.textContent).toContain("Hello world");
    expect(container.textContent).toContain("assistant");
    expect(container.textContent).toContain("Hi there");
  });

  it("has sticky responsive classes on outer wrapper", () => {
    render(makeJob());
    const outer = container.firstElementChild as HTMLElement;
    expect(outer.className).toContain("lg:sticky");
    expect(outer.className).toContain("lg:top-20");
  });
});
