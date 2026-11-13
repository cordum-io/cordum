// Held-out characterization tests (task-722f3b29): pin the RecentActivityList
// table behavior that the TanStack v9 migration (task-fa2b4cef) must preserve.
import { describe, expect, it, vi } from "vitest";
import { Route, Routes, useParams } from "react-router-dom";
import type { Job } from "@/api/types";
import { fireEvent, renderWithProviders, screen } from "@/test-utils/render";
import { RecentActivityList } from "./RecentActivityList";

vi.mock("@/components/home/SafetyDecisionFeed", () => ({
  SafetyDecisionFeed: () => <div data-testid="safety-feed-stub" />,
}));

function makeJob(id: string, topic: string, status: Job["status"]): Job {
  return {
    id,
    type: "job",
    topic,
    status,
    pool: "default",
    capabilities: [],
    riskTags: [],
    metadata: {},
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
  };
}

// Deliberately not sorted by any column, so any reorder is visible.
const jobs: Job[] = [
  makeJob("job-charlie-0003", "topic.charlie", "running"),
  makeJob("job-alpha-00001", "topic.alpha", "failed"),
  makeJob("job-echo-000005", "topic.echo", "succeeded"),
  makeJob("job-bravo-00002", "topic.bravo", "pending"),
  makeJob("job-delta-00004", "topic.delta", "denied"),
  makeJob("job-foxtrot-006", "topic.foxtrot", "running"),
  makeJob("job-golf-000007", "topic.golf", "running"),
];

function JobProbe() {
  const { id } = useParams();
  return <div data-testid="job-probe">{id}</div>;
}

function renderList(list: Job[]) {
  return renderWithProviders(
    <Routes>
      <Route path="/" element={<RecentActivityList jobs={list} />} />
      <Route path="/jobs/:id" element={<JobProbe />} />
    </Routes>,
  );
}

const headerCells = (): HTMLTableCellElement[] =>
  Array.from(document.querySelectorAll<HTMLTableCellElement>("thead th"));

const bodyRows = (): HTMLTableRowElement[] =>
  Array.from(document.querySelectorAll<HTMLTableRowElement>("tbody > tr"));

const topicColumn = (): string[] =>
  bodyRows().map((tr) => tr.querySelectorAll("td")[1]?.textContent ?? "");

describe("RecentActivityList table characterization", () => {
  it("renders the Job ID, Topic, Status, Safety, Time headers in order, none sortable", () => {
    renderList(jobs);
    expect(headerCells().map((th) => th.textContent?.trim())).toEqual([
      "Job ID",
      "Topic",
      "Status",
      "Safety",
      "Time",
    ]);
    for (const th of headerCells()) {
      expect(th.hasAttribute("aria-sort")).toBe(false);
      expect(th.hasAttribute("tabindex")).toBe(false);
    }
  });

  it("renders at most 5 rows, in input order, with the truncated job id cell", () => {
    renderList(jobs);
    expect(bodyRows()).toHaveLength(5);
    expect(topicColumn()).toEqual([
      "topic.charlie",
      "topic.alpha",
      "topic.echo",
      "topic.bravo",
      "topic.delta",
    ]);
    expect(bodyRows()[0].querySelector("td")?.textContent).toBe("job-charlie-");
    expect(screen.queryByText("topic.foxtrot")).toBeNull();
  });

  it("keeps the row order when a header is clicked", () => {
    renderList(jobs);
    const before = topicColumn();
    for (const th of headerCells()) {
      fireEvent.click(th);
      fireEvent.keyDown(th, { key: "Enter" });
    }
    expect(topicColumn()).toEqual(before);
  });

  it("navigates to /jobs/<id> when a row is clicked", () => {
    renderList(jobs);
    fireEvent.click(bodyRows()[1]);
    expect(screen.getByTestId("job-probe").textContent).toBe("job-alpha-00001");
  });

  it("shows the No recent jobs empty state when jobs is empty", () => {
    renderList([]);
    expect(screen.getByText("No recent jobs")).toBeTruthy();
    expect(bodyRows()).toHaveLength(1);
  });
});
