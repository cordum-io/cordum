import { motion } from "framer-motion";
import { Link, useNavigate } from "react-router-dom";
import {
  AlertTriangle,
  ArrowUpRight,
  Database,
  Hash,
  Package,
  Radio,
  RefreshCw,
  Users,
} from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { StatTile } from "@/components/ui/StatTile";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { useTopics } from "@/hooks/useTopics";
import type { TopicRegistration } from "@/api/types";
import { cn } from "@/lib/utils";

function runtimeStatus(topic: TopicRegistration): "active" | "degraded" {
  return topic.activeWorkers > 0 ? "active" : "degraded";
}

function runtimeStatusVariant(topic: TopicRegistration): BadgeVariant {
  return topic.activeWorkers > 0 ? "healthy" : "warning";
}

function registryStatusVariant(status: string): BadgeVariant {
  switch (status) {
    case "active":
      return "cordum";
    case "deprecated":
      return "warning";
    case "disabled":
      return "danger";
    default:
      return "muted";
  }
}

function detailLinkClass(disabled = false) {
  return cn(
    "inline-flex items-center gap-1 text-xs font-medium transition-colors",
    disabled
      ? "cursor-default text-muted-foreground"
      : "text-cordum hover:text-cordum/80",
  );
}

function TopicNameCell({ topic }: { topic: TopicRegistration }) {
  return (
    <div className="space-y-2">
      <div className="flex items-start gap-2">
        <div
          className={cn(
            "mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-xl border",
            topic.activeWorkers > 0
              ? "border-cordum/20 bg-cordum/10 text-cordum"
              : "border-amber-500/20 bg-amber-500/10 text-amber-400",
          )}
        >
          {topic.activeWorkers > 0 ? (
            <Radio className="h-3.5 w-3.5" />
          ) : (
            <AlertTriangle className="h-3.5 w-3.5" />
          )}
        </div>
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate text-sm font-semibold text-foreground">
              {topic.name}
            </span>
            {topic.activeWorkers === 0 && (
              <StatusBadge variant="warning" dot className="shrink-0">
                Degraded
              </StatusBadge>
            )}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            Routed to <span className="font-mono text-foreground">{topic.pool}</span>
          </p>
        </div>
      </div>

      {(topic.requires.length > 0 || topic.riskTags.length > 0) && (
        <div className="flex flex-wrap gap-1.5 pl-9">
          {topic.requires.map((requirement) => (
            <span
              key={`req-${topic.name}-${requirement}`}
              className="rounded-full bg-surface-2 px-2 py-0.5 text-[11px] font-mono text-muted-foreground"
            >
              req:{requirement}
            </span>
          ))}
          {topic.riskTags.map((riskTag) => (
            <StatusBadge
              key={`risk-${topic.name}-${riskTag}`}
              variant="warning"
              className="px-2 py-0.5 text-[11px] font-mono"
            >
              risk:{riskTag}
            </StatusBadge>
          ))}
        </div>
      )}
    </div>
  );
}

function ResourceLinkCell({
  href,
  value,
  icon: Icon,
  emptyLabel = "—",
}: {
  href?: string;
  value?: string;
  icon: typeof Database;
  emptyLabel?: string;
}) {
  if (!value || !href) {
    return <span className={detailLinkClass(true)}>{emptyLabel}</span>;
  }

  return (
    <Link to={href} className={detailLinkClass()}>
      <Icon className="h-3.5 w-3.5" />
      <span className="max-w-[11rem] truncate font-mono">{value}</span>
      <ArrowUpRight className="h-3.5 w-3.5" />
    </Link>
  );
}

