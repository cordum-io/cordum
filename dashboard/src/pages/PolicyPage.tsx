import { useState, useCallback } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Shield, History, Play, Layers, Loader, FileCode, Blocks, Clock } from "lucide-react";
import { get, put } from "../api/client";
import { usePolicyBundles, usePolicySnapshots } from "../hooks/usePolicies";
import { MetricCard } from "../components/MetricCard";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { cn } from "../lib/utils";
import { PolicyYamlEditor } from "../components/policy/PolicyYamlEditor";
import { RulesTable } from "../components/policy/RulesTable";
import { PolicySimulator } from "../components/policy/PolicySimulator";
import { PolicyReplay } from "../components/policy/PolicyReplay";
import { PublishControls } from "../components/policy/PublishControls";
import { SnapshotComparison } from "../components/policy/SnapshotComparison";
import { VisualRuleBuilder } from "../components/policy/VisualRuleBuilder";
import type { PolicyBundle } from "../api/types";

// ---------------------------------------------------------------------------
// Tabs
// ---------------------------------------------------------------------------

type Tab = "overview" | "rules" | "builder" | "editor" | "simulator" | "snapshots";

const tabs: { key: Tab; label: string; icon: typeof Shield }[] = [
  { key: "overview", label: "Overview", icon: Layers },
  { key: "rules", label: "Rules", icon: Shield },
  { key: "builder", label: "Builder", icon: Blocks },
  { key: "editor", label: "YAML", icon: FileCode },
  { key: "simulator", label: "Simulator", icon: Play },
  { key: "snapshots", label: "Snapshots", icon: History },
];

// ---------------------------------------------------------------------------
// Health indicator
// ---------------------------------------------------------------------------

const healthColors: Record<string, string> = {
  healthy: "bg-success",
  degraded: "bg-warning",
  unhealthy: "bg-danger",
};

function HealthDot({ status }: { status?: string }) {
  const color = healthColors[status ?? ""] ?? "bg-muted";
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-muted">
      <span className={cn("inline-block h-2 w-2 rounded-full", color)} />
      {status ?? "unknown"}
    </span>
  );
}

// ---------------------------------------------------------------------------
// Relative time
// ---------------------------------------------------------------------------

function timeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

// ---------------------------------------------------------------------------
// Toggle mutation
// ---------------------------------------------------------------------------

