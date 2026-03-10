import { useCallback, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Shield, Layers, History, Clock, Loader, CheckCircle, FileCheck, Info, ThumbsUp, ThumbsDown } from "lucide-react";
import { PieChart, Pie, Cell, Tooltip, Legend, ResponsiveContainer } from "recharts";
import { get, put } from "../api/client";
import { usePolicySnapshots, usePolicyAudit, usePublishPolicy, usePolicyApprovals, type PolicyAuditEntry, encodePolicyBundleId } from "../hooks/usePolicies";
import { usePolicyBundleContext } from "../components/policy/PolicyBundleContext";
import { SecurityControls } from "../components/policy/SecurityControls";
import { MetricCard } from "../components/MetricCard";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { cn } from "../lib/utils";
import type { PolicyBundle } from "../api/types";
import { usePageTitle } from "../hooks/usePageTitle";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const healthColors: Record<string, string> = {
  healthy: "bg-success",
  degraded: "bg-warning",
  unhealthy: "bg-danger",
};

function HealthDot({ status }: { status?: string }) {
  const color = healthColors[status ?? ""] ?? "bg-muted";
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
      <span className={cn("inline-block h-2 w-2 rounded-full", color)} />
      {status ?? "unknown"}
    </span>
  );
}

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
// Governance score
// ---------------------------------------------------------------------------

function computeGovernanceScore(
  bundles: PolicyBundle[],
  auditEntries: PolicyAuditEntry[],
  snapshotCount: number,
  latestPublishMs: number | null,
): number {
  if (bundles.length === 0) return 0;

  // 40% — enabled ratio
  const enabledCount = bundles.filter((b) => b.enabled !== false).length;
  const enabledScore = (enabledCount / bundles.length) * 100;

  // 30% — violation rate (lower = better)
  const now = Date.now();
  const oneDayAgo = now - 24 * 60 * 60 * 1000;
  const recent = auditEntries.filter(
    (e) => e.timestamp && new Date(e.timestamp).getTime() > oneDayAgo,
  );
  const violations = recent.filter((e) =>
    ["deny", "throttle"].includes(e.action),
  );
  const violationRate =
    recent.length > 0 ? violations.length / recent.length : 0;
  const violationScore = (1 - violationRate) * 100;

  // 30% — freshness
  let freshnessScore = 20;
  if (latestPublishMs !== null) {
    const ageSec = (now - latestPublishMs) / 1000;
    if (ageSec < 3600) freshnessScore = 100;
    else if (ageSec < 86400) freshnessScore = 80;
    else if (ageSec < 604800) freshnessScore = 50;
    else freshnessScore = 20;
  } else if (snapshotCount === 0) {
    freshnessScore = 10;
  }

  const score = enabledScore * 0.4 + violationScore * 0.3 + freshnessScore * 0.3;
  return Math.round(Math.max(0, Math.min(100, score)));
}

function scoreColor(score: number): string {
  if (score > 80) return "text-success";
  if (score >= 50) return "text-warning";
  return "text-danger";
}

function scoreRingColor(score: number): string {
  if (score > 80) return "#1f7a57";
  if (score >= 50) return "#c58a1c";
  return "#b83a3a";
}

// ---------------------------------------------------------------------------
// Governance Score Card
// ---------------------------------------------------------------------------

