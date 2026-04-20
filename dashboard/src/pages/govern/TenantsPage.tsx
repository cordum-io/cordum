import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { Users, ListChecks, Server } from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { MetricValue } from "@/components/ui/MetricValue";
import { TenantList, type TenantSummaryCard } from "@/components/policy/tenants/TenantList";
import { usePolicyAccess } from "@/hooks/usePolicyAccess";
import { usePolicyStudioGlobal } from "@/hooks/usePolicyStudioGlobal";

export const TENANTS_PAGE_SECTIONS = [
  "tenant-bundle-select",
  "tenant-summary-cards",
  "tenant-list",
] as const;

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function toStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((entry) => (typeof entry === "string" ? entry.trim() : ""))
    .filter(Boolean);
}

function readNumber(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim()) {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return undefined;
}

export function parseTenantSummaries(
  sourceRoot: Record<string, unknown>,
): TenantSummaryCard[] {
  const tenantsRaw = asRecord(sourceRoot.tenants);
  const summaries: TenantSummaryCard[] = [];

  for (const [tenantIdRaw, tenantRaw] of Object.entries(tenantsRaw)) {
    const tenantId = tenantIdRaw.trim();
    if (!tenantId) continue;
    const tenant = asRecord(tenantRaw);
    const mcp = asRecord(tenant.mcp);
    const label =
      (typeof tenant.label === "string" && tenant.label.trim()) ||
      (typeof tenant.name === "string" && tenant.name.trim()) ||
      tenantId;
    const allowTopics = toStringArray(tenant.allow_topics);
    const mcpServerIds = new Set([
      ...toStringArray(mcp.allow_servers),
      ...toStringArray(mcp.deny_servers),
    ]);

    summaries.push({
      id: tenantId,
      label,
      allowTopicsCount: allowTopics.length,
      mcpServersCount: mcpServerIds.size,
      maxConcurrentJobs: readNumber(tenant.max_concurrent_jobs),
    });
  }

  return summaries.sort((a, b) => a.id.localeCompare(b.id));
}

export default function TenantsPage({
  hideHeader,
}: {
  hideHeader?: boolean;
} = {}) {
  const navigate = useNavigate();
  const policyAccess = usePolicyAccess();
  const canManageTenants = policyAccess.canManageTenants;
  const {
    bundles,
    selectedBundleId,
    setSelectedBundleId,
    policy,
    isLoading,
    loadError,
    refetchBundles,
    refetchSelectedBundle,
  } = usePolicyStudioGlobal();
  const tenantSummaries = useMemo(
    () => parseTenantSummaries(policy.sourceRoot),
    [policy.sourceRoot],
  );

  if (isLoading && bundles.length === 0) {
    return (
      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <SkeletonCard />
        <SkeletonCard />
        <SkeletonCard />
      </div>
    );
  }

  if (!isLoading && bundles.length === 0) {
    return (
      <EmptyState
        title="No policy bundles found"
        description="Create or sync a policy bundle before managing tenant access boundaries."
        action={<Button variant="outline" size="sm" onClick={() => navigate("/govern/overview?tab=bundles")}>View bundles</Button>}
      />
    );
  }

  return (
    <div className="space-y-6">
      {!hideHeader && (
        <PageHeader
          label="Govern"
          title="Scope & rollout"
          subtitle="Inspect tenant boundaries, MCP governance posture, and bundle-backed rollout state from one policy surface."
          actions={
            <div className="flex items-center gap-2">
              <StatusBadge variant={canManageTenants ? "healthy" : "muted"}>
                {canManageTenants ? "editor access" : "read-only role"}
              </StatusBadge>
            </div>
          }
        />
      )}

      {hideHeader && (
        <div className="flex items-center justify-end">
          <StatusBadge variant={canManageTenants ? "healthy" : "muted"}>
            {canManageTenants ? "editor access" : "read-only role"}
          </StatusBadge>
        </div>
      )}

      <InfoBanner variant="cordum">
        Tenant policies are bundle-backed. Changes apply through existing policy bundle save paths and preserve source YAML compatibility.
      </InfoBanner>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <label className="text-xs text-muted-foreground">
          Bundle
          <select
            id="govern-tenant-bundle-select"
            className="ml-2 h-8 rounded-2xl border border-border bg-surface-2 px-2 text-xs text-foreground"
            value={selectedBundleId}
            onChange={(event) => setSelectedBundleId(event.target.value)}
          >
            {bundles.map((bundle) => (
              <option key={bundle.id} value={bundle.id}>
                {bundle.name || bundle.id}
              </option>
            ))}
          </select>
        </label>
      </div>

      {loadError && (
        <InfoBanner
          variant="error"
          title="Unable to load tenant policy data"
        >
          <p>{loadError.message}</p>
          {loadError.details && <p className="mt-1 text-destructive">{loadError.details}</p>}
          <Button
            variant="outline"
            size="sm"
            className="mt-2"
            onClick={() => {
              void refetchBundles();
              if (selectedBundleId) {
                void refetchSelectedBundle();
              }
            }}
          >
            Retry load
          </Button>
        </InfoBanner>
      )}

      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <InstrumentCard>
          <MetricValue label="tenant entries" value={tenantSummaries.length} icon={<Users className="w-4 h-4" />} />
        </InstrumentCard>
        <InstrumentCard>
          <MetricValue
            label="total allow_topics"
            value={tenantSummaries.reduce((total, tenant) => total + tenant.allowTopicsCount, 0)}
            icon={<ListChecks className="w-4 h-4" />}
          />
        </InstrumentCard>
        <InstrumentCard>
          <MetricValue
            label="configured mcp servers"
            value={tenantSummaries.reduce((total, tenant) => total + tenant.mcpServersCount, 0)}
            icon={<Server className="w-4 h-4" />}
          />
        </InstrumentCard>
      </div>

      <TenantList
        tenants={tenantSummaries}
        canEdit={canManageTenants}
        onOpenTenant={(tenantId) => {
          const params = selectedBundleId
            ? `?bundle=${encodeURIComponent(selectedBundleId)}`
            : "";
          navigate(`/govern/tenants/${encodeURIComponent(tenantId)}${params}`);
        }}
      />
    </div>
  );
}
