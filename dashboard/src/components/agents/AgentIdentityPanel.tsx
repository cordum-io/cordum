/*
 * AgentIdentityPanel — agent identity profile (risk tier, permissions,
 * activity, metadata). Hosted as the "Identity" tab inside AgentDetailPage.
 * Was a standalone route at /agents/identity/:id until the v2.5 IA cut
 * folded it into the agent detail surface.
 */
import { useState } from "react";
import { motion } from "framer-motion";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { EmptyState } from "@/components/ui/EmptyState";
import { Button } from "@/components/ui/Button";
import {
  Shield, Fingerprint, Tag, Clock, AlertTriangle, Activity, FileQuestion,
} from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import { ApiError } from "@/api/client";
import { useAgentIdentity, useAgentStats } from "@/hooks/useAgentIdentities";
import { useWorker } from "@/hooks/useWorkers";
import { useConfigStore } from "@/state/config";
import AgentIdentityCreateForm from "./AgentIdentityCreateForm";

const riskTierConfig: Record<string, { color: string; bg: string; border: string }> = {
  low:      { color: "text-emerald-400", bg: "bg-emerald-500/10", border: "border-emerald-500/30" },
  medium:   { color: "text-amber-400",   bg: "bg-amber-500/10",   border: "border-amber-500/30" },
  high:     { color: "text-orange-400",  bg: "bg-orange-500/10",  border: "border-orange-500/30" },
  critical: { color: "text-red-400",     bg: "bg-red-500/10",     border: "border-red-500/30" },
};

function TagList({ items, label }: { items?: string[]; label: string }) {
  if (!items || items.length === 0) {
    return (
      <div className="text-xs text-muted-foreground/60 italic">
        No {label.toLowerCase()} configured
      </div>
    );
  }
  return (
    <div className="flex flex-wrap gap-1.5">
      {items.map((item) => (
        <span
          key={item}
          className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-surface-2 border border-border text-xs font-mono text-foreground/80"
        >
          <Tag className="w-3 h-3 text-muted-foreground" />
          {item}
        </span>
      ))}
    </div>
  );
}

interface AgentIdentityPanelProps {
  agentId: string;
}

