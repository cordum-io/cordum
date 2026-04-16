/*
 * DESIGN: "Control Surface" — Agent Fleet
 * Matches cordumds-gj5mw4zm.manus.space showcase patterns
 */
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get } from "@/api/client";
import { mapHeartbeatToWorker, type BackendHeartbeat } from "@/api/transform";
import type { Worker } from "@/api/types";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import {
  Cpu, Search, RefreshCw, Zap, Shield, Fingerprint,
} from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { cn, formatRelativeTime, clickableRowProps } from "@/lib/utils";
import { EntitlementGate } from "@/components/EntitlementGate";
import { useAgentIdentities } from "@/hooks/useAgentIdentities";
import type { AgentIdentity } from "@/api/types";
import { TierLimitBar } from "@/components/TierLimitBar";
import { UpgradePrompt } from "@/components/UpgradePrompt";
import { useLicenseUsage } from "@/hooks/useLicense";
import { useWorkers } from "@/hooks/useWorkers";
import { PoolGroupedView } from "@/components/agents/PoolGroupedView";
import { WorkerDetailDrawer } from "@/components/agents/WorkerDetailDrawer";
import { ErrorBanner } from "@/components/ui/ErrorBanner";

function workerStatusVariant(status: string) {
  switch (status) {
    case "idle": return "healthy" as const;
    case "busy": return "info" as const;
    case "draining": return "warning" as const;
    case "offline": return "danger" as const;
    default: return "muted" as const;
  }
}

