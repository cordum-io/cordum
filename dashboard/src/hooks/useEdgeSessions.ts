import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { get, post, ApiError } from "../api/client";
import { API_PATHS } from "../lib/constants";
import { queryKeys } from "../lib/queryKeys";
import type {
  AgentActionEventPage,
  AgentExecution,
  AgentExecutionPage,
  EdgeApproval,
  EdgeApprovalListParams,
  EdgeApprovalPage,
  EdgeError,
  EdgeEventListParams,
  EdgeExecutionListParams,
  EdgeExportRequest,
  EdgeSession,
  EdgeSessionExportBundle,
  EdgeSessionListParams,
  EdgeSessionPage,
} from "../api/types";
import {
  mapAgentActionEventPage,
  mapAgentExecution,
  mapAgentExecutionPage,
  mapEdgeApproval,
  mapEdgeApprovalPage,
  mapEdgeErrorEnvelope,
  mapEdgeSession,
  mapEdgeSessionExportBundle,
  mapEdgeSessionPage,
  type BackendEdgeAgentActionEvent,
  type BackendEdgeAgentExecution,
  type BackendEdgeApproval,
  type BackendEdgePage,
  type BackendEdgeSession,
  type BackendEdgeSessionExportBundle,
} from "../api/transform";

type QueryParamValue = string | number | null | undefined;

export interface ResolveEdgeApprovalInput {
  approvalRef: string;
  reason?: string;
}

export interface WaitEdgeApprovalInput {
  approvalRef: string;
  timeoutMs?: number;
}

export interface ExportEdgeSessionInput {
  sessionId: string;
  request?: EdgeExportRequest;
}

interface BackendEdgeExportRequest {
  max_events?: number;
}

function appendQuery(path: string, params: Record<string, QueryParamValue>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") continue;
    search.set(key, String(value));
  }
  const query = search.toString();
  return query ? `${path}?${query}` : path;
}

function encodePathSegment(value: string): string {
  return encodeURIComponent(value);
}

function edgeApprovalBody(reason?: string): { reason: string } | undefined {
  const trimmed = reason?.trim();
  return trimmed ? { reason: trimmed } : undefined;
}

function edgeWaitBody(timeoutMs?: number): { timeout_ms: number } | undefined {
  return typeof timeoutMs === "number" && Number.isFinite(timeoutMs) && timeoutMs >= 0
    ? { timeout_ms: timeoutMs }
    : undefined;
}

function edgeExportBody(request?: EdgeExportRequest): BackendEdgeExportRequest {
  if (typeof request?.maxEvents === "number" && Number.isFinite(request.maxEvents)) {
    return { max_events: request.maxEvents };
  }
  return {};
}

function filterSessionPage(page: EdgeSessionPage, params?: EdgeSessionListParams): EdgeSessionPage {
  const status = params?.status?.trim().toLowerCase();
  const agentProduct = params?.agentProduct?.trim().toLowerCase();
  if (!status && !agentProduct) return page;
  return {
    ...page,
    items: page.items.filter((session) => {
      if (status && session.status.toLowerCase() !== status) return false;
      if (agentProduct && (session.agentProduct ?? "").toLowerCase() !== agentProduct) return false;
      return true;
    }),
  };
}

function invalidateEdgeSessionQueries(
  queryClient: ReturnType<typeof useQueryClient>,
  sessionId?: string,
  executionId?: string,
): void {
  queryClient.invalidateQueries({ queryKey: queryKeys.edge.sessions.lists() });
  queryClient.invalidateQueries({ queryKey: queryKeys.edge.executions.lists() });
  queryClient.invalidateQueries({ queryKey: queryKeys.edge.approvals.lists() });
  if (sessionId) {
    queryClient.invalidateQueries({ queryKey: queryKeys.edge.sessions.detail(sessionId) });
    queryClient.invalidateQueries({ queryKey: queryKeys.edge.sessions.eventLists(sessionId) });
    queryClient.invalidateQueries({ queryKey: queryKeys.edge.sessions.export(sessionId) });
  }
  if (executionId) {
    queryClient.invalidateQueries({ queryKey: queryKeys.edge.executions.detail(executionId) });
    queryClient.invalidateQueries({ queryKey: queryKeys.edge.executions.eventLists(executionId) });
  }
}

function invalidateEdgeApprovalQueries(
  queryClient: ReturnType<typeof useQueryClient>,
  approval?: EdgeApproval,
  approvalRef?: string,
): void {
  queryClient.invalidateQueries({ queryKey: queryKeys.edge.approvals.lists() });
  const ref = approval?.approvalRef ?? approvalRef;
  if (ref) {
    queryClient.invalidateQueries({ queryKey: queryKeys.edge.approvals.detail(ref) });
  }
  invalidateEdgeSessionQueries(queryClient, approval?.sessionId, approval?.executionId);
}