function GovernanceScoreCard({
  score,
  enabledCount,
  totalBundles,
  violationCount24h,
  lastPublish,
}: {
  score: number;
  enabledCount: number;
  totalBundles: number;
  violationCount24h: number;
  lastPublish: string | null;
}) {
  const circumference = 2 * Math.PI * 45;
  const strokeDash = (score / 100) * circumference;
  const ringColor = scoreRingColor(score);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Governance Health</CardTitle>
      </CardHeader>
      <div className="flex items-center gap-6 px-1 pb-2">
        {/* SVG ring */}
        <div className="relative h-28 w-28 shrink-0">
          <svg viewBox="0 0 100 100" className="h-full w-full -rotate-90">
            <circle
              cx="50" cy="50" r="45"
              fill="none"
              stroke="currentColor"
              strokeWidth="8"
              className="text-surface2"
            />
            <circle
              cx="50" cy="50" r="45"
              fill="none"
              stroke={ringColor}
              strokeWidth="8"
              strokeLinecap="round"
              strokeDasharray={`${strokeDash} ${circumference}`}
            />
          </svg>
          <span
            className={cn(
              "absolute inset-0 flex items-center justify-center text-2xl font-bold",
              scoreColor(score),
            )}
          >
            {score}
          </span>
        </div>
        {/* Sub-metrics */}
        <div className="space-y-1.5 text-xs text-muted-foreground">
          <p>Bundles: {enabledCount}/{totalBundles} enabled</p>
          <p>Violations: {violationCount24h} in 24h</p>
          <p>Last publish: {lastPublish ?? "Never"}</p>
        </div>
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Decision Distribution Chart
// ---------------------------------------------------------------------------

const DECISION_COLORS: Record<string, string> = {
  allow: "#1f7a57",
  deny: "#b83a3a",
  require_approval: "#c58a1c",
  throttle: "#0f7f7a",
};

const DECISION_LABELS: Record<string, string> = {
  allow: "Allow",
  deny: "Deny",
  require_approval: "Approval Required",
  throttle: "Throttle",
};

function DecisionDistributionChart({
  auditEntries,
}: {
  auditEntries: PolicyAuditEntry[];
}) {
  const data = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const entry of auditEntries) {
      const action = entry.action || "unknown";
      counts[action] = (counts[action] ?? 0) + 1;
    }
    return Object.entries(counts).map(([name, value]) => ({
      name: DECISION_LABELS[name] ?? name,
      value,
      color: DECISION_COLORS[name] ?? "#5a6a70",
    }));
  }, [auditEntries]);

  if (data.length === 0) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Decision Distribution</CardTitle>
        </CardHeader>
        <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
          No decision data available
        </div>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Decision Distribution</CardTitle>
      </CardHeader>
      <ResponsiveContainer width="100%" height={220}>
        <PieChart>
          <Pie
            data={data}
            cx="50%"
            cy="50%"
            innerRadius={50}
            outerRadius={80}
            paddingAngle={2}
            dataKey="value"
          >
            {data.map((entry, i) => (
              <Cell key={i} fill={entry.color} />
            ))}
          </Pie>
          <Tooltip
            formatter={(value: number, name: string) => {
              const total = data.reduce((s, d) => s + d.value, 0);
              const pct = total > 0 ? ((value / total) * 100).toFixed(1) : "0";
              return [`${value} (${pct}%)`, name];
            }}
          />
          <Legend />
        </PieChart>
      </ResponsiveContainer>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Recent Violations List
// ---------------------------------------------------------------------------

const ACTION_BADGE_VARIANT: Record<string, "danger" | "info" | "warning"> = {
  deny: "danger",
  throttle: "info",
  require_approval: "warning",
};