export default function AgentsPage() {
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const [tab, setTab] = useState<"fleet" | "registry" | "pools" | "identity">("fleet");
  const [drawerWorkerId, setDrawerWorkerId] = useState<string | null>(null);
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const poolFilter = searchParams.get("pool")?.trim() ?? "";
  const topicFilter = searchParams.get("topic")?.trim() ?? "";

  const { data: workers, isLoading, isError, error, refetch } = useQuery({
    queryKey: ["workers"],
    queryFn: async () => {
      const res = await get<{ items?: BackendHeartbeat[] } | BackendHeartbeat[]>(
        "/workers",
      );
      const items = Array.isArray(res) ? res : (res.items ?? []);
      return items.map(mapHeartbeatToWorker).filter((w): w is Worker => !!w);
    },
    refetchInterval: 15_000,
  });

  const allWorkers = workers ?? [];
  const idleCount = allWorkers.filter((w) => w.status === "idle").length;
  const busyCount = allWorkers.filter((w) => w.status === "busy").length;
  const offlineCount = allWorkers.filter((w) => w.status === "offline").length;
  const { data: licenseUsage } = useLicenseUsage();
  const workerMetric = licenseUsage?.usage?.workers;

  // Sort: offline agents go to the bottom
  const statusOrder: Record<string, number> = { busy: 0, idle: 1, draining: 2, offline: 3 };
  const sorted = [...allWorkers].sort((a, b) => (statusOrder[a.status] ?? 99) - (statusOrder[b.status] ?? 99));

  const filtered = sorted.filter((w) => {
    if (statusFilter !== "all" && w.status !== statusFilter) return false;
    if (poolFilter && (w.pool ?? "") !== poolFilter) return false;
    if (search) {
      const q = search.toLowerCase();
      return (
        w.id.toLowerCase().includes(q) ||
        (w.pool ?? "").toLowerCase().includes(q) ||
        w.capabilities?.some((t: string) => t.toLowerCase().includes(q))
      );
    }
    return true;
  });

  const clearFilters = () => {
    setSearch("");
    setStatusFilter("all");
    setTab("fleet");
    navigate("/agents");
  };

  if (isError) {
    return <ErrorBanner message={error instanceof Error ? error.message : "Failed to load agents"} onRetry={() => void refetch()} />;
  }

  return (
    <div className="space-y-6">
      <PageHeader
        label="Fleet"
        title="Agent Fleet"
        subtitle="Monitor and manage worker agents across all pools"
        actions={
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            <RefreshCw className="w-3 h-3 mr-1" />
            Refresh
          </Button>
        }
      />

      {workerMetric && (
        <div className="space-y-3">
          <TierLimitBar
            label="Workers"
            metric={workerMetric}
            detail={
              typeof workerMetric.registered === "number" && typeof workerMetric.connected === "number"
                ? `${workerMetric.registered.toLocaleString()} registered · ${workerMetric.connected.toLocaleString()} connected`
                : "Registered and connected workers count toward the active tier."
            }
          />
          <UpgradePrompt label="Workers" metric={workerMetric} plan={licenseUsage?.plan} />
        </div>
      )}

      {(poolFilter || topicFilter) && (
        <div className="instrument-card flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="space-y-1">
            <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
              Topic coverage filter
            </p>
            <p className="text-sm text-foreground">
              Showing workers in <span className="font-mono">{poolFilter || "all pools"}</span>
              {topicFilter && (
                <>
                  {" "}for topic <span className="font-mono">{topicFilter}</span>
                </>
              )}
              .
            </p>
          </div>
          <Button variant="outline" size="sm" onClick={clearFilters}>
            Clear filter
          </Button>
        </div>
      )}

      {/* Tabs */}
      <div className="flex items-center gap-4 border-b border-border">
        <button type="button"
          onClick={() => setTab("fleet")}
          className={cn(
            "pb-2 text-sm font-medium border-b-2 transition-colors",
            tab === "fleet" ? "border-cordum text-cordum" : "border-transparent text-muted-foreground hover:text-foreground"
          )}
        >
          Fleet Overview
        </button>
        <button type="button"
          onClick={() => setTab("registry")}
          className={cn(
            "pb-2 text-sm font-medium border-b-2 transition-colors",
            tab === "registry" ? "border-cordum text-cordum" : "border-transparent text-muted-foreground hover:text-foreground"
          )}
        >
          Agent Registry
        </button>
        <button type="button"
          onClick={() => setTab("pools")}
          className={cn(
            "pb-2 text-sm font-medium border-b-2 transition-colors",
            tab === "pools" ? "border-cordum text-cordum" : "border-transparent text-muted-foreground hover:text-foreground"
          )}
        >
          Pool Topology
        </button>
        <button type="button"
          onClick={() => setTab("identity")}
          className={cn(
            "pb-2 text-sm font-medium border-b-2 transition-colors flex items-center gap-1.5",
            tab === "identity" ? "border-cordum text-cordum" : "border-transparent text-muted-foreground hover:text-foreground"
          )}
        >
          <Fingerprint className="w-3.5 h-3.5" />
          Identity Directory
        </button>
      </div>

      {tab === "fleet" && (<>
      {/* KPI Row — showcase style */}
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3 }}
        className="grid grid-cols-2 lg:grid-cols-4 gap-4"
      >
        {isLoading ? (
          Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)
        ) : (
          <>
            <div className="instrument-card">
              <div className="flex items-center justify-between mb-3">
                <span className="text-xs font-mono text-muted-foreground uppercase tracking-widest">Total Agents</span>
                <Cpu className="w-4 h-4 text-cordum" />
              </div>
              <span className="font-mono text-3xl font-bold text-foreground">{allWorkers.length}</span>
              <div className="flex gap-1 mt-3">
                {allWorkers.map((w, i) => (
                  <div
                    key={i}
                    className={cn(
                      "w-2 h-2 rounded-full",
                      w.status === "idle" || w.status === "busy" ? "bg-[var(--color-success)]" : "bg-muted-foreground",
                    )}
                  />
                ))}
              </div>
            </div>

            <div className="instrument-card">
              <div className="flex items-center justify-between mb-3">
                <span className="text-xs font-mono text-muted-foreground uppercase tracking-widest">Idle</span>
                <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-success)] status-pulse" />
              </div>
              <span className="font-mono text-3xl font-bold text-[var(--color-success)]">{idleCount}</span>
              <p className="text-xs text-muted-foreground mt-1">Ready for work</p>
            </div>

            <div className="instrument-card">
              <div className="flex items-center justify-between mb-3">
                <span className="text-xs font-mono text-muted-foreground uppercase tracking-widest">Busy</span>
                <Zap className="w-4 h-4 text-[var(--color-info)]" />
              </div>
              <span className="font-mono text-3xl font-bold text-[var(--color-info)]">{busyCount}</span>
              <p className="text-xs text-muted-foreground mt-1">Processing jobs</p>
            </div>

            <div className={cn("instrument-card", offlineCount > 0 && "status-danger")}>
              <div className="flex items-center justify-between mb-3">
                <span className="text-xs font-mono text-muted-foreground uppercase tracking-widest">Offline</span>
              </div>
              <span className={cn("font-mono text-3xl font-bold", offlineCount > 0 ? "text-destructive" : "text-foreground")}>{offlineCount}</span>
              <p className="text-xs text-muted-foreground mt-1">Disconnected</p>
            </div>
          </>
        )}
      </motion.div>

      {/* Filters — showcase style */}
      <div className="flex items-center gap-3">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search agents..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-8 w-full pl-8 pr-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
          />
        </div>
        <div className="flex items-center gap-1 bg-surface-1 border border-border rounded-2xl p-0.5">
          {["all", "idle", "busy", "draining", "offline"].map((s) => (
            <button type="button"
              key={s}
              onClick={() => setStatusFilter(s)}
              className={cn(
                "px-3 py-1.5 text-xs font-medium rounded transition-colors",
                statusFilter === s
                  ? "bg-cordum/10 text-cordum"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {s.charAt(0).toUpperCase() + s.slice(1)}
            </button>
          ))}
        </div>
      </div>

      {/* Worker Table — showcase style */}
      {isLoading ? (
        <SkeletonTable rows={6} />
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<Cpu className="w-5 h-5" />}
          title="No agents found"
          description={search ? "Try adjusting your search" : "Agents connect via the Cordum SDK. Start an agent with your API key to see it here."}
          action={search ? undefined : (
            <Button variant="outline" size="sm" onClick={() => navigate("/settings/keys")}>
              View API keys
            </Button>
          )}
        />
      ) : (
        <div className="instrument-card overflow-hidden">
          <div className="flex items-center justify-between px-5 py-3 border-b border-border">
            <h2 className="font-display font-semibold text-sm text-foreground">Worker Pool</h2>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              <RefreshCw className="w-3 h-3 mr-1" />
              Refresh
            </Button>
          </div>
          <div className="overflow-x-auto">
          <table className="w-full min-w-[750px]">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Worker</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Status</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Pool</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Capabilities</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Jobs</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Last Seen</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((w) => (
                <tr
                  key={w.id}
                  {...clickableRowProps(() => navigate(`/agents/${w.id}`))}
                  className={cn(
                    "border-b border-border hover:bg-surface-1 transition-colors cursor-pointer",
                    w.status === "offline" && "opacity-50"
                  )}
                >
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2">
                      <Zap className="w-3.5 h-3.5 text-cordum" />
                      <span className="text-sm font-medium text-foreground">{w.id.slice(0, 16)}</span>
                    </div>
                  </td>
                  <td className="px-5 py-3">
                    <StatusBadge variant={workerStatusVariant(w.status)} dot pulse={w.status === "busy"}>
                      {w.status}
                    </StatusBadge>
                  </td>
                  <td className="px-5 py-3 text-sm text-muted-foreground">{w.pool || "default"}</td>
                  <td className="px-5 py-3">
                    <div className="flex flex-wrap gap-1">
                      {(w.capabilities ?? []).slice(0, 3).map((t: string) => (
                        <span key={t} className="text-xs font-mono px-1.5 py-0.5 rounded bg-surface-2 text-muted-foreground">
                          {t}
                        </span>
                      ))}
                      {(w.capabilities?.length ?? 0) > 3 && (
                        <span className="text-xs text-muted-foreground">+{(w.capabilities?.length ?? 0) - 3}</span>
                      )}
                    </div>
                  </td>
                  <td className="px-5 py-3 font-mono text-sm text-foreground">{w.activeJobs} / {w.capacity}</td>
                  <td className="px-5 py-3 text-sm text-muted-foreground">
                    {w.lastHeartbeat ? formatRelativeTime(w.lastHeartbeat) : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
        </div>
      )}

      </>)}

      {tab === "registry" && (
        <AgentRegistryTab />
      )}

      {tab === "pools" && (
        <PoolGroupedView
          workers={allWorkers}
          onWorkerClick={(id) => setDrawerWorkerId(id)}
        />
      )}

      {tab === "identity" && (
        <EntitlementGate entitlement="agentIdentity" label="Agent Identity Directory" description="Agent identity management requires an Enterprise license.">
          <AgentIdentityTab />
        </EntitlementGate>
      )}

      <WorkerDetailDrawer
        workerId={drawerWorkerId}
        onClose={() => setDrawerWorkerId(null)}
      />
    </div>
  );
}

