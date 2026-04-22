// useMcpCatalog — unified hook that exposes the full MCP surface (tools
// + resources + prompts + status) for dashboard pages that want to
// render all four concerns side-by-side.
//
// `useMcpPrompts` does a live fetch of /api/v1/mcp/prompts (admin-gated,
// served by handlers_mcp_prompts.go). The gateway returns the canonical
// {name, description, arguments} from the running PromptRegistry so
// re-registering a prompt at runtime flows into the dashboard without
// a frontend redeploy. UI-only decoration (modelClass, safetyDisclaimer,
// docsHref) is merged in client-side from PROMPT_UI_METADATA below —
// those fields are presentation concerns that don't belong on the MCP
// wire contract.
//
// New prompts land in two places: a server-side entry in
// core/mcp/prompts.go RegisterAllPrompts + a metadata entry below. If
// a prompt exists server-side but not in the metadata table, it still
// renders — just without the model-class chip or docs link.

import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import type { McpPrompt } from "../api/types";
import { useMcpStatus, useMcpTools, useMcpResources } from "./useSettings";

interface PromptUIMetadata {
  modelClass: McpPrompt["modelClass"];
  safetyDisclaimer: boolean;
  docsHref: string;
}

// Client-side decoration keyed by prompt name. Kept separate from the
// server catalogue so UI-only changes don't require a backend deploy.
const PROMPT_UI_METADATA: Record<string, PromptUIMetadata> = {
  draft_safety_rule: {
    modelClass: "small",
    safetyDisclaimer: true,
    docsHref: "/docs/mcp/prompts#draft_safety_rule",
  },
  explain_denial: {
    modelClass: "small",
    safetyDisclaimer: false,
    docsHref: "/docs/mcp/prompts#explain_denial",
  },
  summarize_approvals: {
    modelClass: "reasoning",
    safetyDisclaimer: false,
    docsHref: "/docs/mcp/prompts#summarize_approvals",
  },
  policy_migration_helper: {
    modelClass: "reasoning",
    safetyDisclaimer: true,
    docsHref: "/docs/mcp/prompts#policy_migration_helper",
  },
};

interface McpPromptsResponse {
  prompts: Array<{
    name: string;
    description: string;
    arguments?: Array<{ name: string; description?: string; required?: boolean }>;
  }>;
}

export const mcpCatalogQueryKeys = {
  prompts: () => ["mcp", "prompts"] as const,
};

export function useMcpPrompts() {
  return useQuery<McpPrompt[]>({
    queryKey: mcpCatalogQueryKeys.prompts(),
    queryFn: async () => {
      const resp = await get<McpPromptsResponse>("/mcp/prompts");
      const raw = resp?.prompts ?? [];
      return raw.map<McpPrompt>((p) => {
        const meta = PROMPT_UI_METADATA[p.name];
        return {
          name: p.name,
          description: p.description ?? "",
          arguments: (p.arguments ?? []).map((a) => ({
            name: a.name,
            description: a.description ?? "",
            required: !!a.required,
          })),
          modelClass: meta?.modelClass,
          safetyDisclaimer: meta?.safetyDisclaimer ?? false,
          docsHref: meta?.docsHref,
        };
      });
    },
    // Prompt registries change rarely. 5-minute cache keeps the card
    // grid snappy without hammering the gateway on every tab switch.
    staleTime: 5 * 60_000,
    refetchOnWindowFocus: false,
  });
}

export interface McpCatalog {
  status: ReturnType<typeof useMcpStatus>;
  tools: ReturnType<typeof useMcpTools>;
  resources: ReturnType<typeof useMcpResources>;
  prompts: ReturnType<typeof useMcpPrompts>;
}

export function useMcpCatalog(): McpCatalog {
  return {
    status: useMcpStatus(),
    tools: useMcpTools(),
    resources: useMcpResources(),
    prompts: useMcpPrompts(),
  };
}
