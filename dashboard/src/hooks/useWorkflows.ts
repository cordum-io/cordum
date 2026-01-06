import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { WorkflowRun } from "../types/api";

export function useWorkflows() {
  return useQuery({
    queryKey: ["workflows"],
    queryFn: api.listWorkflows,
  });
}

export function useRunsByWorkflow(workflowId: string | undefined) {
  return useQuery({
    queryKey: ["runs", workflowId],
    queryFn: () => api.listRunsByWorkflow(workflowId as string),
    enabled: Boolean(workflowId),
  });
}

export function useAllRuns(params?: {
  limit?: number;
  cursor?: number;
  status?: string;
  workflow_id?: string;
  org_id?: string;
  team_id?: string;
  updated_after?: number;
  updated_before?: number;
}) {
  const runsQuery = useQuery({
    queryKey: ["runs", params],
    queryFn: () => api.listWorkflowRuns(params),
  });

  const runs = (runsQuery.data?.items || []) as WorkflowRun[];

  return {
    runsQuery,
    runs,
    isLoading: runsQuery.isLoading,
    isError: runsQuery.isError,
    nextCursor: runsQuery.data?.next_cursor ?? null,
  };
}
