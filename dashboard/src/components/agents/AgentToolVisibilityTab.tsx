/*
 * Tool visibility tab for the Agent Identity detail page.
 * Shows the filtered tool catalogue the identity can currently see,
 * plus the last 50 mcp_tool_denied events for operator tuning.
 */
import { motion } from "framer-motion";
import { AlertTriangle, Shield, Tag, Wrench, XOctagon } from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import {
  useAgentToolVisibility,
  useAgentDeniedEvents,
  type MCPTool,
  type AgentDenyEvent,
} from "@/hooks/useAgentTools";

const riskTierColor: Record<string, string> = {
  low: "text-emerald-400 bg-emerald-500/10 border-emerald-500/30",
  medium: "text-amber-400 bg-amber-500/10 border-amber-500/30",
  high: "text-orange-400 bg-orange-500/10 border-orange-500/30",
  critical: "text-red-400 bg-red-500/10 border-red-500/30",
};

const subReasonLabel: Record<string, string> = {
  tool_not_in_allowed_list: "Not in allow-list",
  risk_tier_too_low: "Risk tier too low",
  missing_data_classification: "Missing data classification",
  no_identity: "No identity",
};

export function AgentToolVisibilityTab({ agentId }: { agentId: string }) {
  const tools = useAgentToolVisibility(agentId);
  const events = useAgentDeniedEvents(agentId);

  return (
    <div className="space-y-6">
      <VisibleToolsCard
        isLoading={tools.isLoading}
        isError={tools.isError}
        errorMessage={tools.error instanceof Error ? tools.error.message : undefined}
        tools={tools.data?.tools ?? []}
        note={tools.data?.note}
      />
      <RecentDenialsCard
        isLoading={events.isLoading}
        isError={events.isError}
        errorMessage={events.error instanceof Error ? events.error.message : undefined}
        events={events.data?.events ?? []}
      />
    </div>
  );
}

function VisibleToolsCard({
  isLoading,
  isError,
  errorMessage,
  tools,
  note,
}: {
  isLoading: boolean;
  isError: boolean;
  errorMessage?: string;
  tools: MCPTool[];
  note?: string;
}) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      className="instrument-card"
      data-testid="agent-tool-visibility"
    >
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2">
          <Wrench className="w-4 h-4 text-cordum" />
          <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
            Tools this identity can see
          </span>
        </div>
        {!isLoading && !isError && (
          <span className="text-xs font-mono text-muted-foreground">
            {tools.length} {tools.length === 1 ? "tool" : "tools"}
          </span>
        )}
      </div>

      {isError && (
        <ErrorBanner message={errorMessage ?? "Failed to load tool visibility"} />
      )}

      {isLoading && (
        <div className="space-y-2">
          <SkeletonCard />
          <SkeletonCard />
        </div>
      )}

      {note && !isError && !isLoading && (
        <div className="mb-3 text-xs text-amber-400 border border-amber-500/30 bg-amber-500/10 rounded px-3 py-2">
          {note}
        </div>
      )}

      {!isLoading && !isError && tools.length === 0 && (
        <div
          className="flex flex-col items-center justify-center py-10 text-center border border-dashed border-border rounded-xl"
          data-testid="agent-tool-visibility-empty"
        >
          <Shield className="w-8 h-8 text-muted-foreground/40 mb-2" />
          <p className="text-sm text-muted-foreground">
            No tools visible to this identity.
          </p>
          <p className="text-xs text-muted-foreground/70 mt-1">
            Check allowed_tools, risk_tier, and data_classifications.
          </p>
        </div>
      )}

      {!isLoading && !isError && tools.length > 0 && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
          {tools.map((tool) => (
            <ToolRow key={tool.name} tool={tool} />
          ))}
        </div>
      )}
    </motion.div>
  );
}

function ToolRow({ tool }: { tool: MCPTool }) {
  const tier = tool.riskTier ? riskTierColor[tool.riskTier] : undefined;
  return (
    <div className="flex items-start gap-3 p-3 rounded-xl border border-border bg-surface-1">
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="font-mono text-sm text-foreground truncate">{tool.name}</span>
          {tool.riskTier && (
            <span
              className={cn(
                "inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-mono uppercase tracking-wider border",
                tier,
              )}
            >
              <Shield className="w-2.5 h-2.5" />
              {tool.riskTier}
            </span>
          )}
          {tool.requiresApproval && (
            <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-mono uppercase tracking-wider border border-amber-500/30 bg-amber-500/10 text-amber-400">
              approval
            </span>
          )}
        </div>
        {tool.description && (
          <p className="text-xs text-muted-foreground mt-1 line-clamp-2">{tool.description}</p>
        )}
        {(tool.dataClassifications?.length ?? 0) > 0 && (
          <div className="flex flex-wrap gap-1 mt-2">
            {tool.dataClassifications!.map((c) => (
              <span
                key={c}
                className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-surface-2 border border-border text-[10px] font-mono text-muted-foreground"
              >
                <Tag className="w-2.5 h-2.5" />
                {c}
              </span>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function RecentDenialsCard({
  isLoading,
  isError,
  errorMessage,
  events,
}: {
  isLoading: boolean;
  isError: boolean;
  errorMessage?: string;
  events: AgentDenyEvent[];
}) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ delay: 0.05 }}
      className="instrument-card"
      data-testid="agent-tool-denials"
    >
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2">
          <XOctagon className="w-4 h-4 text-destructive" />
          <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
            Recent denials
          </span>
        </div>
        {!isLoading && !isError && (
          <span className="text-xs font-mono text-muted-foreground">
            last {events.length}
          </span>
        )}
      </div>

      {isError && (
        <ErrorBanner message={errorMessage ?? "Failed to load denial history"} />
      )}

      {isLoading && (
        <div className="space-y-2">
          <SkeletonCard />
        </div>
      )}

      {!isLoading && !isError && events.length === 0 && (
        <div
          className="flex flex-col items-center justify-center py-8 text-center border border-dashed border-border rounded-xl"
          data-testid="agent-tool-denials-empty"
        >
          <AlertTriangle className="w-7 h-7 text-muted-foreground/40 mb-2" />
          <p className="text-sm text-muted-foreground">No recent denials.</p>
          <p className="text-xs text-muted-foreground/70 mt-1">
            Denials appear here when scope-filter rejections happen.
          </p>
        </div>
      )}

      {!isLoading && !isError && events.length > 0 && (
        <div className="divide-y divide-border">
          {events.map((ev, idx) => (
            <div
              key={`${ev.timestamp}-${ev.tool_name}-${idx}`}
              className="flex items-center justify-between gap-4 py-2"
            >
              <div className="flex flex-col min-w-0">
                <span className="font-mono text-sm text-foreground truncate">{ev.tool_name}</span>
                <span className="text-xs text-muted-foreground">
                  {subReasonLabel[ev.sub_reason] ?? ev.sub_reason}
                </span>
              </div>
              <span className="text-xs text-muted-foreground font-mono flex-shrink-0">
                {formatRelativeTime(ev.timestamp)}
              </span>
            </div>
          ))}
        </div>
      )}
    </motion.div>
  );
}

