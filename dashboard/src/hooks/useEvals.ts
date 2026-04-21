import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { del, get, post, put } from "../api/client";
import {
  mapEvalDataset,
  mapEvalRun,
  type BackendEvalDataset,
  type BackendEvalRun,
} from "../api/transform";
import type {
  EvalDataset,
  EvalRun,
  ExtractIncidentsPreview,
  ExtractIncidentsRequest,
} from "../api/types";

export interface UseEvalDatasetsArgs {
  namePrefix?: string;
  limit?: number;
}

interface BackendListDatasetsResponse {
  items?: BackendEvalDataset[];
  next_cursor?: string | null;
  nextCursor?: string | null;
}

interface EvalDatasetsPage {
  items: EvalDataset[];
  nextCursor?: string;
}

function buildListDatasetsQuery(args: UseEvalDatasetsArgs, cursor?: string): string {
  const params = new URLSearchParams();
  params.set("limit", String(Math.max(1, args.limit ?? 50)));
  if (args.namePrefix) params.set("name_prefix", args.namePrefix);
  if (cursor) params.set("cursor", cursor);
  return params.toString();
}

export function useEvalDatasets(args: UseEvalDatasetsArgs = {}) {
  return useInfiniteQuery<EvalDatasetsPage, Error>({
    queryKey: ["evals", "datasets", { namePrefix: args.namePrefix, limit: args.limit }],
    queryFn: async ({ pageParam }) => {
      const cursor = typeof pageParam === "string" ? pageParam : undefined;
      const query = buildListDatasetsQuery(args, cursor);
      const res = await get<BackendListDatasetsResponse>(`/evals/datasets?${query}`);
      return {
        items: (res.items ?? []).map(mapEvalDataset),
        nextCursor: res.next_cursor ?? res.nextCursor ?? undefined,
      };
    },
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
    initialPageParam: undefined as string | undefined,
    staleTime: 60_000,
  });
}

export function useEvalDataset(id: string | undefined) {
  return useQuery<EvalDataset, Error>({
    queryKey: ["evals", "dataset", id],
    enabled: !!id,
    queryFn: async () => {
      const res = await get<BackendEvalDataset>(`/evals/datasets/${encodeURIComponent(id!)}`);
      return mapEvalDataset(res);
    },
    staleTime: 60_000,
  });
}

interface BackendDatasetVersionsResponse {
  items?: BackendEvalDataset[];
}

export function useEvalDatasetVersions(name: string | undefined) {
  return useQuery<EvalDataset[], Error>({
    queryKey: ["evals", "dataset-versions", name],
    enabled: !!name,
    queryFn: async () => {
      const res = await get<BackendDatasetVersionsResponse>(
        `/evals/datasets/by-name/${encodeURIComponent(name!)}`,
      );
      return (res.items ?? []).map(mapEvalDataset);
    },
    staleTime: 60_000,
  });
}

export interface UseEvalRunsArgs {
  since?: string;
  until?: string;
  minScore?: number;
  hasRegression?: boolean;
  limit?: number;
}

interface BackendListRunsResponse {
  items?: BackendEvalRun[];
  next_cursor?: string | null;
  nextCursor?: string | null;
}

interface EvalRunsPage {
  items: EvalRun[];
  nextCursor?: string;
}

function buildListRunsQuery(datasetId: string, args: UseEvalRunsArgs, cursor?: string): string {
  const params = new URLSearchParams();
  params.set("dataset_id", datasetId);
  params.set("limit", String(Math.max(1, args.limit ?? 50)));
  if (args.since) params.set("since", args.since);
  if (args.until) params.set("until", args.until);
  if (typeof args.minScore === "number") params.set("min_score", String(args.minScore));
  if (args.hasRegression) params.set("has_regression", "true");
  if (cursor) params.set("cursor", cursor);
  return params.toString();
}

export function useEvalRuns(datasetId: string | undefined, args: UseEvalRunsArgs = {}) {
  // Destructure args into scalar queryKey entries so React Query dedupes
  // across re-renders. Passing the `args` object by reference makes every
  // render a fresh key (new literal every render) → refetch churn that
  // masquerades as a live stream. Scalars only; arrays/objects here would
  // hit the same trap.
  return useInfiniteQuery<EvalRunsPage, Error>({
    queryKey: [
      "evals",
      "runs",
      datasetId,
      args.since ?? null,
      args.until ?? null,
      args.minScore ?? null,
      args.hasRegression ?? null,
      args.limit ?? null,
    ],
    enabled: !!datasetId,
    queryFn: async ({ pageParam }) => {
      const cursor = typeof pageParam === "string" ? pageParam : undefined;
      const query = buildListRunsQuery(datasetId!, args, cursor);
      const res = await get<BackendListRunsResponse>(`/evals/runs?${query}`);
      return {
        items: (res.items ?? []).map(mapEvalRun),
        nextCursor: res.next_cursor ?? res.nextCursor ?? undefined,
      };
    },
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
    initialPageParam: undefined as string | undefined,
    staleTime: 30_000,
  });
}

const RUN_POLL_INTERVAL_MS = 3_000;

export function useEvalRun(runId: string | undefined, options?: { pollWhilePending?: boolean }) {
  const pollWhilePending = options?.pollWhilePending ?? true;
  return useQuery<EvalRun, Error>({
    queryKey: ["evals", "run", runId],
    enabled: !!runId,
    queryFn: async () => {
      const res = await get<BackendEvalRun>(`/evals/runs/${encodeURIComponent(runId!)}`);
      return mapEvalRun(res);
    },
    refetchInterval: (query) => {
      if (!pollWhilePending) return false;
      const data = query.state.data as EvalRun | undefined;
      if (!data) return RUN_POLL_INTERVAL_MS;
      return data.completedAt ? false : RUN_POLL_INTERVAL_MS;
    },
    staleTime: 0,
  });
}

