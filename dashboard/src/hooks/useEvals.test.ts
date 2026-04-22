import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  createTestQueryClient,
  mockFetch,
  renderWithQueryClient,
} from "./__tests__/test-utils";
import {
  __evalsInternal,
  useCreateDatasetFromIncidents,
  useEvalDataset,
  useEvalDatasets,
  useEvalRun,
  useEvalRuns,
  useRunEvalDataset,
} from "./useEvals";
import { isRegressionRun } from "../api/transform";

const { mockConfigState, loggerMock } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "",
    tenantId: "",
    principalId: "",
    principalRole: "",
    user: null,
    logout: vi.fn(),
  },
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../lib/logger", () => ({ logger: loggerMock }));

describe("useEvals", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue(
      "00000000-0000-0000-0000-000000000001",
    );
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("useEvalDatasets", () => {
    it("fetches first page and exposes nextCursor", async () => {
      mockFetch([
        {
          match: "/evals/datasets",
          method: "GET",
          body: {
            items: [
              {
                id: "ds-1",
                name: "denies",
                version: 1,
                tenant: "acme",
                entry_count: 10,
                content_hash: "sha256:a",
                created_at: "2026-04-19T00:00:00Z",
                updated_at: "2026-04-19T00:00:00Z",
              },
            ],
            next_cursor: "cur-2",
          },
        },
      ]);

      const hook = renderWithQueryClient(() => useEvalDatasets({ limit: 50 }));
      await hook.waitFor(() => {
        expect(hook.result.current?.isSuccess).toBe(true);
      });

      expect(hook.result.current?.data?.pages[0]?.items).toHaveLength(1);
      expect(hook.result.current?.data?.pages[0]?.items[0]?.name).toBe("denies");
      expect(hook.result.current?.hasNextPage).toBe(true);
    });

    it("builds query string with name_prefix + limit + cursor", () => {
      const q = __evalsInternal.buildListDatasetsQuery(
        { namePrefix: "ops", limit: 25 },
        "cur-X",
      );
      expect(q).toContain("name_prefix=ops");
      expect(q).toContain("limit=25");
      expect(q).toContain("cursor=cur-X");
    });
  });

  describe("useEvalDataset", () => {
    it("fetches a single dataset by id", async () => {
      mockFetch([
        {
          match: "/evals/datasets/ds-7",
          method: "GET",
          body: {
            id: "ds-7",
            name: "ops",
            version: 2,
            tenant: "acme",
            entry_count: 3,
            content_hash: "sha256:b",
            created_at: "2026-04-19T00:00:00Z",
            updated_at: "2026-04-19T01:00:00Z",
          },
        },
      ]);

      const hook = renderWithQueryClient(() => useEvalDataset("ds-7"));
      await hook.waitFor(() => {
        expect(hook.result.current?.isSuccess).toBe(true);
      });
      expect(hook.result.current?.data?.id).toBe("ds-7");
      expect(hook.result.current?.data?.version).toBe(2);
    });

    it("is disabled when id is undefined", () => {
      const hook = renderWithQueryClient(() => useEvalDataset(undefined));
      expect(hook.result.current?.isFetching).toBe(false);
    });
  });

  describe("useEvalRuns", () => {
    it("builds runs query with all filters", () => {
      const q = __evalsInternal.buildListRunsQuery(
        "ds-1",
        { since: "2026-04-01", until: "2026-04-20", minScore: 80, hasRegression: true, limit: 20 },
        "cur-Y",
      );
      expect(q).toContain("dataset_id=ds-1");
      expect(q).toContain("since=2026-04-01");
      expect(q).toContain("until=2026-04-20");
      expect(q).toContain("min_score=80");
      expect(q).toContain("has_regression=true");
      expect(q).toContain("limit=20");
      expect(q).toContain("cursor=cur-Y");
    });

    it("returns a page of runs", async () => {
      mockFetch([
        {
          match: "/evals/runs",
          method: "GET",
          body: {
            items: [
              {
                run_id: "run-1",
                dataset_id: "ds-1",
                dataset_name: "denies",
                dataset_version: 1,
                started_at: "2026-04-19T00:00:00Z",
                completed_at: "2026-04-19T00:00:05Z",
                summary: { total: 10, passed: 8, failed: 1, regressions: 1, errored: 0, score_percent: 80 },
              },
            ],
            next_cursor: null,
          },
        },
      ]);

      const hook = renderWithQueryClient(() => useEvalRuns("ds-1"));
      await hook.waitFor(() => {
        expect(hook.result.current?.isSuccess).toBe(true);
      });
      const runs = hook.result.current?.data?.pages[0]?.items ?? [];
      expect(runs).toHaveLength(1);
      expect(isRegressionRun(runs[0]!)).toBe(true);
    });
  });

  describe("useEvalRun polling", () => {
    it("stops refetching once completedAt is set", async () => {
      mockFetch([
        {
          match: "/evals/runs/run-X",
          method: "GET",
          body: {
            run_id: "run-X",
            dataset_id: "ds-1",
            dataset_name: "denies",
            started_at: "2026-04-19T00:00:00Z",
            completed_at: "2026-04-19T00:00:10Z",
            summary: { total: 5, passed: 5, failed: 0, regressions: 0, errored: 0, score_percent: 100 },
          },
        },
      ]);

      const hook = renderWithQueryClient(() => useEvalRun("run-X"));
      await hook.waitFor(() => {
        expect(hook.result.current?.isSuccess).toBe(true);
      });
      const completed = hook.result.current?.data?.completedAt;
      expect(completed).toBeTruthy();
    });
  });

  describe("useCreateDatasetFromIncidents", () => {
    it("invalidates dataset list on real submit", async () => {
      mockFetch([
        {
          match: "/evals/datasets/from-incidents",
          method: "POST",
          body: {
            dataset: {
              id: "ds-new",
              name: "ops-april",
              version: 1,
              tenant: "acme",
              entry_count: 42,
              content_hash: "sha256:c",
              created_at: "2026-04-20T00:00:00Z",
              updated_at: "2026-04-20T00:00:00Z",
            },
            preview: {
              scanned_decisions: 1000,
              entry_count: 42,
              deduped_count: 8,
              warnings: [],
            },
          },
        },
      ]);

      const qc = createTestQueryClient();
      const spy = vi.spyOn(qc, "invalidateQueries");
      const hook = renderWithQueryClient(() => useCreateDatasetFromIncidents(), qc);

      await hook.waitFor(() => {
        expect(hook.result.current).toBeDefined();
      });

      const result = await hook.result.current!.mutateAsync({
        datasetName: "ops-april",
        dryRun: false,
      });

      expect(result.dataset?.id).toBe("ds-new");
      expect(result.preview.entryCount).toBe(42);
      expect(spy).toHaveBeenCalledWith({ queryKey: ["evals", "datasets"] });
    });

    it("skips invalidation for dry-run", async () => {
      mockFetch([
        {
          match: "/evals/datasets/from-incidents",
          method: "POST",
          body: {
            preview: { scanned_decisions: 500, entry_count: 20, deduped_count: 3, warnings: ["skew"] },
          },
        },
      ]);

      const qc = createTestQueryClient();
      const spy = vi.spyOn(qc, "invalidateQueries");
      const hook = renderWithQueryClient(() => useCreateDatasetFromIncidents(), qc);
      await hook.waitFor(() => expect(hook.result.current).toBeDefined());

      const result = await hook.result.current!.mutateAsync({
        datasetName: "ops-april",
        dryRun: true,
      });

      expect(result.preview.warnings).toEqual(["skew"]);
      expect(result.dataset).toBeUndefined();
      expect(spy).not.toHaveBeenCalled();
    });
  });

  describe("useRunEvalDataset", () => {
    it("invalidates dataset runs on success and seeds run cache", async () => {
      mockFetch([
        {
          match: "/evals/datasets/ds-1/runs",
          method: "POST",
          body: {
            run_id: "run-new",
            dataset_id: "ds-1",
            dataset_name: "denies",
            started_at: "2026-04-20T00:00:00Z",
            completed_at: "2026-04-20T00:00:05Z",
            summary: { total: 5, passed: 5, failed: 0, regressions: 0, errored: 0, score_percent: 100 },
          },
        },
      ]);

      const qc = createTestQueryClient();
      const spy = vi.spyOn(qc, "invalidateQueries");
      const hook = renderWithQueryClient(() => useRunEvalDataset("ds-1"), qc);
      await hook.waitFor(() => expect(hook.result.current).toBeDefined());

      const run = await hook.result.current!.mutateAsync({ useCurrentPolicy: true });
      expect(run.runId).toBe("run-new");
      expect(spy).toHaveBeenCalledWith({ queryKey: ["evals", "runs", "ds-1"] });
      expect(qc.getQueryData(["evals", "run", "run-new"])).toBeTruthy();
    });
  });
});
