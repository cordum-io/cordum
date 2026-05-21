/*
 * DESIGN: "Control Surface" — Agent Fleet
 * Matches cordumds-gj5mw4zm.manus.space showcase patterns
 */
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get } from "@/api/client";
import { mapHeartbeatToWorker, type BackendHeartbeat } from "@/api/transform";
import type { Worker } from "@/api/types";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { Input } from "@/components/ui/Input";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import { StatTile } from "@/components/ui/StatTile";
import { Tabs } from "@/components/ui/Tabs";
import {
  Cpu, Search, RefreshCw, Zap, Shield, Fingerprint,
} from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { cn, formatRelativeTime, clickableRowProps } from "@/lib/utils";
import { EntitlementGate } from "@/components/EntitlementGate";
import { useAgentIdentities } from "@/hooks/useAgentIdentities";
import type { AgentIdentity } from "@/api/types";
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

const tableBodyVariants = {
  hidden: {},
  visible: {
    transition: {
      staggerChildren: 0.04,
    },
  },
};

const tableRowVariants = {
  hidden: { opacity: 0, y: 8 },
  visible: { opacity: 1, y: 0 },
};

type AgentsTab = "fleet" | "identities";
type FleetView = "table" | "by-pool";

// Migrate legacy ?tab= values (pre task-083581ca consolidation) so old
// bookmarks/deep-links resolve to the new 2-tab IA. Pool Topology folds
// into Fleet Overview's view-toggle; Agent Registry was a redundant
// worker table; Identity Directory becomes Identities (rename only).
function migrateTabParam(raw: string | null): { tab: AgentsTab; view?: FleetView } {
  switch ((raw ?? "").toLowerCase()) {
    case "pools":
      return { tab: "fleet", view: "by-pool" };
    case "registry":
      return { tab: "fleet" };
    case "identity":
    case "identities":
      return { tab: "identities" };
    case "fleet":
    case "":
      return { tab: "fleet" };
    default:
      return { tab: "fleet" };
  }
}

function parseFleetView(raw: string | null): FleetView {
  return raw === "by-pool" ? "by-pool" : "table";
}

