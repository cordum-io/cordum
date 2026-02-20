import { Users, Activity, Database, Clock } from "lucide-react";
import { useStatus } from "../hooks/useStatus";
import { useWorkers } from "../hooks/useWorkers";
import { MetricCard } from "../components/MetricCard";
import { Card } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { SafetyDecisionFeed } from "../components/home/SafetyDecisionFeed";
import { JobPipelineFunnel } from "../components/home/JobPipelineFunnel";
import { PoolUtilizationGrid } from "../components/home/PoolUtilizationGrid";
import { EventTimeline } from "../components/home/EventTimeline";
import { ActiveWorkflowCards } from "../components/home/ActiveWorkflowCards";
import { DLQSummary } from "../components/home/DLQSummary";
import { CircuitBreakerCard } from "../components/home/CircuitBreakerCard";
import { RateLimiterModeBadge } from "../components/home/RateLimiterModeBadge";
import { QuickActions } from "../components/home/QuickActions";
import { usePageTitle } from "../hooks/usePageTitle";

// ---------------------------------------------------------------------------
// Skeleton cards
// ---------------------------------------------------------------------------

function SkeletonGrid() {
  return (
    <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
      {Array.from({ length: 5 }, (_, i) => (
        <Card key={i} className="animate-pulse">
          <div className="space-y-3">
            <div className="h-4 w-1/2 rounded bg-surface2" />
            <div className="h-8 w-2/3 rounded bg-surface2" />
          </div>
        </Card>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Icon wrapper
// ---------------------------------------------------------------------------

const iconClass = "h-5 w-5 text-muted";

function formatUptime(seconds?: number): string {
  if (seconds == null) return "\u2014";
  if (seconds < 60) return `${seconds}s`;
  const mins = Math.floor(seconds / 60);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ${mins % 60}m`;
  const days = Math.floor(hrs / 24);
  return `${days}d ${hrs % 24}h`;
}

// ---------------------------------------------------------------------------
// HomePage
// ---------------------------------------------------------------------------

export default function HomePage() {
  usePageTitle("Overview");
  const { data: status, isLoading, isError, refetch } = useStatus();
  const { data: workers } = useWorkers();
  const workerCount = workers?.length ?? status?.workers?.count ?? 0;
  const activeJobs = workers?.reduce((sum, w) => sum + (w.activeJobs ?? 0), 0) ?? 0;
  const natsConnected = status?.nats?.connected;
  const redisOk = status?.redis?.ok;

  return (
    <div className="space-y-6">
      <h1 className="font-display text-2xl font-bold text-ink">
        Command Center
      </h1>

      {isLoading && <SkeletonGrid />}

      {!isLoading && isError && (
        <Card>
          <div className="flex items-center justify-between py-6">
            <p className="text-sm text-muted">
              Failed to load system status.
            </p>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              Retry
            </Button>
          </div>
        </Card>
      )}

      {!isLoading && !isError && status && (
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
          <MetricCard
            title="Workers"
            value={workerCount}
            detail="online"
            icon={<Users className={iconClass} />}
          />
          <MetricCard
            title="Active Jobs"
            value={activeJobs}
            icon={<Activity className={iconClass} />}
          />
          <MetricCard
            title="NATS"
            value={natsConnected ? "Connected" : "Disconnected"}
            icon={<Activity className={iconClass} />}
          />
          <MetricCard
            title="Redis"
            value={redisOk ? "OK" : "Degraded"}
            icon={<Database className={iconClass} />}
          />
          <MetricCard
            title="Uptime"
            value={formatUptime(status.uptime_seconds)}
            icon={<Clock className={iconClass} />}
          />
        </div>
      )}

      {/* Circuit breaker status (hidden when HA fields absent) */}
      <CircuitBreakerCard circuitBreakers={status?.circuit_breakers} />

      <RateLimiterModeBadge mode={status?.rate_limiter?.mode} />

      {/* Quick actions */}
      <QuickActions />

      {/* Two-column: Safety Feed + Pipeline Funnel */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <SafetyDecisionFeed />
        <JobPipelineFunnel />
      </div>

      {/* Pool utilization heatmap */}
      <PoolUtilizationGrid />

      {/* DLQ / Error summary */}
      <DLQSummary />

      {/* Two-column: Event Timeline + Active Workflows */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <EventTimeline />
        <ActiveWorkflowCards />
      </div>
    </div>
  );
}
