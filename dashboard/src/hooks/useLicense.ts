import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post } from "../api/client";
import { useToastStore } from "../state/toast";
import type {
  LicenseEntitlements,
  LicenseInfo,
  LicenseRights,
  LicenseSummary,
  LicenseUsage,
  LicenseUsageSummary,
  TierUsageMetric,
} from "../api/types";

type BackendLicenseRights = {
  hosted_service?: boolean;
  embedding?: boolean;
  resale?: boolean;
  white_label?: boolean;
  support_sla?: boolean;
};

type BackendLicenseEntitlements = {
  approval_mode?: string;
  telemetry_mode?: string;
  max_workers?: number;
  requests_per_second?: number;
  max_concurrent_jobs?: number;
  max_workflow_steps?: number;
  max_active_workflows?: number;
  max_tenants?: number;
  max_schema_count?: number;
  max_prompt_chars?: number;
  max_body_bytes?: number;
  max_artifact_bytes?: number;
  max_policy_bundles?: number;
  audit_retention_days?: number;
  sso?: boolean;
  saml?: boolean;
  scim?: boolean;
  rbac?: boolean;
  audit?: boolean;
  audit_export?: boolean;
  siem_export?: boolean;
  legal_hold?: boolean;
  velocity_rules?: boolean;
  break_glass_admin?: boolean;
  features?: Record<string, boolean>;
  limits?: Record<string, number>;
};

type BackendLicenseInfo = {
  mode?: string;
  status?: string;
  plan?: string;
  org_id?: string;
  license_id?: string;
  deployment_type?: string;
  issued_at?: string;
  not_before?: string;
  expires_at?: string;
  features?: string[];
  limits?: Record<string, number>;
};

type BackendTierUsageMetric<TAllowed = number | string> = {
  current?: number;
  allowed?: TAllowed;
  registered?: number;
  connected?: number;
};

type BackendLicenseSummary = {
  plan?: string;
  entitlements?: BackendLicenseEntitlements | null;
  rights?: BackendLicenseRights | null;
  license?: BackendLicenseInfo | null;
  expiry_status?: string;
};

type BackendLicenseUsage = {
  workers?: BackendTierUsageMetric<number>;
  concurrent_jobs?: BackendTierUsageMetric<number>;
  active_workflows?: BackendTierUsageMetric<number>;
  workflow_steps?: BackendTierUsageMetric<number>;
  schemas?: BackendTierUsageMetric<number>;
  policy_bundles?: BackendTierUsageMetric<number>;
  requests_per_second?: BackendTierUsageMetric<number>;
  prompt_chars?: BackendTierUsageMetric<number>;
  body_bytes?: BackendTierUsageMetric<number>;
  approval_mode?: BackendTierUsageMetric<string>;
};

type BackendLicenseUsageSummary = {
  tenant_id?: string;
  plan?: string;
  license?: BackendLicenseInfo | null;
  usage?: BackendLicenseUsage;
};

function mapLicenseRights(rights?: BackendLicenseRights | null): LicenseRights | null {
  if (!rights) {
    return null;
  }

  return {
    hostedService: !!rights.hosted_service,
    embedding: !!rights.embedding,
    resale: !!rights.resale,
    whiteLabel: !!rights.white_label,
    supportSla: !!rights.support_sla,
  };
}

function mapLicenseEntitlements(entitlements?: BackendLicenseEntitlements | null): LicenseEntitlements {
  if (!entitlements) {
    return {};
  }

  return {
    approvalMode: entitlements.approval_mode,
    telemetryMode: entitlements.telemetry_mode,
    maxWorkers: entitlements.max_workers,
    requestsPerSecond: entitlements.requests_per_second,
    maxConcurrentJobs: entitlements.max_concurrent_jobs,
    maxWorkflowSteps: entitlements.max_workflow_steps,
    maxActiveWorkflows: entitlements.max_active_workflows,
    maxTenants: entitlements.max_tenants,
    maxSchemaCount: entitlements.max_schema_count,
    maxPromptChars: entitlements.max_prompt_chars,
    maxBodyBytes: entitlements.max_body_bytes,
    maxArtifactBytes: entitlements.max_artifact_bytes,
    maxPolicyBundles: entitlements.max_policy_bundles,
    auditRetentionDays: entitlements.audit_retention_days,
    sso: entitlements.sso,
    saml: entitlements.saml,
    scim: entitlements.scim,
    rbac: entitlements.rbac,
    audit: entitlements.audit,
    auditExport: entitlements.audit_export,
    siemExport: entitlements.siem_export,
    legalHold: entitlements.legal_hold,
    velocityRules: entitlements.velocity_rules,
    breakGlassAdmin: entitlements.break_glass_admin,
    features: entitlements.features,
    limits: entitlements.limits,
  };
}

