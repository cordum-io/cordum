import { useMemo, useState } from "react";
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

export default function BundlesPage({ hideHeader }: { hideHeader?: boolean } = {}) {
  const navigate = useNavigate();
  const policyAccess = usePolicyAccess();
  const { data, isLoading, isError, error, refetch } = usePolicyBundles();

  const bundles = useMemo(() => data?.items ?? [], [data]);
  const [sortField, setSortField] = useState<"name" | "signed">("name");

  // classifySigned keeps the tri-state sort order deterministic:
  //   signed (0) → unsigned (1) → unknown (2)
  // Unsigned bundles are the remediation target in strict-mode deploys
  // so surfacing them near the top when sorting by signature is the
  // compliance-friendly default.
  const sortedBundles = useMemo(() => {
    const copy = [...bundles];
    if (sortField === "signed") {
      copy.sort((a, b) => {
        const as = a.signed === true ? 0 : a.signed === false ? 1 : 2;
        const bs = b.signed === true ? 0 : b.signed === false ? 1 : 2;
        if (as !== bs) return as - bs;
        return (a.name || a.id).localeCompare(b.name || b.id);
      });
    } else {
      copy.sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id));
    }
    return copy;
  }, [bundles, sortField]);

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
      {!hideHeader && (
        <PageHeader
          label="Govern"
          title="Bundles"
          subtitle="Policy bundle inventory. Select a bundle to view YAML, diff, snapshots, and manage publish lifecycle."
          actions={
            <div className="flex items-center gap-2">
              <StatusBadge variant={policyAccess.canPublish ? "healthy" : "muted"}>
                {policyAccess.canPublish ? "publish access" : "publish restricted"}
              </StatusBadge>
            </div>
          }
        />
      )}

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

          <div className="flex items-center justify-end">
            <label className="inline-flex items-center gap-2 text-xs text-muted-foreground">
              <span>Sort by</span>
              <select
                value={sortField}
                onChange={(e) => setSortField(e.target.value as "name" | "signed")}
                className="h-7 rounded-full border border-border bg-background px-3 text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cordum/40"
                aria-label="Sort bundles by"
                data-testid="bundles-sort-select"
              >
                <option value="name">Name</option>
                <option value="signed">Signature status</option>
              </select>
            </label>
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
