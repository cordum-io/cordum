import { ArrowLeft, Save } from "lucide-react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { motion } from "framer-motion";
import { toast } from "sonner";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { TenantTagListEditor } from "@/components/policy/tenants/TenantTagListEditor";
import { TenantMcpMatrixEditor } from "@/components/policy/tenants/TenantMcpMatrixEditor";
import { TenantTopicAccessSection } from "@/components/policy/tenants/TenantTopicAccessSection";
import { TenantMcpGovernanceSection } from "@/components/policy/tenants/TenantMcpGovernanceSection";
import { TenantLimitsSection } from "@/components/policy/tenants/TenantLimitsSection";
import { TenantScopedRulesSection } from "@/components/policy/tenants/TenantScopedRulesSection";
import { usePolicyAccess } from "@/hooks/usePolicyAccess";
import { usePolicyStudioGlobal } from "@/hooks/usePolicyStudioGlobal";
import type { GlobalPolicyInputRule } from "@/types/policy";

export const TENANT_DETAIL_SECTIONS = [
  "topic-access-control",
  "mcp-governance",
  "limits",
  "tenant-scoped-rules",
] as const;

export function getTenantDetailAffordances(canManageTenants: boolean): {
  canEdit: boolean;
  showSave: boolean;
  showReadOnlyBanner: boolean;
} {
  return {
    canEdit: canManageTenants,
    showSave: canManageTenants,
    showReadOnlyBanner: !canManageTenants,
  };
}

export function filterTenantScopedRules(
  rules: GlobalPolicyInputRule[],
  tenantId: string,
): GlobalPolicyInputRule[] {
  const normalizedTenantId = tenantId.trim().toLowerCase();
  return rules.filter((rule) =>
    rule.match.tenants.some(
      (entry) => entry.trim().toLowerCase() === normalizedTenantId,
    ),
  );
}