function mapLicenseInfo(info?: BackendLicenseInfo | null): LicenseInfo | null | undefined {
  if (info === null) {
    return null;
  }
  if (!info) {
    return undefined;
  }

  return {
    mode: info.mode,
    status: info.status,
    plan: info.plan,
    orgId: info.org_id,
    licenseId: info.license_id,
    deploymentType: info.deployment_type,
    issuedAt: info.issued_at,
    notBefore: info.not_before,
    expiresAt: info.expires_at,
    features: info.features,
    limits: info.limits,
  };
}

function mapTierUsageMetric<TAllowed = number | string>(
  metric?: BackendTierUsageMetric<TAllowed>,
): TierUsageMetric<TAllowed> {
  return {
    current: metric?.current,
    allowed: metric?.allowed,
    registered: metric?.registered,
    connected: metric?.connected,
  };
}

function mapLicenseUsage(usage?: BackendLicenseUsage): LicenseUsage {
  return {
    workers: mapTierUsageMetric(usage?.workers),
    concurrentJobs: mapTierUsageMetric(usage?.concurrent_jobs),
    activeWorkflows: mapTierUsageMetric(usage?.active_workflows),
    workflowSteps: mapTierUsageMetric(usage?.workflow_steps),
    schemas: mapTierUsageMetric(usage?.schemas),
    policyBundles: mapTierUsageMetric(usage?.policy_bundles),
    requestsPerSecond: mapTierUsageMetric(usage?.requests_per_second),
    promptChars: mapTierUsageMetric(usage?.prompt_chars),
    bodyBytes: mapTierUsageMetric(usage?.body_bytes),
    approvalMode: mapTierUsageMetric(usage?.approval_mode),
  };
}

function mapLicenseSummary(response: BackendLicenseSummary): LicenseSummary {
  return {
    plan: response.plan ?? "community",
    entitlements: mapLicenseEntitlements(response.entitlements),
    rights: mapLicenseRights(response.rights),
    license: mapLicenseInfo(response.license),
    expiryStatus: response.expiry_status,
  };
}

function mapLicenseUsageSummary(response: BackendLicenseUsageSummary): LicenseUsageSummary {
  return {
    tenantId: response.tenant_id ?? "",
    plan: response.plan ?? "community",
    license: mapLicenseInfo(response.license),
    usage: mapLicenseUsage(response.usage),
  };
}

export function useLicense() {
  return useQuery<LicenseSummary>({
    queryKey: ["license"],
    queryFn: async () => mapLicenseSummary(await get<BackendLicenseSummary>("/license")),
    staleTime: 30_000,
    refetchInterval: 60_000,
  });
}

export function useLicenseUsage() {
  return useQuery<LicenseUsageSummary>({
    queryKey: ["license", "usage"],
    queryFn: async () =>
      mapLicenseUsageSummary(await get<BackendLicenseUsageSummary>("/license/usage")),
    staleTime: 10_000,
    refetchInterval: 15_000,
  });
}

export function useLicenseOverview() {
  const license = useLicense();
  const usage = useLicenseUsage();

  return {
    license,
    usage,
    isLoading: license.isLoading || usage.isLoading,
    isError: license.isError || usage.isError,
  };
}

export function useReloadLicense() {
  const queryClient = useQueryClient();
  return useMutation<{ status: string; plan: string }, Error>({
    mutationKey: ["license", "reload"],
    mutationFn: () => post<{ status: string; plan: string }>("/license/reload"),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["license"] });
      useToastStore.getState().addToast({
        type: "success",
        title: `License reloaded — ${data.plan ?? "community"} plan active`,
      });
    },
    onError: (err) => {
      useToastStore.getState().addToast({
        type: "error",
        title: "License reload failed",
        description: err.message,
      });
    },
  });
}

/** @internal exported for unit tests */
export const __licenseInternal = {
  mapLicenseRights,
  mapLicenseEntitlements,
  mapLicenseInfo,
  mapTierUsageMetric,
  mapLicenseSummary,
  mapLicenseUsageSummary,
};
