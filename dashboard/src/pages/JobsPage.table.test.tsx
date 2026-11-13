// Held-out characterization tests (task-722f3b29): pin the JobsPage table
// behavior that the TanStack v9 migration (task-fa2b4cef) must preserve.
import { describe, expect, it } from "vitest";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { fireEvent, renderWithProviders, waitFor } from "@/test-utils/render";
import { http, HttpResponse, server } from "@/test-utils/msw";
import JobsPage from "./JobsPage";

const MICROS = 1_000;
const BASE_MS = Date.parse("2026-04-25T00:00:00.000Z");

// Input order is sorted by neither topic nor updated_at, so both the
// initial Updated-desc sort and the Topic sort are order-sensitive.
const records = [
  { id: "job-bravo-0002", topic: "topic.bravo", state: "SUCCEEDED", attempts: 2, updated_at: (BASE_MS + 2_000) * MICROS },
  { id: "job-alpha-0001", topic: "topic.alpha", state: "FAILED", attempts: 1, updated_at: (BASE_MS + 1_000) * MICROS },
  { id: "job-charlie-003", topic: "topic.charlie", state: "RUNNING", attempts: 3, updated_at: (BASE_MS + 3_000) * MICROS },
];

function mockJobs(): void {
  server.use(
    http.get("*/api/v1/jobs", () => HttpResponse.json({ items: records, total: records.length })),
    http.get("*/api/v1/dlq/page", () => HttpResponse.json({ items: [], next_cursor: null })),
  );
}

function renderPage() {
  return renderWithProviders(
    <NuqsTestingAdapter searchParams="">
      <JobsPage />
    </NuqsTestingAdapter>,
  );
}

const headerCells = (root: HTMLElement): HTMLTableCellElement[] =>
  Array.from(root.querySelectorAll<HTMLTableCellElement>("thead th"));

const columnText = (root: HTMLElement, index: number): string[] =>
  Array.from(root.querySelectorAll("tbody tr")).map(
    (tr) => tr.querySelectorAll("td")[index]?.textContent?.trim() ?? "",
  );

describe("JobsPage table characterization", () => {
  it("renders the column headers in order with Origin not sortable", async () => {
    mockJobs();
    const { container } = renderPage();
    await waitFor(() => {
      expect(container.querySelectorAll("tbody tr").length).toBe(3);
    });

    const headers = headerCells(container);
    expect(headers.map((th) => th.textContent?.trim())).toEqual([
      "Status",
      "Job ID",
      "Topic",
      "Origin",
      "Safety Decision",
      "Attempts",
      "Updated",
    ]);
    const origin = headers[3];
    expect(origin.hasAttribute("aria-sort")).toBe(false);
    expect(origin.hasAttribute("tabindex")).toBe(false);
    for (const index of [0, 1, 2, 4, 5, 6]) {
      expect(headers[index].getAttribute("tabindex")).toBe("0");
    }
  });

  it("renders one row per mocked job, initially sorted by Updated descending", async () => {
    mockJobs();
    const { container } = renderPage();
    await waitFor(() => {
      expect(container.querySelectorAll("tbody tr").length).toBe(3);
    });

    expect(headerCells(container)[6].getAttribute("aria-sort")).toBe("descending");
    expect(columnText(container, 1)).toEqual(["job-charlie-003", "job-bravo-0002", "job-alpha-0001"]);
    expect(columnText(container, 2)).toEqual(["topic.charlie", "topic.bravo", "topic.alpha"]);
    expect(columnText(container, 5)).toEqual(["3", "2", "1"]);
  });

  it("reorders rows when a sortable header (Topic) is clicked, and ignores Origin clicks", async () => {
    mockJobs();
    const { container } = renderPage();
    await waitFor(() => {
      expect(container.querySelectorAll("tbody tr").length).toBe(3);
    });
    const headers = headerCells(container);

    fireEvent.click(headers[3]);
    expect(columnText(container, 2)).toEqual(["topic.charlie", "topic.bravo", "topic.alpha"]);

    fireEvent.click(headers[2]);
    await waitFor(() => {
      expect(columnText(container, 2)).toEqual(["topic.alpha", "topic.bravo", "topic.charlie"]);
    });
    expect(headers[2].getAttribute("aria-sort")).toBe("ascending");
    expect(headers[6].getAttribute("aria-sort")).toBe("none");
  });
});
