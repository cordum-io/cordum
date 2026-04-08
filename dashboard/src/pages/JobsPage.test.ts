import React, { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readStoredJobsPageSize, SubmitJobDialog } from "./JobsPage";

const { hookState } = vi.hoisted(() => ({
  hookState: {
    submitJob: {
      mutate: vi.fn(),
      isPending: false,
    },
  },
}));

vi.mock("@/hooks/useJobs", () => ({
  useSubmitJob: () => hookState.submitJob,
}));

describe("readStoredJobsPageSize", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("returns the stored page size when valid", () => {
    window.localStorage.setItem("cordum-jobs-page-size", "100");
    expect(readStoredJobsPageSize()).toBe(100);
  });

  it("falls back to the default when storage throws", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });

    expect(readStoredJobsPageSize()).toBe(50);
  });

  it("falls back to the default when the stored value is invalid", () => {
    window.localStorage.setItem("cordum-jobs-page-size", "not-a-number");
    expect(readStoredJobsPageSize()).toBe(50);
  });
});

describe("SubmitJobDialog accessibility", () => {
  beforeEach(() => {
    hookState.submitJob = {
      mutate: vi.fn(),
      isPending: false,
    };
  });

  it("focuses the topic field on open and restores focus to the trigger on close", () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);

    function Harness() {
      const [open, setOpen] = useState(false);
      return React.createElement(
        MemoryRouter,
        { initialEntries: ["/jobs"] },
        React.createElement(
          React.Fragment,
          null,
          React.createElement(
            "button",
            { type: "button", onClick: () => setOpen(true) },
            "Open submit dialog",
          ),
          React.createElement(SubmitJobDialog, {
            open,
            onClose: () => setOpen(false),
          }),
        ),
      );
    }

    try {
      act(() => {
        root.render(React.createElement(Harness));
      });

      const trigger = Array.from(container.querySelectorAll("button")).find(
        (button) => button.textContent?.includes("Open submit dialog"),
      ) as HTMLButtonElement | undefined;

      expect(trigger).toBeDefined();

      act(() => {
        trigger?.focus();
        trigger?.click();
      });

      const topicInput = container.querySelector(
        'input[aria-label="Job topic"]',
      ) as HTMLInputElement | null;
      expect(topicInput).not.toBeNull();
      expect(document.activeElement).toBe(topicInput);

      const closeButton = container.querySelector(
        'button[aria-label="Close submit job dialog"]',
      ) as HTMLButtonElement | null;
      expect(closeButton).not.toBeNull();

      act(() => {
        closeButton?.click();
      });

      expect(document.activeElement).toBe(trigger);
    } finally {
      act(() => root.unmount());
      container.remove();
    }
  });
});