function useToggleBundle() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { id: string; enabled: boolean }>({
    mutationFn: async ({ id, enabled }) => {
      const detail = await get<{ content?: string }>(`/policy/bundles/${id}`);
      const content = (detail.content ?? "").trim();
      if (!content) {
        throw new Error("bundle content is required");
      }
      await put<void>(`/policy/bundles/${id}`, { content, enabled });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["policy-bundles"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Bundle card
// ---------------------------------------------------------------------------

function BundleCard({
  bundle,
  onToggle,
  toggling,
  onClick,
}: {
  bundle: PolicyBundle;
  onToggle: (id: string, enabled: boolean) => void;
  toggling: boolean;
  onClick: () => void;
}) {
  const canToggle = bundle.id.startsWith("secops/");
  const isEnabled = bundle.enabled ?? true;
  const handleToggle = useCallback(
    (e: React.MouseEvent) => {
      e.stopPropagation();
      if (!canToggle) return;
      const next = !isEnabled;
      const confirmed = window.confirm(
        `${next ? "Enable" : "Disable"} bundle "${bundle.name}"?`,
      );
      if (confirmed) onToggle(bundle.id, next);
    },
    [bundle, onToggle, canToggle, isEnabled],
  );

  return (
    <Card
      className="cursor-pointer hover:shadow-lift transition-shadow"
      onClick={onClick}
    >
      <CardHeader>
        <CardTitle>{bundle.name}</CardTitle>
        <Badge variant={isEnabled ? "success" : "default"}>
          {isEnabled ? "Enabled" : "Disabled"}
        </Badge>
      </CardHeader>

      <div className="space-y-3">
        {/* Rule count & version */}
        <div className="flex items-center gap-4 text-sm">
          <span className="text-muted">
            {bundle.rules.length} rule{bundle.rules.length !== 1 ? "s" : ""}
          </span>
          <Badge variant="info">v{bundle.version ?? "—"}</Badge>
        </div>

        {/* Health status */}
        <HealthDot status={bundle.healthStatus} />

        {/* Last published */}
        <div className="text-xs text-muted">
          {bundle.publishedAt
            ? `Published ${timeAgo(bundle.publishedAt)}`
            : "Not yet published"}
        </div>

        {/* Toggle switch */}
        <div className="pt-1">
          <button
            type="button"
            role="switch"
            aria-checked={isEnabled}
            disabled={toggling || !canToggle}
            onClick={handleToggle}
            className={cn(
              "relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent",
              isEnabled ? "bg-accent" : "bg-surface2",
              (toggling || !canToggle) && "opacity-50 cursor-not-allowed",
            )}
          >
            <span
              className={cn(
                "pointer-events-none inline-block h-5 w-5 rounded-full bg-white shadow-sm transition-transform duration-200",
                isEnabled ? "translate-x-5" : "translate-x-0",
              )}
            />
          </button>
          {!canToggle && (
            <p className="mt-1 text-[10px] text-muted">
              Managed by pack — edit via YAML only.
            </p>
          )}
        </div>
      </div>
    </Card>
  );
}

export default function PolicyPage() {
  const [activeTab, setActiveTab] = useState<Tab>("builder");
  const [selectedBundleId, setSelectedBundleId] = useState<string>("");
  const navigate = useNavigate();

  const { data, isLoading, isError } = usePolicyBundles();
  const bundles = data?.items ?? [];
  const activeBundleId = selectedBundleId || bundles[0]?.id || "";
  const { data: snapshotsData } = usePolicySnapshots();

  const toggleBundle = useToggleBundle();

  const handleToggle = useCallback(
    (id: string, enabled: boolean) => {
      toggleBundle.mutate({ id, enabled });
    },
    [toggleBundle],
  );

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="font-display text-2xl font-semibold text-ink">
            Policy Studio
          </h2>
          <p className="text-sm text-muted">
            Define safety rules that govern how AI jobs are evaluated.
          </p>
        </div>

        {/* Bundle selector for non-overview tabs */}
        {activeTab !== "overview" && bundles.length > 1 && (
          <select
            value={activeBundleId}
            onChange={(e) => setSelectedBundleId(e.target.value)}
            className="rounded-2xl border border-border bg-white/70 px-4 py-2 text-sm text-ink"
          >
            {bundles.map((b) => (
              <option key={b.id} value={b.id}>
                {b.name} (v{b.version})
              </option>
            ))}
          </select>
        )}
      </div>

      {/* Tabs */}
      <div className="flex gap-1 overflow-x-auto rounded-full border border-border p-1">
        {tabs.map((tab) => {
          const Icon = tab.icon;
          return (
            <button
              key={tab.key}
              type="button"
              className={cn(
                "flex shrink-0 items-center gap-2 rounded-full px-5 py-2 text-xs font-semibold uppercase tracking-widest transition",
                activeTab === tab.key
                  ? "bg-accent/15 text-accent"
                  : "text-muted hover:text-ink",
              )}
              onClick={() => setActiveTab(tab.key)}
            >
              <Icon className="h-3.5 w-3.5" />
              {tab.label}
            </button>
          );
        })}
      </div>

      {/* Content */}
      {isLoading ? (
        <div className="flex items-center justify-center py-16 text-sm text-muted">
          <Loader className="mr-2 h-4 w-4 animate-spin" />
          Loading policy bundles...
        </div>
      ) : isError ? (
        <div className="py-16 text-center text-sm text-danger">
          Failed to load policy bundles.
        </div>
      ) : activeTab === "overview" ? (
        bundles.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-border px-6 py-12 text-center text-sm text-muted">
            No policy bundles configured
          </div>
        ) : (
          <div className="space-y-6">
            {/* Metrics row */}
            <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
              <MetricCard
                title="Total Rules"
                value={bundles.reduce((sum, b) => sum + b.rules.length, 0)}
                icon={<Shield className="h-5 w-5 text-muted" />}
              />
              <MetricCard
                title="Active Bundles"
                value={bundles.filter((b) => b.enabled !== false).length}
                detail={`${bundles.length} total`}
                icon={<Layers className="h-5 w-5 text-muted" />}
              />
              <MetricCard
                title="Snapshots"
                value={snapshotsData?.items?.length ?? 0}
                icon={<History className="h-5 w-5 text-muted" />}
              />
              <MetricCard
                title="Last Published"
                value={(() => {
                  const dates = bundles
                    .filter((b) => b.publishedAt)
                    .map((b) => new Date(b.publishedAt!).getTime());
                  if (dates.length === 0) return "Never";
                  const latest = new Date(Math.max(...dates)).toISOString();
                  return timeAgo(latest);
                })()}
                icon={<Clock className="h-5 w-5 text-muted" />}
              />
            </div>

            {/* Bundle cards */}
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {bundles.map((bundle: PolicyBundle) => (
                <BundleCard
                  key={bundle.id}
                  bundle={bundle}
                  onToggle={handleToggle}
                  toggling={toggleBundle.isPending}
                  onClick={() => navigate(`/policy/${bundle.id}`)}
                />
              ))}
            </div>
          </div>
        )
      ) : activeTab === "rules" ? (
        <RulesTable
          onSelectRule={() => setActiveTab("builder")}
        />
      ) : activeTab === "builder" ? (
        bundles.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-border px-6 py-12 text-center text-sm text-muted">
            No policy bundles found. Create one to get started.
          </div>
        ) : (
          <VisualRuleBuilder bundleId={activeBundleId} />
        )
      ) : activeTab === "editor" ? (
        bundles.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-border px-6 py-12 text-center text-sm text-muted">
            No policy bundles found. Create one to get started.
          </div>
        ) : (
          <div className="space-y-4">
            <PolicyYamlEditor bundleId={activeBundleId} />
          </div>
        )
      ) : activeTab === "simulator" ? (
        bundles.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-border px-6 py-12 text-center text-sm text-muted">
            No policy bundles found. Create one to simulate policy checks.
          </div>
        ) : (
          <div className="space-y-8">
            <PolicySimulator bundleId={activeBundleId} />
            <PolicyReplay bundleId={activeBundleId} />
          </div>
        )
      ) : activeTab === "snapshots" ? (
        <div className="space-y-8">
          {activeBundleId && (
            <PublishControls
              bundleId={activeBundleId}
              ruleCount={bundles.find((b) => b.id === activeBundleId)?.rules.length ?? 0}
            />
          )}
          <SnapshotComparison />
        </div>
      ) : null}
    </div>
  );
}
