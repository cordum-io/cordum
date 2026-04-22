import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";

export interface MCPTool {
  name: string;
  description?: string;
  tags?: string[];
  riskTier?: string;
  dataClassifications?: string[];
  requiresApproval?: boolean;
}

export interface AgentToolVisibility {
  tools: MCPTool[];
  agent_id: string;
  filtered: boolean;
  note?: string;
}

export interface AgentDenyEvent {
  timestamp: string;
  agent_id: string;
  tool_name: string;
  sub_reason: string;
  severity: string;
}

export interface AgentDenyEventsResponse {
  agent_id: string;
  events: AgentDenyEvent[];
  limit: number;
}

// useAgentToolVisibility fetches the tool catalogue filtered for a
// specific agent identity. Returns the set of tools the identity is
// currently allowed to see given its allowed_tools, risk_tier, and
// data_classifications.
export function useAgentToolVisibility(agentId: string | undefined) {
  return useQuery<AgentToolVisibility>({
    queryKey: ["agent-tool-visibility", agentId],
    queryFn: () => get<AgentToolVisibility>(`/agents/${agentId}/tools`),
    enabled: !!agentId,
    refetchInterval: 30_000,
  });
}

// useAgentDeniedEvents fetches the 50 most recent mcp_tool_denied
// SIEM events for the identity. Surfaces which tools the identity has
// tried to use but couldn't, so operators can tune allowed_tools or
// risk_tier with real usage data.
export function useAgentDeniedEvents(agentId: string | undefined) {
  return useQuery<AgentDenyEventsResponse>({
    queryKey: ["agent-denied-events", agentId],
    queryFn: () => get<AgentDenyEventsResponse>(`/agents/${agentId}/denied-events`),
    enabled: !!agentId,
    refetchInterval: 15_000,
  });
}
