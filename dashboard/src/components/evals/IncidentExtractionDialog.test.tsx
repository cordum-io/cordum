import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi, afterEach } from "vitest";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { mutationState, toastState } = vi.hoisted(() => ({
  mutationState: {
    mutateAsync: vi.fn(),
    isPending: false,
  },
  toastState: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("@/hooks/useEvals", () => ({
  useCreateDatasetFromIncidents: () => mutationState,
}));

vi.mock("sonner", () => ({
  toast: toastState,
}));

import { IncidentExtractionDialog } from "./IncidentExtractionDialog";
import { ApiError } from "@/api/client";

function renderDialog(open: boolean, onOpenChange = vi.fn()) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root.render(
      <QueryClientProvider client={qc}>
        <IncidentExtractionDialog open={open} onOpenChange={onOpenChange} />
      </QueryClientProvider>,
    );
  });
  return {
    container,
    onOpenChange,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

async function flush() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
}

function setInputValue(input: HTMLInputElement, value: string) {
  const proto = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    "value",
  );
  proto?.set?.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

describe("IncidentExtractionDialog", () => {
  beforeEach(() => {
    mutationState.mutateAsync = vi.fn();
    mutationState.isPending = false;
    toastState.success.mockReset();
    toastState.error.mockReset();
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("does not render content when closed", () => {
    const { container, cleanup } = renderDialog(false);
    expect(container.querySelector('[role="dialog"]')).toBeFalsy();
    cleanup();
  });

  it("renders the form when open", () => {
    const { container, cleanup } = renderDialog(true);
    expect(container.querySelector('[role="dialog"]')).toBeTruthy();
    expect(container.textContent).toContain("Create dataset from incidents");
    cleanup();
  });

  it("shows validation error when dataset name is invalid", async () => {
    const { container, cleanup } = renderDialog(true);
    const nameInput = container.querySelector<HTMLInputElement>('input[name="datasetName"]')!;
    act(() => {
      setInputValue(nameInput, "BadName!");
    });
    const submit = Array.from(container.querySelectorAll("button")).find((b) =>
      /Preview/i.test(b.textContent ?? ""),
    )!;
    act(() => {
      submit.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    await flush();
    expect(container.textContent).toMatch(/lowercase letters/i);
    expect(mutationState.mutateAsync).not.toHaveBeenCalled();
    cleanup();
  });

  it("renders dry-run preview counts after dry-run submit", async () => {
    mutationState.mutateAsync = vi.fn().mockResolvedValue({
      preview: {
        scannedDecisions: 500,
        entryCount: 42,
        dedupedCount: 8,
        warnings: ["clock skew"],
      },
    });
    const { container, cleanup } = renderDialog(true);
    const nameInput = container.querySelector<HTMLInputElement>('input[name="datasetName"]')!;
    act(() => {
      setInputValue(nameInput, "denies-2026-04");
    });
    const submit = Array.from(container.querySelectorAll("button")).find((b) =>
      /Preview/i.test(b.textContent ?? ""),
    )!;
    act(() => {
      submit.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    await flush();
    await flush();
    expect(mutationState.mutateAsync).toHaveBeenCalled();
    const call = mutationState.mutateAsync.mock.calls[0]![0];
    expect(call.datasetName).toBe("denies-2026-04");
    expect(call.dryRun).toBe(true);
    expect(container.textContent).toContain("42");
    expect(container.textContent).toContain("clock skew");
    cleanup();
  });

  it("shows 409 toast on collision and does not close", async () => {
    mutationState.mutateAsync = vi.fn().mockRejectedValue(new ApiError(409, "exists"));
    const onOpenChange = vi.fn();
    const { container, cleanup } = renderDialog(true, onOpenChange);
    const nameInput = container.querySelector<HTMLInputElement>('input[name="datasetName"]')!;
    act(() => {
      setInputValue(nameInput, "denies-2026-04");
    });
    // Toggle dry-run off so we're attempting a real submit.
    const dryRunCheckbox = Array.from(container.querySelectorAll<HTMLInputElement>("input[type=checkbox]")).find(
      (c) => c.nextSibling?.textContent?.includes("Dry-run preview") || false,
    );
    // Easier: find label with text
    const labels = Array.from(container.querySelectorAll("label"));
    const dryLabel = labels.find((l) => /Dry-run preview/.test(l.textContent ?? ""));
    const dryCheckbox = dryLabel?.querySelector<HTMLInputElement>('input[type="checkbox"]');
    if (dryCheckbox) {
      act(() => {
        dryCheckbox.click();
      });
    }
    const submit = Array.from(container.querySelectorAll("button")).find((b) =>
      /Create dataset/i.test(b.textContent ?? ""),
    )!;
    act(() => {
      submit.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    await flush();
    await flush();
    expect(toastState.error).toHaveBeenCalled();
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
    void dryRunCheckbox;
    cleanup();
  });
});