export default function AgentsPage() {
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const migrated = migrateTabParam(searchParams.get("tab"));
  const [tab, setTab] = useState<AgentsTab>(migrated.tab);
  const [view, setView] = useState<FleetView>(
    migrated.view ?? parseFleetView(searchParams.get("view")),
  );
  const [drawerWorkerId, setDrawerWorkerId] = useState<string | null>(null);
  const poolFilter = searchParams.get("pool")?.trim() ?? "";
  const topicFilter = searchParams.get("topic")?.trim() ?? "";

  // One-shot URL migration on mount: rewrite legacy ?tab=pools|registry|identity
  // to the new tab+view combination so existing bookmarks don't 404 visually.
  // replace:true so the rewrite isn't added to browser history.
  useEffect(() => {
    const raw = searchParams.get("tab");
    if (raw === null) return;
    const m = migrateTabParam(raw);
    if (m.tab === raw && (!m.view || m.view === searchParams.get("view"))) return;
    const next = new URLSearchParams(searchParams);
    next.set("tab", m.tab);
    if (m.view) next.set("view", m.view);
    setSearchParams(next, { replace: true });
  }, []);

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

  // task-083581ca: consolidated from 4 tabs to 2. Pool Topology folded
  // into Fleet Overview's view-toggle (Table / By Pool); Agent Registry
  // removed as redundant (its worker table duplicated Fleet Overview's);
  // Identity Directory renamed to Identities. Legacy ?tab= values
  // migrate via migrateTabParam so old bookmarks resolve.
  const topTabs = [
    { id: "fleet", label: "Fleet Overview" },
    { id: "identities", label: "Identities", icon: <Fingerprint className="h-3.5 w-3.5" /> },
  ];

  const fleetViewTabs = [
    { id: "table", label: "Table" },
    { id: "by-pool", label: "By Pool" },
  ];

  const statusTabs = ["all", "idle", "busy", "draining", "offline"].map((status) => ({
    id: status,
    label: status.charAt(0).toUpperCase() + status.slice(1),
    count: status === "all" ? allWorkers.length : allWorkers.filter((worker) => worker.status === status).length,
  }));

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
      <Tabs
        tabs={topTabs}
        activeTab={tab}
        onChange={(id) => setTab(id as typeof tab)}
        ariaLabel="Agent fleet sections"
        className="w-full"
      />

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
            <StatTile
              label="Total agents"
              value={allWorkers.length}
              helperText="Workers seen in the fleet registry."
              icon={<Cpu className="h-4 w-4 text-cordum" />}
            />
            <StatTile
              label="Idle"
              value={idleCount}
              helperText="Ready to pick up new work."
              accent="healthy"
            />
            <StatTile
              label="Busy"
              value={busyCount}
              helperText="Currently processing jobs."
              icon={<Zap className="h-4 w-4 text-sky-400" />}
              accent="info"
            />
            <StatTile
              label="Offline"
              value={offlineCount}
              helperText="Disconnected or not heartbeating."
              accent={offlineCount > 0 ? "danger" : "muted"}
            />
          </>
        )}
      </motion.div>

      {/* Filters — showcase style */}
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="text"
          placeholder="Search agents..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          icon={<Search className="h-3.5 w-3.5" />}
          className="h-9 max-w-sm flex-1 text-sm"
        />
        <Tabs
          tabs={statusTabs}
          activeTab={statusFilter}
          onChange={setStatusFilter}
          variant="segmented"
          ariaLabel="Agent status filters"
          className="w-full md:w-auto"
        />
      </div>

      {/* task-083581ca: view-mode toggle absorbs the deleted "Pool
          Topology" tab — same worker data, two layout choices. URL
          state syncs via setSearchParams so refresh keeps the choice. */}
      <Tabs
        tabs={fleetViewTabs}
        activeTab={view}
        onChange={(id) => {
          const next = (id === "by-pool" ? "by-pool" : "table") as FleetView;
          setView(next);
          const params = new URLSearchParams(searchParams);
          if (next === "table") params.delete("view");
          else params.set("view", next);
          setSearchParams(params, { replace: true });
        }}
        variant="segmented"
        ariaLabel="Fleet view mode"
        className="w-full md:w-auto"
      />

      {/* Worker content — Table vs By Pool view */}
      {view === "by-pool" ? (
        <PoolGroupedView
          workers={allWorkers}
          onWorkerClick={(id) => setDrawerWorkerId(id)}
        />
      ) : isLoading ? (
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
            <motion.tbody initial="hidden" animate="visible" variants={tableBodyVariants}>
              {filtered.map((w) => (
                <motion.tr
                  key={w.id}
                  variants={tableRowVariants}
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
                </motion.tr>
              ))}
            </motion.tbody>
          </table>
          </div>
        </div>
      )}

      </>)}

      {tab === "identities" && (
        <EntitlementGate entitlement="agentIdentity" label="Agent Identities" description="Agent identity management requires an Enterprise license.">
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
            <motion.tbody initial="hidden" animate="visible" variants={tableBodyVariants}>
              {identities.map((agent: AgentIdentity) => (
                <motion.tr
                  key={agent.id}
                  variants={tableRowVariants}
                  {...clickableRowProps(() => navigate(`/agents/${agent.id}?tab=identity`))}
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
                </motion.tr>
              ))}
            </motion.tbody>
          </table>
        </div>
      </div>
    </motion.div>
  );
}

/* AgentRegistryTab removed by task-083581ca — its worker table duplicated
   Fleet Overview's. Workers data is fully surfaced via the consolidated
   Fleet Overview tab (Table / By Pool view-modes). */
