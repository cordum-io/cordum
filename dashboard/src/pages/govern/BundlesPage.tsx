import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { AlertTriangle } from "lucide-react";
import { GitBranch, ShieldCheck, FileEdit } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { MetricValue } from "@/components/ui/MetricValue";
import { BundleList } from "@/components/policy/bundles/BundleList";
import { usePolicyBundles } from "@/hooks/usePolicies";
import { usePolicyAccess } from "@/hooks/usePolicyAccess";
import { encodePolicyBundleId } from "@/hooks/usePolicies";

export const BUNDLES_PAGE_SECTIONS = [
  "bundle-summary-cards",
  "bundle-list",
] as const;

export function getBundleStatusVariant(status?: string): "healthy" | "warning" | "muted" {
  const normalized = (status ?? "").toLowerCase();
  if (normalized === "published") return "healthy";
  if (normalized === "draft") return "warning";
  return "muted";
}

export function getBundleAffordances(canPublish: boolean) {
  return {
    canManageBundle: canPublish,
    canViewBundle: true,
    actionLabel: canPublish ? "Manage bundle" : "View bundle",
  };
}

export default function BundlesPage() {
  const navigate = useNavigate();
  const policyAccess = usePolicyAccess();
  const { data, isLoading, isError, error, refetch } = usePolicyBundles();

  const bundles = useMemo(() => data?.items ?? [], [data]);
  const sortedBundles = useMemo(
    () => [...bundles].sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id)),
    [bundles],
  );

  const publishedCount = useMemo(
    () => bundles.filter((b) => (b.status ?? "").toLowerCase() === "published").length,
    [bundles],
  );
  const draftCount = useMemo(
    () => bundles.filter((b) => (b.status ?? "").toLowerCase() === "draft").length,
    [bundles],
  );

  return (
    <div className="space-y-6">
      <PageHeader
        label="Govern"
        title="Bundles"
        subtitle="Policy bundle inventory. Select a bundle to view YAML, diff, snapshots, and manage publish lifecycle."
        actions={
          <StatusBadge variant={policyAccess.canPublish ? "healthy" : "muted"}>
            {policyAccess.canPublish ? "publish access" : "publish restricted"}
          </StatusBadge>
        }
      />

      {isLoading && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
        </div>
      )}

      {isError && (
        <EmptyState
          icon={<AlertTriangle className="w-6 h-6" />}
          title="Unable to load policy bundles"
          description={error instanceof Error ? error.message : "An unexpected error occurred while loading policy bundle data."}
          action={
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              Retry
            </Button>
          }
        />
      )}

      {!isLoading && !isError && (
        <>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <InstrumentCard>
              <MetricValue label="Total bundles" value={bundles.length} icon={<GitBranch className="w-4 h-4" />} />
            </InstrumentCard>
            <InstrumentCard>
              <MetricValue label="Published" value={publishedCount} icon={<ShieldCheck className="w-4 h-4" />} />
            </InstrumentCard>
            <InstrumentCard>
              <MetricValue label="Draft" value={draftCount} icon={<FileEdit className="w-4 h-4" />} />
            </InstrumentCard>
          </div>

          <BundleList
            bundles={sortedBundles}
            canPublish={policyAccess.canPublish}
            onOpenBundle={(bundleId) =>
              navigate(`/govern/bundles/${encodeURIComponent(encodePolicyBundleId(bundleId))}`)
            }
          />
        </>
      )}
    </div>
  );
}
