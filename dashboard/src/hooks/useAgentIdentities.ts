import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import type { AgentIdentity, AgentStats } from "../api/types";

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