/* --- Risk Tier Badge --- */
const riskTierConfig: Record<string, { color: string; bg: string }> = {
  low:      { color: "text-emerald-400", bg: "bg-emerald-500/10 border-emerald-500/20" },
  medium:   { color: "text-amber-400",   bg: "bg-amber-500/10 border-amber-500/20" },
  high:     { color: "text-orange-400",  bg: "bg-orange-500/10 border-orange-500/20" },
  critical: { color: "text-red-400",     bg: "bg-red-500/10 border-red-500/20" },
};

function RiskTierBadge({ tier }: { tier: string }) {
  const c = riskTierConfig[tier] ?? { color: "text-muted-foreground", bg: "bg-surface-2" };
  return (
    <span className={cn("inline-flex items-center gap-1 px-2 py-0.5 rounded-full border text-xs font-mono font-semibold uppercase tracking-wider", c.color, c.bg)}>
      <Shield className="w-3 h-3" />
      {tier}
    </span>
  );
}

/* --- Agent Identity Tab --- */
function AgentIdentityTab() {
  const navigate = useNavigate();
  const [cursor] = useState("");
  const { data, isLoading, isError, error } = useAgentIdentities({ limit: 25, cursor });
  const identities = data?.items ?? [];

  if (isLoading) {
    return <SkeletonTable rows={5} />;
  }

  if (isError) {
    return (
      <EmptyState
        icon={<Fingerprint className="w-12 h-12 text-destructive/40" />}
        title="Failed to load agent identities"
        description={error instanceof Error ? error.message : "An error occurred."}
      />
    );
  }

  if (identities.length === 0) {
    return (
      <EmptyState
        icon={<Fingerprint className="w-12 h-12 text-muted-foreground/40" />}
        title="No agent identities registered"
        description="Create agent identities via the API to assign risk tiers, permissions, and audit trails to your workers."
      />
    );
  }

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.25 }}
    >
      <div className="instrument-card overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-left">
            <thead>
              <tr className="border-b border-border">
                <th className="px-5 py-3 text-xs font-mono uppercase tracking-widest text-muted-foreground">Name</th>
                <th className="px-5 py-3 text-xs font-mono uppercase tracking-widest text-muted-foreground">Owner</th>
                <th className="px-5 py-3 text-xs font-mono uppercase tracking-widest text-muted-foreground">Team</th>
                <th className="px-5 py-3 text-xs font-mono uppercase tracking-widest text-muted-foreground">Risk Tier</th>
                <th className="px-5 py-3 text-xs font-mono uppercase tracking-widest text-muted-foreground">Status</th>
                <th className="px-5 py-3 text-xs font-mono uppercase tracking-widest text-muted-foreground">Last Active</th>
              </tr>
            </thead>
            <tbody>
              {identities.map((agent: AgentIdentity) => (
                <tr
                  key={agent.id}
                  {...clickableRowProps(() => navigate(`/agents/identity/${agent.id}`))}
                  className="border-b border-border/50 hover:bg-surface-2 transition-colors cursor-pointer"
                >
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2">
                      <Fingerprint className="w-4 h-4 text-cordum/60" />
                      <span className="text-sm font-medium text-foreground">{agent.name}</span>
                    </div>
                    {agent.description && (
                      <p className="text-xs text-muted-foreground mt-0.5 ml-6 truncate max-w-[240px]">{agent.description}</p>
                    )}
                  </td>
                  <td className="px-5 py-3 text-sm text-muted-foreground">{agent.owner}</td>
                  <td className="px-5 py-3 text-sm text-muted-foreground">{agent.team || "—"}</td>
                  <td className="px-5 py-3"><RiskTierBadge tier={agent.risk_tier} /></td>
                  <td className="px-5 py-3">
                    <StatusBadge variant={agent.status === "active" ? "healthy" : agent.status === "suspended" ? "warning" : "danger"} dot>
                      {agent.status}
                    </StatusBadge>
                  </td>
                  <td className="px-5 py-3 text-sm text-muted-foreground">
                    {agent.last_active
                      ? formatRelativeTime(new Date(agent.last_active / 1000).toISOString())
                      : "Never"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </motion.div>
  );
}

/* --- Agent Registry Tab --- */
function AgentRegistryTab() {
  const navigate = useNavigate();
  const { data: workers = [], isLoading } = useWorkers();

  if (isLoading) {
    return <SkeletonTable rows={6} />;
  }

  if (workers.length === 0) {
    return (
      <EmptyState icon={<Shield className="w-8 h-8" />} title="No agents registered" description="Agents will appear here after they connect and send heartbeats." action={<Button variant="outline" size="sm" onClick={() => navigate("/settings/keys")}>View API keys</Button>} />
    );
  }

  return (
    <div className="space-y-4">
      <p className="text-xs text-muted-foreground">Agents that have submitted jobs, with their safety decision breakdown and policy bindings.</p>
      <div className="instrument-card overflow-hidden">
        <div className="overflow-x-auto">
        <table className="w-full min-w-[800px]">
          <thead>
            <tr className="border-b border-border bg-surface-0">
              <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Agent</th>
              <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Pool</th>
              <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Status</th>
              <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Active Jobs</th>
              <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Capacity</th>
              <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Capabilities</th>
              <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Last Active</th>
            </tr>
          </thead>
          <tbody>
            {workers.map((w) => (
              <tr
                key={w.id}
                {...clickableRowProps(() => navigate(`/agents/${w.id}`))}
                className="border-b border-border hover:bg-surface-1 transition-colors cursor-pointer"
              >
                <td className="px-5 py-3">
                  <div className="flex items-center gap-2">
                    <Shield className="w-3.5 h-3.5 text-cordum" />
                    <div>
                      <p className="text-sm font-medium text-foreground">{w.name || w.id}</p>
                      <p className="text-xs font-mono text-muted-foreground">{w.id}</p>
                    </div>
                  </div>
                </td>
                <td className="px-5 py-3 font-mono text-sm text-foreground">{w.pool || "—"}</td>
                <td className="px-5 py-3">
                  <StatusBadge variant={w.status === "busy" ? "warning" : w.status === "idle" ? "healthy" : "muted"}>{w.status}</StatusBadge>
                </td>
                <td className="px-5 py-3 font-mono text-sm text-foreground">{w.activeJobs}</td>
                <td className="px-5 py-3 font-mono text-sm text-foreground">{w.capacity}</td>
                <td className="px-5 py-3">
                  <div className="flex flex-wrap gap-1">
                    {w.capabilities?.slice(0, 3).map((c) => (
                      <span key={c} className="text-xs font-mono px-1.5 py-0.5 rounded bg-cordum/10 text-cordum">{c}</span>
                    ))}
                    {(w.capabilities?.length ?? 0) > 3 && (
                      <span className="text-xs font-mono text-muted-foreground">+{(w.capabilities?.length ?? 0) - 3}</span>
                    )}
                  </div>
                </td>
                <td className="px-5 py-3 text-sm text-muted-foreground">{w.lastHeartbeat ? formatRelativeTime(w.lastHeartbeat) : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
        </div>
      </div>
    </div>
  );
}
