/*
 * DESIGN: "Control Surface" — Environments
 * Truthful inventory view derived from persisted config.
 */
import { motion } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { Globe, Copy, Server, ExternalLink } from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import { useEnvironments } from "@/hooks/useSettings";

function formatDateTime(raw?: string): string {
  if (!raw) return "Not recorded";
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return raw;
  return parsed.toLocaleString();
}

function statusVariant(status: "active" | "maintenance" | "degraded") {
  switch (status) {
    case "active":
      return "healthy" as const;
    case "maintenance":
      return "warning" as const;
    case "degraded":
      return "danger" as const;
  }
}

export default function SettingsEnvironmentsPage() {
  const { data: envs, isLoading, isError, error, refetch } = useEnvironments();

  if (isError) {
    return (
      <ErrorBanner
        title="Unable to load environment inventory"
        message={error instanceof Error ? error.message : "Failed to load environment config"}
        onRetry={() => {
          void refetch();
        }}
      />
    );
  }

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader
        title="Environments"
        subtitle="Read-only inventory derived from the saved deployment config, not a separate runtime environment service."
      />

      <InfoBanner variant="info" title="Config-backed inventory">
        This page reflects the environments stored in system config. It does not create, promote, or deploy environments from the dashboard.
      </InfoBanner>

      {isLoading ? (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          {Array.from({ length: 2 }).map((_, i) => <SkeletonCard key={i} />)}
        </div>
      ) : (envs ?? []).length === 0 ? (
        <EmptyState
          icon={<Server className="w-8 h-8" />}
          title="No configured environments"
          description="This deployment has no environments saved in system config, so the dashboard does not invent a default production environment."
        />
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          {(envs ?? []).map((env, i) => (
            <motion.div
              key={env.id}
              initial={{ opacity: 0, y: 8 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: i * 0.05 }}
              className={cn(
                "instrument-card",
                env.status === "active" && "status-healthy",
                env.status === "degraded" && "status-danger",
              )}
            >
              <div className="flex items-start justify-between mb-3">
                <div className="flex items-center gap-2">
                  <Server className="w-4 h-4 text-cordum" />
                  <span className="text-sm font-display font-semibold text-foreground">{env.name}</span>
                </div>
                <StatusBadge variant={statusVariant(env.status)} dot>{env.status}</StatusBadge>
              </div>
              <div className="space-y-2 mb-4">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Environment ID</span>
                  <span className="text-xs font-mono text-foreground">{env.id}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Endpoint</span>
                  <div className="flex items-center gap-1">
                    <span className="max-w-[14rem] truncate text-xs font-mono text-foreground">
                      {env.endpoint || "Not configured"}
                    </span>
                    {env.endpoint && (
                      <button
                        type="button"
                        onClick={() => {
                          void navigator.clipboard.writeText(env.endpoint!);
                          toast.success("Endpoint copied");
                        }}
                        className="p-0.5 rounded hover:bg-surface-2"
                        aria-label={`Copy endpoint for ${env.name}`}
                      >
                        <Copy className="w-3 h-3 text-muted-foreground" />
                      </button>
                    )}
                  </div>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Last deployed</span>
                  <span className="text-xs text-foreground">{formatDateTime(env.lastDeployedAt)}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Last promoted</span>
                  <span className="text-xs text-foreground">{formatDateTime(env.lastPromotedAt)}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-xs font-mono text-muted-foreground uppercase tracking-wider">Config entries</span>
                  <span className="text-xs text-foreground">{Object.keys(env.config ?? {}).length}</span>
                </div>
              </div>
              <div className="flex gap-2 pt-3 border-t border-border">
                {env.endpoint ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => window.open(env.endpoint, "_blank", "noopener,noreferrer")}
                  >
                    <ExternalLink className="w-3 h-3" />
                  </Button>
                ) : (
                  <div className="text-xs text-muted-foreground flex items-center gap-2">
                    <Globe className="w-3 h-3" />
                    No external endpoint published
                  </div>
                )}
              </div>
            </motion.div>
          ))}
        </div>
      )}
    </motion.div>
  );
}
