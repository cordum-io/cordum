import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { get, post } from "../api/client";
import type { AgentIdentity, AgentStats } from "../api/types";

/**
 * Body for POST /api/v1/agents — mirrors `createAgentRequest` in
 * core/controlplane/gateway/handlers_agents.go. `name`, `owner`, and
 * `risk_tier` are required by the server; the rest are optional and may
 * be pre-populated from the worker's heartbeat when registering an
 * identity for an already-online worker (see GitHub issue #314).
 */
export interface CreateAgentIdentityBody {
  name: string;
  owner: string;
  risk_tier: string;
  description?: string;
  team?: string;
  allowed_topics?: string[];
  allowed_pools?: string[];
  allowed_servers?: string[];
  allowed_tools?: string[];
  allowed_resources?: string[];
  entitlements?: string[];
  preapproved_mutating_tools?: string[];
  data_classifications?: string[];
}

interface AgentIdentitiesResponse {
  items: AgentIdentity[];
  cursor?: string;
}

export function useAgentIdentities(params?: {
  cursor?: string;
  limit?: number;
  status?: string;
  risk_tier?: string;
  team?: string;
}) {
  const searchParams = new URLSearchParams();
  if (params?.cursor) searchParams.set("cursor", params.cursor);
  if (params?.limit) searchParams.set("limit", String(params.limit));
  if (params?.status) searchParams.set("status", params.status);
  if (params?.risk_tier) searchParams.set("risk_tier", params.risk_tier);
  if (params?.team) searchParams.set("team", params.team);
  const qs = searchParams.toString();
  const path = qs ? `/agents?${qs}` : "/agents";

  return useQuery<AgentIdentitiesResponse>({
    queryKey: ["agent-identities", params],
    queryFn: () => get<AgentIdentitiesResponse>(path),
    refetchInterval: 30_000,
  });
}

export function useAgentIdentity(id: string | undefined) {
  return useQuery<AgentIdentity>({
    queryKey: ["agent-identity", id],
    queryFn: () => get<AgentIdentity>(`/agents/${id}`),
    enabled: !!id,
  });
}

export function useAgentStats(id: string | undefined) {
  return useQuery<AgentStats>({
    queryKey: ["agent-stats", id],
    queryFn: () => get<AgentStats>(`/agents/${id}/stats`),
    enabled: !!id,
    refetchInterval: 60_000,
  });
}

/**
 * Create an Agent Identity record. Used by the AgentIdentityPanel's
 * empty-state "Create identity" affordance to register a heartbeating
 * worker so its audit rows surface `agent_label` instead of falling back
 * to the raw `agent_id` (GitHub issue #314).
 *
 * On success, invalidates the per-id `agent-identity` query so the panel
 * re-fetches and renders the populated identity; also invalidates the
 * list so the /agents catalog reflects the new record.
 */
export function useCreateAgentIdentity() {
  const qc = useQueryClient();
  return useMutation<AgentIdentity, Error, CreateAgentIdentityBody>({
    mutationFn: (body) => post<AgentIdentity>("/agents", body),
    onSuccess: (created) => {
      qc.invalidateQueries({ queryKey: ["agent-identities"] });
      qc.invalidateQueries({ queryKey: ["agent-identity", created.id] });
    },
  });
}
