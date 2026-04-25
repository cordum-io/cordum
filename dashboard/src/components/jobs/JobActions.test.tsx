import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { JobActions } from "./JobActions";
import type { Job } from "@/api/types";

const { hookState, toastState } = vi.hoisted(() => ({
  hookState: {
    cancelMutation: {
      mutate: vi.fn(),
      isPending: false,
    },
    retryMutation: {
      mutate: vi.fn(),
      isPending: false,
    },
  },
  toastState: {
    error: vi.fn(),
  },
}));

vi.mock("../../hooks/useJobs", () => ({
  useCancelJob: () => hookState.cancelMutation,
  useRetryJob: () => hookState.retryMutation,
}));

vi.mock("sonner", () => ({
  toast: {
    error: toastState.error,
  },
}));

function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "job-1234567890",
    topic: "job.code-review",
    status: "running",
    createdAt: "2026-01-01T00:00:00.000Z",
    updatedAt: "2026-01-01T00:00:10.000Z",
    attempts: 1,
    labels: {},
    ...overrides,
  } as Job;
}

function renderJobActions(job: Job) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={["/jobs/job-123"]}>
        <JobActions job={job} />
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
  if (!element) throw new Error("Expected element to exist before clicking");
  act(() => {
    element.dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
  });
}

describe("JobActions error handling", () => {
  beforeEach(() => {
    hookState.cancelMutation = {
      mutate: vi.fn(),
      isPending: false,
    };
    hookState.retryMutation = {
      mutate: vi.fn(),
      isPending: false,
    };
    toastState.error.mockReset();
  });

  it("shows an error toast and keeps the cancel dialog open when cancel fails", () => {
    hookState.cancelMutation.mutate = vi.fn((_jobId, options) => {
      options?.onError?.(new Error("cancel failed"));
    });

    const { container, cleanup } = renderJobActions(makeJob());
    try {
      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Cancel"),
        ) ?? null,
      );

      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Cancel Job"),
        ) ?? null,
      );

      expect(toastState.error).toHaveBeenCalledTimes(1);
      expect(container.textContent).toContain("Cancel Job");
    } finally {
      cleanup();
    }
  });

  it("shows an error toast and keeps the retry dialog open when retry fails", () => {
    hookState.retryMutation.mutate = vi.fn((_input, options) => {
      options?.onError?.(new Error("retry failed"));
    });

    const { container, cleanup } = renderJobActions(
      makeJob({ status: "failed" }),
    );
    try {
      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Retry"),
        ) ?? null,
      );

      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Retry Job"),
        ) ?? null,
      );

      expect(toastState.error).toHaveBeenCalledTimes(1);
      expect(container.textContent).toContain("Retry Job");
    } finally {
      cleanup();
    }
  });
});