export default function TenantDetailPage() {
  const navigate = useNavigate();
  const { id = "" } = useParams();
  const [searchParams] = useSearchParams();
  const initialBundleId = searchParams.get("bundle") ?? "";
  const tenantId = decodeURIComponent(id);
  const policyAccess = usePolicyAccess();
  const canManageTenants = policyAccess.canManageTenants;
  const affordances = getTenantDetailAffordances(canManageTenants);
  const {
    bundles,
    selectedBundleId,
    setSelectedBundleId,
    policy,
    tenantPolicies,
    setTenantPolicy,
    save,
    saveError,
    clearSaveError,
    isLoading,
    isDirty,
    isSaving,
    loadError,
    refetchBundles,
    refetchSelectedBundle,
  } = usePolicyStudioGlobal(initialBundleId);
  const tenant = tenantPolicies[tenantId];
  const scopedRules = filterTenantScopedRules(policy.rules, tenantId);

  const saveTenantChanges = async () => {
    const result = await save();
    if (result.ok) {
      toast.success("Tenant policy saved");
      clearSaveError();
      return;
    }

    const description =
      result.error?.code === "conflict"
        ? "Bundle changed on server. Refresh the bundle and retry your tenant update."
        : result.error?.details;
    toast.error(result.error?.message ?? "Failed to save tenant policy", {
      description,
    });
  };

  if (isLoading && bundles.length === 0) {
    return (
      <div className="space-y-3">
        <SkeletonCard />
        <SkeletonCard />
      </div>
    );
  }

  if (!isLoading && bundles.length === 0) {
    return (
      <EmptyState
        title="No policy bundles found"
        description="Create or sync a policy bundle before viewing tenant policy detail."
        action={<Button variant="outline" size="sm" onClick={() => navigate("/govern/overview?tab=bundles")}>View bundles</Button>}
      />
    );
  }

  if (!tenant) {
    return (
      <EmptyState
        title="Tenant policy not found in selected bundle"
        description={`No tenants.${tenantId} entry exists in the selected bundle.`}
        action={
          <Button variant="outline" size="sm" onClick={() => navigate("/govern/tenants")}>
            Back to tenants
          </Button>
        }
      />
    );
  }

  return (
    <motion.div
      className="space-y-6"
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.3, ease: "easeOut" }}
    >
      <PageHeader
        label="Govern / Tenants"
        title={tenant.label ?? tenant.id}
        subtitle={`Tenant ID: ${tenant.id}`}
        actions={
          <StatusBadge variant={affordances.canEdit ? "healthy" : "muted"}>
            {affordances.canEdit ? "editor access" : "read-only role"}
          </StatusBadge>
        }
      />

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              const params = selectedBundleId
                ? `?bundle=${encodeURIComponent(selectedBundleId)}`
                : "";
              navigate(`/govern/tenants${params}`);
            }}
          >
            <ArrowLeft className="mr-1 h-3.5 w-3.5" />
            Back to tenants
          </Button>
          <label className="text-xs text-muted-foreground">
            Bundle
            <select
              id="govern-tenant-detail-bundle-select"
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
        {affordances.showSave && (
          <Button
            size="sm"
            disabled={!isDirty || isSaving || !selectedBundleId}
            onClick={() => void saveTenantChanges()}
          >
            <Save className="mr-1 h-3.5 w-3.5" />
            {isSaving ? "Saving…" : "Save"}
          </Button>
        )}
      </div>

      {loadError && (
        <InfoBanner variant="error" title="Unable to load tenant policy detail">
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

      {saveError && (
        <InfoBanner variant="error" title="Tenant policy save failed">
          <p>{saveError.message}</p>
          {saveError.details && <p className="mt-1 text-destructive">{saveError.details}</p>}
          {saveError.code === "conflict" && (
            <p className="mt-1 text-destructive">
              Conflict detected: the bundle changed on server. Reload bundle content and retry.
            </p>
          )}
        </InfoBanner>
      )}

      {affordances.showReadOnlyBanner && (
        <InfoBanner variant="warning">
          Viewer mode: tenant detail is read-only. Deep-linked tenant pages remain inspectable without edit controls.
        </InfoBanner>
      )}

      {affordances.canEdit && (
        <InstrumentCard accent="muted" className="p-4 space-y-3">
          <h3 className="font-display text-sm font-semibold text-foreground">
            Edit tenant policy fields
          </h3>
          <p className="text-xs text-muted-foreground">
            Changes update in-memory bundle state and are persisted with Save using existing bundle endpoints.
          </p>

          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <TenantTagListEditor
              inputId="tenant-allow-topics"
              label="allow_topics"
              helpText="Topic patterns allowed for this tenant."
              hint="Case-insensitive matching; deny overrides allow."
              values={tenant.allowTopics}
              onChange={(next) => {
                setTenantPolicy(tenant.id, (previous) =>
                  previous ? { ...previous, allowTopics: next } : previous,
                );
              }}
            />
            <TenantTagListEditor
              inputId="tenant-deny-topics"
              label="deny_topics"
              helpText="Topic patterns denied for this tenant."
              hint="Use precise deny patterns to avoid broad unintended blocks."
              values={tenant.denyTopics}
              onChange={(next) => {
                setTenantPolicy(tenant.id, (previous) =>
                  previous ? { ...previous, denyTopics: next } : previous,
                );
              }}
            />
            <TenantTagListEditor
              inputId="tenant-allow-repo-hosts"
              label="allowed_repo_hosts"
              helpText="Allowed repository hosts for this tenant."
              values={tenant.allowedRepoHosts}
              onChange={(next) => {
                setTenantPolicy(tenant.id, (previous) =>
                  previous ? { ...previous, allowedRepoHosts: next } : previous,
                );
              }}
            />
            <TenantTagListEditor
              inputId="tenant-deny-repo-hosts"
              label="denied_repo_hosts"
              helpText="Denied repository hosts for this tenant."
              values={tenant.deniedRepoHosts}
              onChange={(next) => {
                setTenantPolicy(tenant.id, (previous) =>
                  previous ? { ...previous, deniedRepoHosts: next } : previous,
                );
              }}
            />
            <label className="text-xs text-muted-foreground">
              max_concurrent_jobs
              <input
                className="mt-1 h-8 w-full rounded-2xl border border-border bg-surface-2 px-3 text-xs text-foreground"
                type="number"
                min={0}
                step={1}
                value={
                  typeof tenant.maxConcurrentJobs === "number"
                    ? tenant.maxConcurrentJobs
                    : ""
                }
                onChange={(event) => {
                  const nextValue = event.target.value;
                  const parsed =
                    nextValue.trim() === ""
                      ? undefined
                      : Number.parseInt(nextValue, 10);
                  setTenantPolicy(tenant.id, (previous) =>
                    previous
                      ? {
                          ...previous,
                          maxConcurrentJobs:
                            typeof parsed === "number" && Number.isFinite(parsed)
                              ? Math.max(0, parsed)
                              : undefined,
                        }
                      : previous,
                  );
                }}
              />
              <p className="mt-1 text-xs text-muted-foreground">
                Leave empty to inherit system defaults.
              </p>
            </label>
          </div>

          <TenantMcpMatrixEditor
            matrix={tenant.mcp}
            onChange={(next) => {
              setTenantPolicy(tenant.id, (previous) =>
                previous ? { ...previous, mcp: next } : previous,
              );
            }}
          />
        </InstrumentCard>
      )}

      <TenantTopicAccessSection
        allowTopics={tenant.allowTopics}
        denyTopics={tenant.denyTopics}
      />

      <TenantMcpGovernanceSection matrix={tenant.mcp} />

      <TenantLimitsSection
        maxConcurrentJobs={tenant.maxConcurrentJobs}
        allowedRepoHosts={tenant.allowedRepoHosts}
        deniedRepoHosts={tenant.deniedRepoHosts}
      />

      <TenantScopedRulesSection tenantId={tenant.id} rules={scopedRules} />
    </motion.div>
  );
}