interface BackendExtractResponse {
  dataset?: BackendEvalDataset;
  preview?: {
    scanned_decisions?: number;
    entry_count?: number;
    deduped_count?: number;
    warnings?: string[];
  };
  scanned_decisions?: number;
  entry_count?: number;
  deduped_count?: number;
  warnings?: string[];
  dataset_id?: string;
}

export interface CreateDatasetFromIncidentsResult {
  dataset?: EvalDataset;
  preview: ExtractIncidentsPreview;
}

function toExtractRequestBody(req: ExtractIncidentsRequest): Record<string, unknown> {
  const body: Record<string, unknown> = {
    dataset_name: req.datasetName,
  };
  if (req.datasetDescription !== undefined) body.dataset_description = req.datasetDescription;
  if (req.since !== undefined) body.since = req.since;
  if (req.until !== undefined) body.until = req.until;
  if (req.topicPattern !== undefined) body.topic_pattern = req.topicPattern;
  if (req.ruleId !== undefined) body.rule_id = req.ruleId;
  if (req.verdicts !== undefined) body.verdicts = req.verdicts;
  if (req.agentId !== undefined) body.agent_id = req.agentId;
  if (req.maxEntries !== undefined) body.max_entries = req.maxEntries;
  if (req.dryRun !== undefined) body.dry_run = req.dryRun;
  return body;
}

export function useCreateDatasetFromIncidents() {
  const qc = useQueryClient();
  return useMutation<CreateDatasetFromIncidentsResult, Error, ExtractIncidentsRequest>({
    mutationFn: async (req) => {
      const res = await post<BackendExtractResponse>(
        "/evals/datasets/from-incidents",
        toExtractRequestBody(req),
      );
      const preview = res.preview ?? {
        scanned_decisions: res.scanned_decisions,
        entry_count: res.entry_count,
        deduped_count: res.deduped_count,
        warnings: res.warnings,
      };
      const dataset = res.dataset ? mapEvalDataset(res.dataset) : undefined;
      return {
        dataset,
        preview: {
          scannedDecisions: preview.scanned_decisions ?? 0,
          entryCount: preview.entry_count ?? 0,
          dedupedCount: preview.deduped_count ?? 0,
          warnings: preview.warnings ?? [],
          datasetId: dataset?.id ?? res.dataset_id,
        },
      };
    },
    onSuccess: (result, variables) => {
      if (variables.dryRun) return;
      qc.invalidateQueries({ queryKey: ["evals", "datasets"] });
      if (result.dataset) {
        qc.invalidateQueries({ queryKey: ["evals", "dataset", result.dataset.id] });
      }
    },
  });
}

export interface RunEvalDatasetInput {
  candidateBundleId?: string;
  candidateContent?: string;
  useCurrentPolicy?: boolean;
}

export function useRunEvalDataset(datasetId: string | undefined) {
  const qc = useQueryClient();
  return useMutation<EvalRun, Error, RunEvalDatasetInput | void>({
    mutationFn: async (input) => {
      if (!datasetId) throw new Error("datasetId is required");
      const body: Record<string, unknown> = {};
      if (input?.candidateBundleId) body.candidate_bundle_id = input.candidateBundleId;
      if (input?.candidateContent) body.candidate_content = input.candidateContent;
      if (typeof input?.useCurrentPolicy === "boolean") {
        body.use_current_policy = input.useCurrentPolicy;
      }
      const res = await post<BackendEvalRun>(
        `/evals/datasets/${encodeURIComponent(datasetId)}/runs`,
        body,
      );
      return mapEvalRun(res);
    },
    onSuccess: (run) => {
      qc.invalidateQueries({ queryKey: ["evals", "datasets"] });
      if (datasetId) {
        qc.invalidateQueries({ queryKey: ["evals", "runs", datasetId] });
      }
      if (run.runId) {
        qc.setQueryData(["evals", "run", run.runId], run);
      }
    },
  });
}

export function useDeleteEvalDataset() {
  const qc = useQueryClient();
  return useMutation<void, Error, { datasetId: string; force?: boolean }>({
    mutationFn: async ({ datasetId, force }) => {
      const path = force
        ? `/evals/datasets/${encodeURIComponent(datasetId)}?force=true`
        : `/evals/datasets/${encodeURIComponent(datasetId)}`;
      await del(path);
    },
    onSuccess: (_void, vars) => {
      qc.invalidateQueries({ queryKey: ["evals", "datasets"] });
      qc.removeQueries({ queryKey: ["evals", "dataset", vars.datasetId] });
    },
  });
}

export interface CreateDatasetVersionInput {
  baseDatasetId: string;
  version?: number;
  description?: string;
}

export function useCreateDatasetVersion() {
  const qc = useQueryClient();
  return useMutation<EvalDataset, Error, CreateDatasetVersionInput>({
    mutationFn: async ({ baseDatasetId, version, description }) => {
      const body: Record<string, unknown> = {};
      if (version !== undefined) body.version = version;
      if (description !== undefined) body.description = description;
      const res = await put<BackendEvalDataset>(
        `/evals/datasets/${encodeURIComponent(baseDatasetId)}`,
        body,
      );
      return mapEvalDataset(res);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["evals", "datasets"] });
    },
  });
}

/** @internal for tests */
export const __evalsInternal = {
  buildListDatasetsQuery,
  buildListRunsQuery,
  toExtractRequestBody,
  RUN_POLL_INTERVAL_MS,
};