export default function AgentIdentityPanel({ agentId }: AgentIdentityPanelProps) {
  const { data: agent, isLoading, isError, error } = useAgentIdentity(agentId);
  const { data: stats, isLoading: statsLoading, isError: statsError } = useAgentStats(agentId);
  // Heartbeat data for the empty-state pre-fill. Loaded eagerly so the form
  // can pop instantly when the operator clicks "Create identity" — no extra
  // round trip. Falls back gracefully if the worker isn't heartbeating.
  const { data: heartbeat } = useWorker(agentId);
  const defaultOwner = useConfigStore((s) => s.principalId);
  const [showCreateForm, setShowCreateForm] = useState(false);

  if (isError) {
    if (error instanceof ApiError && error.status === 404) {
      // Empty state with an actionable CTA. Previously this was a text-only
      // placeholder pointing operators at "the /agents catalog or cordumctl"
      // — they'd leave the page, lose context, and meanwhile every audit
      // row for this worker fell back to the raw agent_id (issue #314).
      // Now: explanation + a button that pre-fills from the worker's
      // heartbeat (when available) and POSTs in place.
      return (
        <div className="space-y-4">
          <EmptyState
            icon={<FileQuestion className="w-6 h-6" />}
            title="No identity registered for this agent"
            description="This worker is reporting heartbeats but has no Agent Identity record. Audit rows for its jobs will show the raw agent_id as the label instead of a human-readable agent name. Register an identity below to fix the attribution surface."
            action={
              !showCreateForm ? (
                <Button
                  variant="default"
                  size="sm"
                  onClick={() => setShowCreateForm(true)}
                  data-testid="agent-identity-create-button"
                >
                  Create identity
                </Button>
              ) : undefined
            }
          />
          {showCreateForm && (
            <div className="instrument-card">
              <AgentIdentityCreateForm
                agentId={agentId}
                heartbeat={heartbeat ?? undefined}
                defaultOwner={defaultOwner || undefined}
                onCreated={() => setShowCreateForm(false)}
                onCancel={() => setShowCreateForm(false)}
              />
            </div>
          )}
        </div>
      );
    }
    return <ErrorBanner message={error instanceof Error ? error.message : "Failed to load agent identity"} />;
  }

  if (isLoading || !agent) {
    return (
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)}
      </div>
    );
  }

  const tier = riskTierConfig[agent.risk_tier] ?? riskTierConfig.low;

  return (
    <div className="space-y-6">
      {/* Header card with risk tier + status */}
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        className={cn("instrument-card border-l-4", tier.border)}
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-4">
            <div className={cn("w-12 h-12 rounded-xl flex items-center justify-center", tier.bg)}>
              <Fingerprint className={cn("w-6 h-6", tier.color)} />
            </div>
            <div>
              <div className="flex items-center gap-3">
                <h2 className="text-lg font-semibold text-foreground">{agent.name}</h2>
                <StatusBadge variant={agent.status === "active" ? "healthy" : agent.status === "suspended" ? "warning" : "danger"} dot>
                  {agent.status}
                </StatusBadge>
              </div>
              <div className="flex items-center gap-3 mt-1 text-sm text-muted-foreground">
                <span>Owner: <span className="font-mono">{agent.owner}</span></span>
                {agent.team && <span>Team: <span className="font-mono">{agent.team}</span></span>}
              </div>
            </div>
          </div>
          <div className={cn("px-4 py-2 rounded-xl border font-mono text-sm font-bold uppercase tracking-wider", tier.color, tier.bg, tier.border)}>
            <Shield className="w-4 h-4 inline mr-1.5" />
            {agent.risk_tier} risk
          </div>
        </div>
      </motion.div>

      {/* Stats + Permissions grid */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        {/* Activity stats */}
        <motion.div
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.1 }}
          className="instrument-card"
        >
          <div className="flex items-center gap-2 mb-4">
            <Activity className="w-4 h-4 text-cordum" />
            <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">7-Day Activity</span>
          </div>
          {statsLoading ? (
            <div className="space-y-3">
              <div className="h-8 w-24 bg-surface-2 rounded animate-pulse" />
              <div className="h-4 w-32 bg-surface-2 rounded animate-pulse" />
              <div className="h-4 w-40 bg-surface-2 rounded animate-pulse" />
            </div>
          ) : statsError ? (
            <div className="text-xs text-destructive">Failed to load activity stats</div>
          ) : (
            <div className="space-y-4">
              <div>
                <span className="text-3xl font-mono font-bold text-foreground">{stats?.total_jobs_7d ?? 0}</span>
                <span className="text-sm text-muted-foreground ml-2">jobs</span>
              </div>
              <div className="flex items-center gap-2">
                <AlertTriangle className="w-3.5 h-3.5 text-destructive" />
                <span className="text-sm">
                  <span className="font-mono font-semibold text-foreground">{stats?.denied_7d ?? 0}</span>
                  <span className="text-muted-foreground ml-1">denied</span>
                </span>
              </div>
              <div className="flex items-center gap-2">
                <Clock className="w-3.5 h-3.5 text-muted-foreground" />
                <span className="text-sm text-muted-foreground">
                  Last active: {stats?.last_active ? formatRelativeTime(new Date(stats.last_active / 1000).toISOString()) : "Never"}
                </span>
              </div>
            </div>
          )}
        </motion.div>

        {/* Permissions */}
        <motion.div
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.15 }}
          className="instrument-card lg:col-span-2"
        >
          <div className="flex items-center gap-2 mb-4">
            <Shield className="w-4 h-4 text-cordum" />
            <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">Permissions & Classifications</span>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <div className="text-xs font-mono text-muted-foreground mb-2">Allowed Topics</div>
              <TagList items={agent.allowed_topics} label="topics" />
            </div>
            <div>
              <div className="text-xs font-mono text-muted-foreground mb-2">Allowed Pools</div>
              <TagList items={agent.allowed_pools} label="pools" />
            </div>
            <div>
              <div className="text-xs font-mono text-muted-foreground mb-2">Allowed Tools</div>
              <TagList items={agent.allowed_tools} label="tools" />
            </div>
            <div>
              <div className="text-xs font-mono text-muted-foreground mb-2">Data Classifications</div>
              <TagList items={agent.data_classifications} label="classifications" />
            </div>
          </div>
        </motion.div>
      </div>

      {/* Activity Timeline */}
      <motion.div
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ delay: 0.15 }}
        className="instrument-card"
      >
        <div className="flex items-center gap-2 mb-4">
          <Activity className="w-4 h-4 text-cordum" />
          <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">Activity Timeline</span>
        </div>
        {statsLoading ? (
          <div className="space-y-3">
            {Array.from({ length: 3 }).map((_, i) => (
              <div key={i} className="h-6 bg-surface-2 rounded animate-pulse" />
            ))}
          </div>
        ) : statsError ? (
          <div className="text-xs text-destructive">Failed to load activity timeline</div>
        ) : (
          <div className="space-y-3 relative before:absolute before:left-[7px] before:top-2 before:bottom-2 before:w-px before:bg-border">
            <div className="flex items-start gap-3 pl-0">
              <div className="w-[15px] h-[15px] rounded-full bg-cordum/20 border-2 border-cordum flex-shrink-0 mt-0.5 relative z-10" />
              <div>
                <p className="text-sm font-medium text-foreground">{stats?.total_jobs_7d ?? 0} jobs processed</p>
                <p className="text-xs text-muted-foreground">Last 7 days</p>
              </div>
            </div>
            {(stats?.denied_7d ?? 0) > 0 && (
              <div className="flex items-start gap-3 pl-0">
                <div className="w-[15px] h-[15px] rounded-full bg-destructive/20 border-2 border-destructive flex-shrink-0 mt-0.5 relative z-10" />
                <div>
                  <p className="text-sm font-medium text-foreground">{stats?.denied_7d} jobs denied by policy</p>
                  <p className="text-xs text-muted-foreground">Safety kernel enforcement</p>
                </div>
              </div>
            )}
            <div className="flex items-start gap-3 pl-0">
              <div className="w-[15px] h-[15px] rounded-full bg-muted border-2 border-muted-foreground/30 flex-shrink-0 mt-0.5 relative z-10" />
              <div>
                <p className="text-sm text-muted-foreground">
                  {stats?.last_active
                    ? `Last active ${formatRelativeTime(new Date(stats.last_active / 1000).toISOString())}`
                    : "No recent activity"}
                </p>
              </div>
            </div>
            <div className="flex items-start gap-3 pl-0">
              <div className="w-[15px] h-[15px] rounded-full bg-muted border-2 border-muted-foreground/30 flex-shrink-0 mt-0.5 relative z-10" />
              <div>
                <p className="text-sm text-muted-foreground">
                  Identity created {formatRelativeTime(agent.created_at)}
                </p>
              </div>
            </div>
          </div>
        )}
      </motion.div>

      {/* Metadata */}
      <motion.div
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ delay: 0.2 }}
        className="instrument-card"
      >
        <div className="text-xs font-mono uppercase tracking-widest text-muted-foreground mb-3">Metadata</div>
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
          <div>
            <span className="text-muted-foreground">ID</span>
            <p className="font-mono text-foreground truncate" title={agent.id}>{agent.id}</p>
          </div>
          <div>
            <span className="text-muted-foreground">Created</span>
            <p className="text-foreground">{formatRelativeTime(agent.created_at)}</p>
          </div>
          <div>
            <span className="text-muted-foreground">Updated</span>
            <p className="text-foreground">{formatRelativeTime(agent.updated_at)}</p>
          </div>
          <div>
            <span className="text-muted-foreground">Risk Tier</span>
            <p className={cn("font-mono font-semibold uppercase", tier.color)}>{agent.risk_tier}</p>
          </div>
        </div>
      </motion.div>
    </div>
  );
}