export default function TopicsPage() {
  const navigate = useNavigate();
  const { data, isLoading, isError, error, isFetching, refetch } = useTopics();
  const topics = data?.items ?? [];

  const activeCount = topics.filter((topic) => topic.activeWorkers > 0).length;
  const degradedCount = topics.filter((topic) => topic.activeWorkers === 0).length;
  const sortedTopics = [...topics].sort((left, right) => {
    const leftRank = left.activeWorkers > 0 ? 0 : 1;
    const rightRank = right.activeWorkers > 0 ? 0 : 1;
    if (leftRank !== rightRank) return leftRank - rightRank;
    return left.name.localeCompare(right.name);
  });

  return (
    <motion.div
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      className="space-y-6"
    >
      <PageHeader
        label="Extend"
        title="Topics"
        subtitle="Track every registered topic, its pool mapping, linked schemas, owning pack, and runtime coverage."
        actions={(
          <Button
            variant="outline"
            size="sm"
            disabled={isFetching}
            aria-busy={isFetching}
            onClick={() => void refetch()}
          >
            <RefreshCw className={cn("mr-1 h-3 w-3", isFetching && "animate-spin")} />
            {isFetching ? "Refreshing" : "Refresh"}
          </Button>
        )}
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <StatTile
          label="Registered"
          value={isLoading ? "—" : topics.length}
          helperText="Topics known to the registry."
          icon={<Hash className="h-4 w-4 text-cordum" aria-hidden="true" />}
        />
        <StatTile
          label="Active"
          value={isLoading ? "—" : activeCount}
          helperText="Topics with live worker coverage."
          icon={<Radio className="h-4 w-4 text-emerald-400" aria-hidden="true" />}
          accent="healthy"
        />
        <StatTile
          label="Degraded"
          value={isLoading ? "—" : degradedCount}
          helperText="Known topics with zero active workers."
          icon={<AlertTriangle className="h-4 w-4 text-amber-400" aria-hidden="true" />}
          accent="warning"
        />
      </div>

      {isLoading ? (
        <div className="instrument-card overflow-hidden">
          <SkeletonTable rows={6} />
        </div>
      ) : isError ? (
        <div className="instrument-card">
          <ErrorBanner
            title="Topic registry unavailable"
            message={error instanceof Error ? error.message : "Failed to load topics"}
            onRetry={() => void refetch()}
          />
        </div>
      ) : sortedTopics.length === 0 ? (
        <div className="instrument-card">
          <EmptyState
            icon={<Hash className="h-5 w-5" />}
            title="No topics registered"
            description="Install a pack or use `cordumctl topic create` to register a topic before routing work through the control plane."
            action={(
              <Button variant="outline" size="sm" onClick={() => navigate("/packs")}>
                <Package className="mr-1 h-3 w-3" />
                Browse packs
              </Button>
            )}
          />
        </div>
      ) : (
        <div className="instrument-card overflow-hidden">
          <div className="flex items-center justify-between border-b border-border px-5 py-3">
            <div>
              <h2 className="text-sm font-display font-semibold text-foreground">
                Topic registry
              </h2>
              <p className="mt-1 text-xs text-muted-foreground">
                Degraded topics stay valid but need worker coverage restored.
              </p>
            </div>
            <StatusBadge
              variant={degradedCount > 0 ? "warning" : "healthy"}
              dot
            >
              {degradedCount > 0 ? `${degradedCount} degraded` : "All covered"}
            </StatusBadge>
          </div>

          <div className="overflow-x-auto">
            <table className="min-w-[980px] w-full">
              <thead>
                <tr className="border-b border-border bg-surface-0">
                  <th className="px-5 py-3 text-left text-xs font-mono font-medium uppercase tracking-widest text-muted-foreground">
                    Topic
                  </th>
                  <th className="px-5 py-3 text-left text-xs font-mono font-medium uppercase tracking-widest text-muted-foreground">
                    Pool
                  </th>
                  <th className="px-5 py-3 text-left text-xs font-mono font-medium uppercase tracking-widest text-muted-foreground">
                    Input schema
                  </th>
                  <th className="px-5 py-3 text-left text-xs font-mono font-medium uppercase tracking-widest text-muted-foreground">
                    Output schema
                  </th>
                  <th className="px-5 py-3 text-left text-xs font-mono font-medium uppercase tracking-widest text-muted-foreground">
                    Pack
                  </th>
                  <th className="px-5 py-3 text-left text-xs font-mono font-medium uppercase tracking-widest text-muted-foreground">
                    Active workers
                  </th>
                  <th className="px-5 py-3 text-left text-xs font-mono font-medium uppercase tracking-widest text-muted-foreground">
                    Status
                  </th>
                </tr>
              </thead>
              <tbody>
                {sortedTopics.map((topic, index) => (
                  <motion.tr
                    key={topic.name}
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    transition={{ delay: index * 0.025 }}
                    className="border-b border-border align-top transition-colors hover:bg-surface-1/70"
                  >
                    <td className="px-5 py-4">
                      <TopicNameCell topic={topic} />
                    </td>
                    <td className="px-5 py-4">
                      <div className="space-y-1">
                        <span className="inline-flex rounded-full bg-surface-2 px-2 py-1 text-xs font-mono text-foreground">
                          {topic.pool}
                        </span>
                        <p className="text-xs text-muted-foreground">
                          Worker routing pool
                        </p>
                      </div>
                    </td>
                    <td className="px-5 py-4">
                      <ResourceLinkCell
                        href={topic.inputSchemaId ? `/schemas/${encodeURIComponent(topic.inputSchemaId)}` : undefined}
                        value={topic.inputSchemaId}
                        icon={Database}
                      />
                    </td>
                    <td className="px-5 py-4">
                      <ResourceLinkCell
                        href={topic.outputSchemaId ? `/schemas/${encodeURIComponent(topic.outputSchemaId)}` : undefined}
                        value={topic.outputSchemaId}
                        icon={Database}
                      />
                    </td>
                    <td className="px-5 py-4">
                      <ResourceLinkCell
                        href={topic.packId ? `/packs/${encodeURIComponent(topic.packId)}` : undefined}
                        value={topic.packId}
                        icon={Package}
                      />
                    </td>
                    <td className="px-5 py-4">
                      <Link
                        to={`/agents?pool=${encodeURIComponent(topic.pool)}&topic=${encodeURIComponent(topic.name)}`}
                        className={detailLinkClass()}
                      >
                        <Users className="h-3.5 w-3.5" />
                        <span className="font-mono">
                          {topic.activeWorkers}
                        </span>
                        <span className="text-muted-foreground">
                          {topic.activeWorkers === 1 ? "worker" : "workers"}
                        </span>
                        <ArrowUpRight className="h-3.5 w-3.5" />
                      </Link>
                    </td>
                    <td className="px-5 py-4">
                      <div className="space-y-2">
                        <StatusBadge
                          variant={runtimeStatusVariant(topic)}
                          dot
                        >
                          {runtimeStatus(topic)}
                        </StatusBadge>
                        <StatusBadge
                          variant={registryStatusVariant(topic.status)}
                        >
                          registry:{topic.status}
                        </StatusBadge>
                      </div>
                    </td>
                  </motion.tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </motion.div>
  );
}