export function edgeErrorFromApiError(error: unknown): EdgeError | undefined {
  if (!(error instanceof ApiError)) return undefined;
  return mapEdgeErrorEnvelope(error.body);
}

export async function fetchEdgeSessions(params: EdgeSessionListParams = {}): Promise<EdgeSessionPage> {
  const path = appendQuery(API_PATHS.edge.sessions, {
    principal_id: params.principalId,
    cursor: params.cursor,
    limit: params.limit,
  });
  const page = mapEdgeSessionPage(await get<BackendEdgePage<BackendEdgeSession>>(path));
  return filterSessionPage(page, params);
}

export async function fetchEdgeSession(sessionId: string): Promise<EdgeSession> {
  return mapEdgeSession(
    await get<BackendEdgeSession>(`${API_PATHS.edge.sessions}/${encodePathSegment(sessionId)}`),
  );
}

export async function fetchEdgeSessionEvents(
  sessionId: string,
  params: EdgeEventListParams = {},
): Promise<AgentActionEventPage> {
  const path = appendQuery(
    `${API_PATHS.edge.sessions}/${encodePathSegment(sessionId)}/events`,
    {
      cursor: params.cursor,
      limit: params.limit,
      kind: params.kind,
      decision: params.decision,
      since: params.since,
      until: params.until,
    },
  );
  return mapAgentActionEventPage(await get<BackendEdgePage<BackendEdgeAgentActionEvent>>(path));
}

export async function fetchEdgeExecutions(params: EdgeExecutionListParams = {}): Promise<AgentExecutionPage> {
  const path = appendQuery(API_PATHS.edge.executions, {
    session_id: params.sessionId,
    job_id: params.jobId,
    trace_id: params.traceId,
    workflow_run_id: params.workflowRunId,
    cursor: params.cursor,
    limit: params.limit,
  });
  return mapAgentExecutionPage(await get<BackendEdgePage<BackendEdgeAgentExecution>>(path));
}

export async function fetchEdgeExecution(executionId: string): Promise<AgentExecution> {
  return mapAgentExecution(
    await get<BackendEdgeAgentExecution>(`${API_PATHS.edge.executions}/${encodePathSegment(executionId)}`),
  );
}

export async function fetchEdgeExecutionEvents(
  executionId: string,
  params: EdgeEventListParams = {},
): Promise<AgentActionEventPage> {
  const path = appendQuery(
    `${API_PATHS.edge.executions}/${encodePathSegment(executionId)}/events`,
    {
      cursor: params.cursor,
      limit: params.limit,
      kind: params.kind,
      decision: params.decision,
      since: params.since,
      until: params.until,
    },
  );
  return mapAgentActionEventPage(await get<BackendEdgePage<BackendEdgeAgentActionEvent>>(path));
}

export async function fetchEdgeApprovals(params: EdgeApprovalListParams = {}): Promise<EdgeApprovalPage> {
  const path = appendQuery(API_PATHS.edge.approvals, {
    status: params.status,
    session_id: params.sessionId,
    execution_id: params.executionId,
    action_hash: params.actionHash,
    cursor: params.cursor,
    limit: params.limit,
  });
  return mapEdgeApprovalPage(await get<BackendEdgePage<BackendEdgeApproval>>(path));
}

export async function fetchEdgeApproval(approvalRef: string): Promise<EdgeApproval> {
  return mapEdgeApproval(
    await get<BackendEdgeApproval>(`${API_PATHS.edge.approvals}/${encodePathSegment(approvalRef)}`),
  );
}

export async function approveEdgeApproval(input: ResolveEdgeApprovalInput): Promise<EdgeApproval> {
  return mapEdgeApproval(
    await post<BackendEdgeApproval>(
      `${API_PATHS.edge.approvals}/${encodePathSegment(input.approvalRef)}/approve`,
      edgeApprovalBody(input.reason),
    ),
  );
}

export async function rejectEdgeApproval(input: ResolveEdgeApprovalInput): Promise<EdgeApproval> {
  return mapEdgeApproval(
    await post<BackendEdgeApproval>(
      `${API_PATHS.edge.approvals}/${encodePathSegment(input.approvalRef)}/reject`,
      edgeApprovalBody(input.reason),
    ),
  );
}

export async function waitForEdgeApproval(input: WaitEdgeApprovalInput): Promise<EdgeApproval> {
  return mapEdgeApproval(
    await post<BackendEdgeApproval>(
      `${API_PATHS.edge.approvals}/${encodePathSegment(input.approvalRef)}/wait`,
      edgeWaitBody(input.timeoutMs),
    ),
  );
}

export async function exportEdgeSession(input: ExportEdgeSessionInput): Promise<EdgeSessionExportBundle> {
  return mapEdgeSessionExportBundle(
    await post<BackendEdgeSessionExportBundle>(
      `${API_PATHS.edge.sessions}/${encodePathSegment(input.sessionId)}/export`,
      edgeExportBody(input.request),
    ),
  );
}

