import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi, afterEach } from "vitest";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    run: {
      data: undefined as unknown,
      isLoading: false,
      isError: false,
      error: null as Error | null,
      refetch: vi.fn(),
    },
  },
}));

vi.mock("@/hooks/useEvals", () => ({
  useEvalRun: () => hookState.run,
}));

import EvalRunDetailPage from "./EvalRunDetailPage";
import type { EvalEntryResult, EvalRun, SafetyDecisionType } from "@/api/types";

function mkEntry(
  id: string,
  status: EvalEntryResult["status"],
  drift: EvalEntryResult["driftDirection"] = "unchanged",
  expected: SafetyDecisionType = "deny",
  actual: SafetyDecisionType | string = "allow",
  ruleId?: string,
): EvalEntryResult {
  return {
    entryId: id,
    input: { topic: "fs.delete", target: id },
    expectedDecision: expected,
    actualDecision: actual,
    ruleId,
    status,
    driftDirection: drift,
    reason: "policy drift",
  };
}

function mkRun(entries: EvalEntryResult[]): EvalRun {
  const regressions = entries.filter((e) => e.status === "regression").length;
  return {
    runId: "run-1",
    datasetId: "ds-1",
    datasetName: "denies",
    datasetVersion: 1,
    policySnapshot: "snap-abcdef123",
    startedAt: "2026-04-20T10:00:00Z",
    completedAt: "2026-04-20T10:00:05Z",
    summary: {
      total: entries.length,
      passed: entries.filter((e) => e.status === "pass").length,
      failed: entries.filter((e) => e.status === "fail").length,
      regressions,
      errored: entries.filter((e) => e.status === "error").length,
      scorePercent: entries.length
        ? Math.round(100 * (entries.filter((e) => e.status === "pass").length / entries.length))
        : null,
    },
    entries,
  };
}

function renderPage() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={["/evals/runs/run-1"]}>
          <Routes>
            <Route path="/evals/runs/:runId" element={<EvalRunDetailPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
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

describe("EvalRunDetailPage", () => {
  beforeEach(() => {
    hookState.run.data = undefined;
    hookState.run.isLoading = false;
    hookState.run.isError = false;
    hookState.run.error = null;
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("expands and collapses an entry row on click", () => {
    hookState.run.data = mkRun([
      mkEntry("e-1", "regression", "relaxed", "deny", "allow", "rule-1"),
    ]);
    const { container, cleanup } = renderPage();
    const row = container.querySelector('[data-testid="entry-row"]');
    expect(row).toBeTruthy();
    const btn = row!.querySelector<HTMLButtonElement>("button")!;
    expect(btn.getAttribute("aria-expanded")).toBe("false");
    act(() => {
      btn.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    expect(btn.getAttribute("aria-expanded")).toBe("true");
    expect(container.textContent).toContain("Input snapshot");
    act(() => {
      btn.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    expect(btn.getAttribute("aria-expanded")).toBe("false");
    cleanup();
  });

  it("pre-filters to regressions when the run has any", () => {
    hookState.run.data = mkRun([
      mkEntry("e-pass", "pass"),
      mkEntry("e-reg", "regression", "relaxed"),
    ]);
    const { container, cleanup } = renderPage();
    // With "Only regressions" default-on, only the regression row shows.
    const rows = container.querySelectorAll('[data-testid="entry-row"]');
    expect(rows.length).toBe(1);
    expect(rows[0]!.textContent).toContain("e-reg");
    cleanup();
  });

  it("rule filter narrows the visible list", () => {
    hookState.run.data = mkRun([
      mkEntry("e-1", "fail", "unchanged", "deny", "deny", "rule-a"),
      mkEntry("e-2", "fail", "unchanged", "deny", "deny", "rule-b"),
    ]);
    const { container, cleanup } = renderPage();
    // toggle off "Only regressions"
    const labels = Array.from(container.querySelectorAll("label"));
    const onlyReg = labels.find((l) => /Only regressions/.test(l.textContent ?? ""));
    const onlyRegCheckbox = onlyReg?.querySelector<HTMLInputElement>('input[type="checkbox"]');
    if (onlyRegCheckbox && onlyRegCheckbox.checked) {
      act(() => onlyRegCheckbox.click());
    }
    expect(container.querySelectorAll('[data-testid="entry-row"]').length).toBe(2);
    const ruleInput = container.querySelector<HTMLInputElement>('input[placeholder="rule-…"]')!;
    const proto = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value");
    act(() => {
      proto?.set?.call(ruleInput, "rule-b");
      ruleInput.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const rows = container.querySelectorAll('[data-testid="entry-row"]');
    expect(rows.length).toBe(1);
    expect(rows[0]!.textContent).toContain("e-2");
    cleanup();
  });

  it("engages virtualization at >200 entries", () => {
    const entries: EvalEntryResult[] = [];
    for (let i = 0; i < 201; i++) {
      entries.push(mkEntry(`e-${i}`, "fail"));
    }
    hookState.run.data = mkRun(entries);
    const { container, cleanup } = renderPage();
    // Toggle off "Only regressions" (this run has zero regressions so it's off by default)
    const list = container.querySelector('[data-testid="entry-list"]');
    expect(list?.getAttribute("data-virtualized")).toBe("true");
    const visibleRows = container.querySelectorAll('[data-testid="entry-row"]').length;
    expect(visibleRows).toBe(200);
    const showMore = Array.from(container.querySelectorAll("button")).find((b) =>
      /Show next/i.test(b.textContent ?? ""),
    );
    expect(showMore).toBeTruthy();
    cleanup();
  });

  it("verdict arrow color matches drift direction", () => {
    hookState.run.data = mkRun([
      mkEntry("e-esc", "regression", "escalated", "allow", "deny"),
      mkEntry("e-rel", "regression", "relaxed", "deny", "allow"),
    ]);
    const { container, cleanup } = renderPage();
    const arrows = container.querySelectorAll("[data-drift]");
    const directions = Array.from(arrows).map((el) => el.getAttribute("data-drift"));
    expect(directions).toContain("escalated");
    expect(directions).toContain("relaxed");
    cleanup();
  });
});
