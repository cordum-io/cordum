import {
  NuqsTestingAdapter,
  type UrlUpdateEvent,
} from "nuqs/adapters/testing";
import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { DlqRouteRedirect } from "@/App";
import { http, HttpResponse, server } from "@/test-utils/msw";
import {
  fireEvent,
  renderWithProviders,
  screen,
  waitFor,
  within,
} from "@/test-utils/render";
import JobsPage from "./JobsPage";

function LocationProbe() {
  const location = useLocation();
  return (
    <div data-testid="location">
      {location.pathname}
      {location.search}
    </div>
  );
}

function JobsHarness({
  searchParams = "",
  onUrlUpdate,
}: {
  searchParams?: string;
  onUrlUpdate?: (event: UrlUpdateEvent) => void;
}) {
  return (
    <NuqsTestingAdapter
      searchParams={searchParams}
      onUrlUpdate={onUrlUpdate}
    >
      <Routes>
        <Route path="/jobs" element={<JobsPage />} />
        <Route path="/jobs/:id" element={<div>Job detail</div>} />
        <Route path="*" element={<Navigate to="/jobs" replace />} />
      </Routes>
    </NuqsTestingAdapter>
  );
}

function RedirectHarness() {
  return (
    <>
      <LocationProbe />
      <Routes>
        <Route path="/dlq" element={<DlqRouteRedirect />} />
        <Route path="/jobs" element={<div>Jobs route</div>} />
      </Routes>
    </>
  );
}

function backendJob(index: number) {
  return {
    id: `job-${String(index).padStart(4, "0")}`,
    topic: `job.topic.${index}`,
    state: index % 2 === 0 ? "RUNNING" : "SUCCEEDED",
    updated_at: Date.parse("2026-05-08T12:00:00Z") * 1000 + index,
    attempts: index % 3,
  };
}

const dlqItems = [
  {
    job_id: "dlq-job-1",
    topic: "job.dlq.email",
    status: "failed",
    reason: "terminal policy error",
    reason_code: "terminal_failure",
    last_state: "failed_fatal",
    attempts: 2,
    created_at: "2026-05-08T12:30:00Z",
  },
  {
    job_id: "dlq-job-2",
    topic: "job.dlq.billing",
    status: "failed",
    reason: "exhausted retries",
    reason_code: "max_retries",
    last_state: "failed_retryable",
    attempts: 4,
    created_at: "2026-05-08T12:31:00Z",
  },
];

describe("JobsPage DLQ fold", () => {
  it("redirects /dlq to /jobs?status=dlq", async () => {
    renderWithProviders(<RedirectHarness />, {
      initialEntries: ["/dlq"],
    });

    await waitFor(() => {
      expect(screen.getByTestId("location").textContent).toBe(
        "/jobs?status=dlq",
      );
    });
  });

  it("roundtrips the status filter in URL and virtualizes 1000 job rows", async () => {
    const urlUpdates: UrlUpdateEvent[] = [];
    server.use(
      http.get("*/api/v1/jobs", () =>
        HttpResponse.json({
          items: Array.from({ length: 1000 }, (_, index) => backendJob(index)),
          total: 1000,
        }),
      ),
      http.get("*/api/v1/dlq/page", () =>
        HttpResponse.json({ items: [], next_cursor: null }),
      ),
    );

    const { container } = renderWithProviders(
      <JobsHarness onUrlUpdate={(event) => urlUpdates.push(event)} />,
      {
        initialEntries: ["/jobs"],
      },
    );

    await waitFor(() => {
      expect(container.querySelector("[data-virtualized='true']")).not.toBeNull();
    });

    fireEvent.click(screen.getByRole("tab", { name: /^Failed/ }));

    await waitFor(() => {
      expect(urlUpdates.at(-1)?.queryString).toBe("?status=failed");
    });

    fireEvent.click(screen.getByRole("tab", { name: "All" }));

    await waitFor(() => {
      expect(urlUpdates.at(-1)?.queryString).toBe("");
    });
  });

  it("uses the DLQ source and gates replay/drop row actions", async () => {
    let jobsHits = 0;
    let dlqHits = 0;
    let retryHits = 0;
    let dropHits = 0;

    server.use(
      http.get("*/api/v1/jobs", () => {
        jobsHits += 1;
        return HttpResponse.json({ items: [backendJob(1)], total: 1 });
      }),
      http.get("*/api/v1/dlq/page", ({ request }) => {
        dlqHits += 1;
        expect(new URL(request.url).searchParams.get("limit")).toBe("1000");
        return HttpResponse.json({ items: dlqItems, next_cursor: null });
      }),
      http.post("*/api/v1/dlq/:id/retry", ({ params }) => {
        retryHits += 1;
        expect(params.id).toBe("dlq-job-1");
        return new HttpResponse(null, { status: 204 });
      }),
      http.delete("*/api/v1/dlq/:id", ({ params }) => {
        dropHits += 1;
        expect(params.id).toBe("dlq-job-2");
        return new HttpResponse(null, { status: 204 });
      }),
    );

    renderWithProviders(
      <JobsHarness searchParams="?status=dlq&safety=deny" />,
      { initialEntries: ["/jobs"] },
    );

    expect(await screen.findByText(/Dead-letter queue/)).toBeTruthy();
    expect(screen.getByText(/failed terminally/i)).toBeTruthy();
    expect(screen.getByText(/Safety filter does not apply/i)).toBeTruthy();
    expect(await screen.findByText("job.dlq.email")).toBeTruthy();
    expect(screen.getByText("job.dlq.billing")).toBeTruthy();

    await waitFor(() => {
      expect(dlqHits).toBeGreaterThan(0);
      expect(jobsHits).toBe(0);
    });

    fireEvent.click(
      screen.getByRole("button", { name: "Actions for DLQ entry dlq-job-1" }),
    );
    fireEvent.click(screen.getByRole("menuitem", { name: "Replay" }));
    expect(screen.getByRole("dialog", { name: "Replay DLQ entry?" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(retryHits).toBe(0);

    fireEvent.click(
      screen.getByRole("button", { name: "Actions for DLQ entry dlq-job-1" }),
    );
    fireEvent.click(screen.getByRole("menuitem", { name: "Replay" }));
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "Replay DLQ entry?" }))
        .getByRole("button", { name: "Replay" }),
    );

    await waitFor(() => expect(retryHits).toBe(1));

    fireEvent.click(
      screen.getByRole("button", { name: "Actions for DLQ entry dlq-job-2" }),
    );
    fireEvent.click(screen.getByRole("menuitem", { name: "Drop" }));

    const dropDialog = screen.getByRole("dialog", { name: "Drop DLQ entry?" });
    const dropConfirm = within(dropDialog).getByRole("button", { name: "Drop" });
    expect((dropConfirm as HTMLButtonElement).disabled).toBe(true);

    fireEvent.change(within(dropDialog).getByPlaceholderText("dlq-job-2"), {
      target: { value: "dlq-job-2" },
    });
    expect((dropConfirm as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(dropConfirm);

    await waitFor(() => expect(dropHits).toBe(1));
  });
});