export function useEdgeSessions(params: EdgeSessionListParams = {}) {
  return useQuery<EdgeSessionPage, ApiError>({
    queryKey: queryKeys.edge.sessions.list(params),
    queryFn: () => fetchEdgeSessions(params),
    staleTime: 5_000,
  });
}

export function useEdgeSession(sessionId?: string | null) {
  return useQuery<EdgeSession, ApiError>({
    queryKey: queryKeys.edge.sessions.detail(sessionId),
    queryFn: () => fetchEdgeSession(sessionId ?? ""),
    enabled: Boolean(sessionId),
    staleTime: 5_000,
  });
}

export function useEdgeSessionEvents(sessionId?: string | null, params: EdgeEventListParams = {}) {
  return useQuery<AgentActionEventPage, ApiError>({
    queryKey: queryKeys.edge.sessions.events(sessionId, params),
    queryFn: () => fetchEdgeSessionEvents(sessionId ?? "", params),
    enabled: Boolean(sessionId),
    staleTime: 5_000,
  });
}

export function useEdgeExecutions(params: EdgeExecutionListParams = {}) {
  const hasIndex = Boolean(params.sessionId || params.jobId || params.traceId || params.workflowRunId);
  return useQuery<AgentExecutionPage, ApiError>({
    queryKey: queryKeys.edge.executions.list(params),
    queryFn: () => fetchEdgeExecutions(params),
    enabled: hasIndex,
    staleTime: 5_000,
  });
}

export function useEdgeExecution(executionId?: string | null) {
  return useQuery<AgentExecution, ApiError>({
    queryKey: queryKeys.edge.executions.detail(executionId),
    queryFn: () => fetchEdgeExecution(executionId ?? ""),
    enabled: Boolean(executionId),
    staleTime: 5_000,
  });
}

export function useEdgeExecutionEvents(executionId?: string | null, params: EdgeEventListParams = {}) {
  return useQuery<AgentActionEventPage, ApiError>({
    queryKey: queryKeys.edge.executions.events(executionId, params),
    queryFn: () => fetchEdgeExecutionEvents(executionId ?? "", params),
    enabled: Boolean(executionId),
    staleTime: 5_000,
  });
}

export function useEdgeApprovals(params: EdgeApprovalListParams = {}) {
  return useQuery<EdgeApprovalPage, ApiError>({
    queryKey: queryKeys.edge.approvals.list(params),
    queryFn: () => fetchEdgeApprovals(params),
    staleTime: 5_000,
  });
}

export function useEdgeApproval(approvalRef?: string | null) {
  return useQuery<EdgeApproval, ApiError>({
    queryKey: queryKeys.edge.approvals.detail(approvalRef),
    queryFn: () => fetchEdgeApproval(approvalRef ?? ""),
    enabled: Boolean(approvalRef),
    staleTime: 5_000,
  });
}

export function useApproveEdgeApproval() {
  const queryClient = useQueryClient();
  return useMutation<EdgeApproval, ApiError, ResolveEdgeApprovalInput>({
    mutationKey: ["edge", "approvals", "approve"],
    mutationFn: approveEdgeApproval,
    onSuccess: (approval, variables) => {
      invalidateEdgeApprovalQueries(queryClient, approval, variables.approvalRef);
    },
  });
}

export function useRejectEdgeApproval() {
  const queryClient = useQueryClient();
  return useMutation<EdgeApproval, ApiError, ResolveEdgeApprovalInput>({
    mutationKey: ["edge", "approvals", "reject"],
    mutationFn: rejectEdgeApproval,
    onSuccess: (approval, variables) => {
      invalidateEdgeApprovalQueries(queryClient, approval, variables.approvalRef);
    },
  });
}

export function useWaitEdgeApproval() {
  const queryClient = useQueryClient();
  return useMutation<EdgeApproval, ApiError, WaitEdgeApprovalInput>({
    mutationKey: ["edge", "approvals", "wait"],
    mutationFn: waitForEdgeApproval,
    onSuccess: (approval, variables) => {
      invalidateEdgeApprovalQueries(queryClient, approval, variables.approvalRef);
    },
  });
}

export function useExportEdgeSession() {
  const queryClient = useQueryClient();
  return useMutation<EdgeSessionExportBundle, ApiError, ExportEdgeSessionInput>({
    mutationKey: ["edge", "sessions", "export"],
    mutationFn: exportEdgeSession,
    onSuccess: (bundle, variables) => {
      invalidateEdgeSessionQueries(queryClient, variables.sessionId);
      for (const execution of bundle.executions ?? []) {
        invalidateEdgeSessionQueries(queryClient, variables.sessionId, execution.executionId);
      }
    },
  });
}