function RecentViolationsList({
  auditEntries,
}: {
  auditEntries: PolicyAuditEntry[];
}) {
  const violations = useMemo(() => {
    return auditEntries
      .filter((e) => e.action !== "allow")
      .sort((a, b) => {
        const ta = a.timestamp ? new Date(a.timestamp).getTime() : 0;
        const tb = b.timestamp ? new Date(b.timestamp).getTime() : 0;
        return tb - ta;
      })
      .slice(0, 10);
  }, [auditEntries]);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Recent Violations</CardTitle>
      </CardHeader>
      {violations.length === 0 ? (
        <div className="flex items-center gap-2 px-4 pb-4 text-sm text-success">
          <CheckCircle className="h-4 w-4" />
          No recent violations
        </div>
      ) : (
        <div className="space-y-0 divide-y divide-border">
          {violations.map((v) => (
            <div key={v.id} className="flex items-center gap-3 px-4 py-2.5 text-xs">
              <span className="shrink-0 text-muted-foreground">
                {v.timestamp ? timeAgo(v.timestamp) : "\u2014"}
              </span>
              <Badge variant={ACTION_BADGE_VARIANT[v.action] ?? "default"}>
                {v.action}
              </Badge>
              <span className="truncate text-ink">{v.actor || "\u2014"}</span>
              <span className="ml-auto truncate text-muted-foreground">{v.bundleId}</span>
            </div>
          ))}
          <div className="px-4 py-3">
            <Link
              to="/policies/history"
              className="text-xs font-medium text-accent hover:underline"
            >
              View All
            </Link>
          </div>
        </div>
      )}
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Toggle mutation
// ---------------------------------------------------------------------------

function useToggleBundle() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { id: string; enabled: boolean }>({
    mutationFn: async ({ id, enabled }) => {
      const safeId = encodePolicyBundleId(id);
      const detail = await get<{ content?: string }>(`/policy/bundles/${safeId}`);
      const content = (detail.content ?? "").trim();
      if (!content) throw new Error("bundle content is required");
      await put<void>(`/policy/bundles/${safeId}`, { content, enabled });
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
    <Card className="cursor-pointer hover:shadow-lift transition-shadow" onClick={onClick}>
      <CardHeader>
        <CardTitle>{bundle.name}</CardTitle>
        <Badge variant={isEnabled ? "success" : "default"}>
          {isEnabled ? "Enabled" : "Disabled"}
        </Badge>
      </CardHeader>
      <div className="space-y-3">
        <div className="flex items-center gap-4 text-sm">
          <span className="text-muted-foreground">
            {bundle.rules.length} rule{bundle.rules.length !== 1 ? "s" : ""}
          </span>
          <Badge variant="info">v{bundle.version ?? "\u2014"}</Badge>
        </div>
        <HealthDot status={bundle.healthStatus} />
        <div className="text-xs text-muted-foreground">
          {bundle.publishedAt ? `Published ${timeAgo(bundle.publishedAt)}` : "Not yet published"}
        </div>
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
                "pointer-events-none inline-block h-5 w-5 rounded-full bg-card shadow-sm transition-transform duration-200",
                isEnabled ? "translate-x-5" : "translate-x-0",
              )}
            />
          </button>
          {!canToggle && (
            <p className="mt-1 text-[10px] text-muted-foreground">
              Managed by pack — edit via YAML only.
            </p>
          )}
        </div>
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Pending Policy Changes
// ---------------------------------------------------------------------------

function PendingPolicyChanges({
  onNavigateToBundle,
}: {
  onNavigateToBundle: (bundleId: string) => void;
}) {
  const { pending: pendingBundles } = usePolicyApprovals();
  const publishPolicy = usePublishPolicy();
  const [rejectingId, setRejectingId] = useState<string | null>(null);
  const [publishedIds, setPublishedIds] = useState<Set<string>>(new Set());

  const handleApprove = useCallback(
    (bundleId: string) => {
      publishPolicy.mutate(
        { bundleId },
        {
          onSuccess: () => {
            setPublishedIds((prev) => new Set([...prev, bundleId]));
          },
        },
      );
    },
    [publishPolicy],
  );

  const handleReject = useCallback(
    (bundleId: string) => {
      // No dedicated reject endpoint — navigate to the bundle for manual review
      setRejectingId(null);
      onNavigateToBundle(bundleId);
    },
    [onNavigateToBundle],
  );

  // Filter out just-approved bundles
  const visiblePending = pendingBundles.filter((p) => !publishedIds.has(p.bundle.id));

  if (visiblePending.length === 0) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Pending Policy Changes</CardTitle>
        </CardHeader>
        <div className="flex items-center gap-2 px-4 pb-4 text-sm text-muted-foreground">
          <Info className="h-4 w-4 shrink-0" />
          <span>
            No pending changes. Policy updates are published directly via the{" "}
            <Link to="/policies/builder" className="font-medium text-accent hover:underline">
              Policy Builder
            </Link>.
          </span>
        </div>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          <FileCheck className="h-4 w-4 text-warning" />
          <CardTitle>Pending Policy Changes</CardTitle>
        </div>
        <Badge variant="warning">{visiblePending.length} pending</Badge>
      </CardHeader>
      <div className="space-y-0 divide-y divide-border">
        {visiblePending.map(({ bundle, changeSummary }) => (
          <div key={bundle.id} className="flex items-center gap-3 px-4 py-3">
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="truncate text-sm font-medium text-ink">{bundle.name}</span>
                <Badge variant="info">v{bundle.version ?? "draft"}</Badge>
              </div>
              <div className="mt-0.5 flex items-center gap-2 text-xs text-muted-foreground">
                {bundle.author && <span>by {bundle.author}</span>}
                {bundle.updatedAt && <span>{timeAgo(bundle.updatedAt)}</span>}
                <span>&middot; {changeSummary}</span>
              </div>
            </div>
            <div className="flex items-center gap-1.5 shrink-0">
              <Button
                size="sm"
                onClick={(e) => {
                  e.stopPropagation();
                  handleApprove(bundle.id);
                }}
                disabled={publishPolicy.isPending}
              >
                <ThumbsUp className="h-3.5 w-3.5" />
                Approve
              </Button>
              <Button
                variant="danger"
                size="sm"
                onClick={(e) => {
                  e.stopPropagation();
                  if (rejectingId === bundle.id) {
                    handleReject(bundle.id);
                  } else {
                    setRejectingId(bundle.id);
                  }
                }}
              >
                <ThumbsDown className="h-3.5 w-3.5" />
                {rejectingId === bundle.id ? "Review & Reject" : "Reject"}
              </Button>
            </div>
          </div>
        ))}
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Overview page
// ---------------------------------------------------------------------------

export default function PoliciesOverviewPage() {
  usePageTitle("Policies");
  const navigate = useNavigate();
  const { bundles, isLoading, isError } = usePolicyBundleContext();
  const { data: snapshotsData } = usePolicySnapshots();
  const { data: auditData } = usePolicyAudit();
  const toggleBundle = useToggleBundle();

  const auditEntries = auditData?.items ?? [];
  const snapshotCount = snapshotsData?.items?.length ?? 0;

  const handleToggle = useCallback(
    (id: string, enabled: boolean) => {
      toggleBundle.mutate({ id, enabled });
    },
    [toggleBundle],
  );

  // Compute governance score
  const latestPublishMs = useMemo(() => {
    const dates = bundles
      .filter((b) => b.publishedAt)
      .map((b) => new Date(b.publishedAt!).getTime());
    return dates.length > 0 ? Math.max(...dates) : null;
  }, [bundles]);

  const governanceScore = useMemo(
    () => computeGovernanceScore(bundles, auditEntries, snapshotCount, latestPublishMs),
    [bundles, auditEntries, snapshotCount, latestPublishMs],
  );

  const enabledCount = bundles.filter((b) => b.enabled !== false).length;

  const violationCount24h = useMemo(() => {
    const cutoff = Date.now() - 24 * 60 * 60 * 1000;
    return auditEntries.filter(
      (e) =>
        ["deny", "throttle"].includes(e.action) &&
        e.timestamp &&
        new Date(e.timestamp).getTime() > cutoff,
    ).length;
  }, [auditEntries]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-16 text-sm text-muted-foreground">
        <Loader className="mr-2 h-4 w-4 animate-spin" />
        Loading policy bundles...
      </div>
    );
  }

  if (isError) {
    return (
      <div className="py-16 text-center text-sm text-danger">
        Failed to load policy bundles.
      </div>
    );
  }

  if (bundles.length === 0) {
    return (
      <div className="rounded-2xl border border-dashed border-border px-6 py-12 text-center text-sm text-muted-foreground">
        No policy bundles configured
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Top row: Governance Score + Decision Distribution */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-5">
        <div className="lg:col-span-2">
          <GovernanceScoreCard
            score={governanceScore}
            enabledCount={enabledCount}
            totalBundles={bundles.length}
            violationCount24h={violationCount24h}
            lastPublish={latestPublishMs ? timeAgo(new Date(latestPublishMs).toISOString()) : null}
          />
        </div>
        <div className="lg:col-span-3">
          <DecisionDistributionChart auditEntries={auditEntries} />
        </div>
      </div>

      {/* Metrics row */}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <MetricCard
          title="Total Rules"
          value={bundles.reduce((sum, b) => sum + b.rules.length, 0)}
          icon={<Shield className="h-5 w-5 text-muted-foreground" />}
        />
        <MetricCard
          title="Active Bundles"
          value={enabledCount}
          detail={`${bundles.length} total`}
          icon={<Layers className="h-5 w-5 text-muted-foreground" />}
        />
        <MetricCard
          title="Snapshots"
          value={snapshotCount}
          icon={<History className="h-5 w-5 text-muted-foreground" />}
        />
        <MetricCard
          title="Last Published"
          value={latestPublishMs ? timeAgo(new Date(latestPublishMs).toISOString()) : "Never"}
          icon={<Clock className="h-5 w-5 text-muted-foreground" />}
        />
      </div>

      {/* Pending policy changes */}
      <PendingPolicyChanges
        onNavigateToBundle={(id) => navigate(`/policies/rules?bundle=${id}`)}
      />

      {/* Security controls */}
      <SecurityControls />

      {/* Recent violations */}
      <RecentViolationsList auditEntries={auditEntries} />

      {/* Bundle cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {bundles.map((bundle: PolicyBundle) => (
          <BundleCard
            key={bundle.id}
            bundle={bundle}
            onToggle={handleToggle}
            toggling={toggleBundle.isPending}
            onClick={() => navigate(`/policies/rules?bundle=${bundle.id}`)}
          />
        ))}
      </div>
    </div>
  );
}
