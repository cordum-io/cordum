import { useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import {
  Activity,
  ArrowUpRight,
  CheckCircle2,
  RefreshCw,
  FileUp,
  Gauge,
  KeyRound,
  RadioTower,
  ShieldCheck,
  XCircle,
} from "lucide-react";
import type { LicenseEntitlements, LicenseRights, TelemetryMode, TierUsageMetric } from "@/api/types";
import { get } from "@/api/client";
import { ExpiryBanner } from "@/components/ExpiryBanner";
import { TierBadge, normalizeLicensePlan } from "@/components/TierBadge";
import { TierLimitBar } from "@/components/TierLimitBar";
import { UpgradePrompt, shouldShowUpgradePrompt } from "@/components/UpgradePrompt";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { useLicenseOverview, useReloadLicense } from "@/hooks/useLicense";
import { cn } from "@/lib/utils";

type BackendTelemetryStatus = {
  mode?: TelemetryMode | string;
  endpoint?: string;
  last_collected_at?: string;
  last_reported_at?: string;
};

type TelemetryStatus = {
  mode?: TelemetryMode | string;
  endpoint?: string;
  lastCollectedAt?: string;
  lastReportedAt?: string;
};

const FEATURE_ROWS: Array<{ key: keyof LicenseEntitlements; label: string }> = [
  { key: "sso", label: "Single sign-on" },
  { key: "saml", label: "SAML" },
  { key: "scim", label: "SCIM" },
  { key: "rbac", label: "Advanced RBAC" },
  { key: "audit", label: "Audit trail" },
  { key: "auditExport", label: "Audit export" },
  { key: "siemExport", label: "SIEM export" },
  { key: "legalHold", label: "Legal hold" },
  { key: "velocityRules", label: "Velocity rules" },
  { key: "breakGlassAdmin", label: "Break-glass admin" },
];

const RIGHTS_ROWS: Array<{ key: keyof LicenseRights; label: string }> = [
  { key: "hostedService", label: "Hosted service" },
  { key: "embedding", label: "Embedding" },
  { key: "resale", label: "Resale" },
  { key: "whiteLabel", label: "White label" },
  { key: "supportSla", label: "Support SLA" },
];

function mapTelemetryStatus(response: BackendTelemetryStatus): TelemetryStatus {
  return {
    mode: response.mode,
    endpoint: response.endpoint,
    lastCollectedAt: response.last_collected_at,
    lastReportedAt: response.last_reported_at,
  };
}

function formatDateTime(raw?: string | null): string {
  if (!raw) return "—";
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return raw;
  return parsed.toLocaleString();
}

function formatDate(raw?: string | null): string {
  if (!raw) return "—";
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return raw;
  return parsed.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function formatCountdown(raw?: string | null): string {
  if (!raw) return "No expiry set";
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return raw;

  const diffMs = parsed.getTime() - Date.now();
  const dayMs = 24 * 60 * 60 * 1000;
  const days = Math.ceil(Math.abs(diffMs) / dayMs);

  if (diffMs >= 0) {
    return days <= 1 ? "Less than a day remaining" : `${days} days remaining`;
  }

  return days <= 1 ? "Expired less than a day ago" : `Expired ${days} days ago`;
}

function formatNumber(value?: number): string {
  return typeof value === "number" && Number.isFinite(value)
    ? value.toLocaleString()
    : "—";
}

function formatBytes(value?: number): string {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) return "—";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  if (value < 1024 * 1024 * 1024) return `${(value / (1024 * 1024)).toFixed(1)} MB`;
  return `${(value / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

function formatTelemetryMode(mode?: string | null): string {
  switch ((mode ?? "").trim().toLowerCase()) {
    case "local_only":
      return "Local only";
    case "off":
      return "Off";
    case "anonymous":
      return "Anonymous";
    default:
      return mode?.trim() ? mode : "Unknown";
  }
}

function formatPlanLabel(plan?: string | null): string {
  switch (normalizeLicensePlan(plan)) {
    case "enterprise":
      return "Enterprise";
    case "team":
      return "Team";
    default:
      return "Community";
  }
}

function renderFeatureState(enabled: boolean) {
  return enabled ? (
    <span className="inline-flex items-center gap-1 text-[var(--color-success)]">
      <CheckCircle2 className="h-3.5 w-3.5" />
      Enabled
    </span>
  ) : (
    <span className="inline-flex items-center gap-1 text-muted-foreground">
      <XCircle className="h-3.5 w-3.5" />
      Not included
    </span>
  );
}

function metricDetails(label: string, metric?: TierUsageMetric<number>): string | undefined {
  if (!metric) return undefined;
  if (label === "Workers" && typeof metric.registered === "number" && typeof metric.connected === "number") {
    return `${metric.registered.toLocaleString()} registered · ${metric.connected.toLocaleString()} connected`;
  }
  return undefined;
}

export default function LicensePage() {
  const { license, usage, isLoading, isError } = useLicenseOverview();
  const [selectedFileName, setSelectedFileName] = useState<string>("");
  const fileInputRef = useRef<HTMLInputElement>(null);

  const telemetryStatus = useQuery<TelemetryStatus>({
    queryKey: ["telemetry", "status"],
    queryFn: async () =>
      mapTelemetryStatus(await get<BackendTelemetryStatus>("/telemetry/status")),
    staleTime: 60_000,
    refetchInterval: 60_000,
    retry: 1,
  });

  const reloadLicense = useReloadLicense();

  const error = (license.error ?? usage.error) as Error | null;
  const licenseSummary = license.data;
  const usageSummary = usage.data;
  const expiryStatus = licenseSummary?.expiryStatus ?? licenseSummary?.license?.status;
  const planLabel = formatPlanLabel(licenseSummary?.plan ?? usageSummary?.plan);
  const runtimeTelemetryMode =
    telemetryStatus.data?.mode ??
    licenseSummary?.entitlements.telemetryMode ??
    "anonymous";

  const usageBars = useMemo(
    () => [
      { label: "Workers", metric: usageSummary?.usage.workers },
      { label: "Concurrent jobs", metric: usageSummary?.usage.concurrentJobs },
      { label: "Active workflows", metric: usageSummary?.usage.activeWorkflows },
      { label: "Schemas", metric: usageSummary?.usage.schemas },
      { label: "Policy bundles", metric: usageSummary?.usage.policyBundles },
    ],
    [usageSummary],
  );

  const pressuredMetrics = usageBars.filter((entry) => shouldShowUpgradePrompt(entry.metric));

  if (isError && !licenseSummary && !usageSummary) {
    return (
      <ErrorBanner
        title="Unable to load license data"
        message={error?.message ?? "Failed to load license usage and entitlements"}
        onRetry={() => {
          void license.refetch();
          void usage.refetch();
        }}
      />
    );
  }

  if (isLoading && !licenseSummary && !usageSummary) {
    return (
      <div className="space-y-6">
        <PageHeader
          label="Settings"
          title="License & Limits"
          subtitle="Current plan, entitlements, and capacity usage across the control plane."
        />
        <div className="grid gap-4 lg:grid-cols-3">
          {Array.from({ length: 3 }).map((_, index) => (
            <SkeletonCard key={index} />
          ))}
        </div>
        <div className="grid gap-4 xl:grid-cols-2">
          {Array.from({ length: 4 }).map((_, index) => (
            <SkeletonCard key={index} />
          ))}
        </div>
      </div>
    );
  }

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader
        label="Settings"
        title="License & Limits"
        subtitle="Review the active tier, runtime ceilings, and telemetry mode for this Cordum deployment."
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => reloadLicense.mutate()}
              disabled={reloadLicense.isPending}
            >
              <RefreshCw className={cn("mr-1 h-3.5 w-3.5", reloadLicense.isPending && "animate-spin")} />
              {reloadLicense.isPending ? "Reloading..." : "Reload license"}
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => window.open("https://cordum.io/pricing", "_blank", "noopener,noreferrer")}
            >
              Compare tiers
              <ArrowUpRight className="ml-1 h-3.5 w-3.5" />
            </Button>
          </div>
        }
      />

      {!licenseSummary?.license && normalizeLicensePlan(licenseSummary?.plan ?? usageSummary?.plan) === "community" && (
        <InfoBanner variant="info" title="Community defaults are active">
          No signed license is loaded for this deployment. Cordum is enforcing Community entitlements until a Team or Enterprise license is installed.
        </InfoBanner>
      )}

      <ExpiryBanner status={expiryStatus} expiresAt={licenseSummary?.license?.expiresAt} />

      <div className="grid gap-4 xl:grid-cols-3">
        <motion.section
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.04 }}
          className="instrument-card space-y-4"
        >
          <div className="flex items-start justify-between gap-3">
            <div>
              <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
                Current plan
              </p>
              <h2 className="mt-1 font-display text-xl font-semibold text-foreground">
                {planLabel}
              </h2>
            </div>
            <TierBadge plan={licenseSummary?.plan ?? usageSummary?.plan} />
          </div>

          <dl className="space-y-3 text-sm">
            <div className="flex items-center justify-between gap-4">
              <dt className="text-muted-foreground">Customer / org</dt>
              <dd className="font-mono text-right text-foreground">
                {licenseSummary?.license?.orgId ?? "Default tenant"}
              </dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-muted-foreground">License ID</dt>
              <dd className="font-mono text-right text-foreground">
                {licenseSummary?.license?.licenseId ?? "Community mode"}
              </dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-muted-foreground">Deployment</dt>
              <dd className="font-mono text-right text-foreground">
                {licenseSummary?.license?.deploymentType ?? "self-hosted"}
              </dd>
            </div>
          </dl>
        </motion.section>

        <motion.section
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.08 }}
          className="instrument-card space-y-4"
        >
          <div className="flex items-center gap-2">
            <KeyRound className="h-4 w-4 text-cordum" />
            <h2 className="text-sm font-display font-semibold text-foreground">
              Expiry status
            </h2>
          </div>

          <div className="space-y-3">
            <div>
              <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
                Status
              </p>
              <p className="mt-1 text-sm font-medium text-foreground">
                {(expiryStatus ?? "active").toString().replace(/_/g, " ")}
              </p>
            </div>

            <dl className="space-y-3 text-sm">
              <div className="flex items-center justify-between gap-4">
                <dt className="text-muted-foreground">Issued</dt>
                <dd className="font-mono text-right text-foreground">
                  {formatDate(licenseSummary?.license?.issuedAt)}
                </dd>
              </div>
              <div className="flex items-center justify-between gap-4">
                <dt className="text-muted-foreground">Valid from</dt>
                <dd className="font-mono text-right text-foreground">
                  {formatDate(licenseSummary?.license?.notBefore)}
                </dd>
              </div>
              <div className="flex items-center justify-between gap-4">
                <dt className="text-muted-foreground">Expires</dt>
                <dd className="font-mono text-right text-foreground">
                  {formatDate(licenseSummary?.license?.expiresAt)}
                </dd>
              </div>
            </dl>
          </div>

          <div className="rounded-2xl border border-border bg-surface-1 px-3 py-2 text-xs text-muted-foreground">
            {formatCountdown(licenseSummary?.license?.expiresAt)}
          </div>
        </motion.section>

        <motion.section
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.12 }}
          className="instrument-card space-y-4"
        >
          <div className="flex items-center gap-2">
            <RadioTower className="h-4 w-4 text-cordum" />
            <h2 className="text-sm font-display font-semibold text-foreground">
              Telemetry mode
            </h2>
          </div>

          <div className="flex flex-wrap gap-2">
            {(["off", "local_only", "anonymous"] as const).map((mode) => {
              const isActive = (runtimeTelemetryMode ?? "").toString().toLowerCase() === mode;
              return (
                <span
                  key={mode}
                  className={cn(
                    "inline-flex items-center rounded-full border px-3 py-1 text-xs font-medium",
                    isActive
                      ? "border-cordum/25 bg-cordum/10 text-cordum"
                      : "border-border bg-surface-1 text-muted-foreground",
                  )}
                >
                  {formatTelemetryMode(mode)}
                </span>
              );
            })}
          </div>

          <dl className="space-y-3 text-sm">
            <div className="flex items-center justify-between gap-4">
              <dt className="text-muted-foreground">Last collected</dt>
              <dd className="font-mono text-right text-foreground">
                {formatDateTime(telemetryStatus.data?.lastCollectedAt)}
              </dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-muted-foreground">Last reported</dt>
              <dd className="font-mono text-right text-foreground">
                {formatDateTime(telemetryStatus.data?.lastReportedAt)}
              </dd>
            </div>
          </dl>

          <p className="text-xs leading-relaxed text-muted-foreground">
            Telemetry mode is configured on the server. Update{" "}
            <span className="font-mono text-foreground">CORDUM_TELEMETRY_MODE</span> and restart the gateway to change it.
            {telemetryStatus.data?.endpoint && (
              <>
                {" "}Reports target <span className="font-mono text-foreground">{telemetryStatus.data.endpoint}</span>.
              </>
            )}
          </p>
        </motion.section>
      </div>

      <section className="space-y-4">
        <div className="flex items-center gap-2">
          <Gauge className="h-4 w-4 text-cordum" />
          <h2 className="text-sm font-display font-semibold text-foreground">
            Runtime ceilings
          </h2>
        </div>

        <div className="grid gap-4 xl:grid-cols-2">
          {usageBars.map((entry, index) => (
            <motion.div
              key={entry.label}
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: 0.16 + index * 0.04 }}
            >
              <TierLimitBar
                label={entry.label}
                metric={entry.metric}
                detail={metricDetails(entry.label, entry.metric)}
              />
            </motion.div>
          ))}
        </div>

        {pressuredMetrics.length > 0 && (
          <div className="space-y-3">
            {pressuredMetrics.map((entry) => (
              <UpgradePrompt
                key={entry.label}
                label={entry.label}
                metric={entry.metric}
                plan={planLabel}
              />
            ))}
          </div>
        )}
      </section>

      <div className="grid gap-4 xl:grid-cols-[1.2fr_0.8fr]">
        <motion.section
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.2 }}
          className="instrument-card space-y-5"
        >
          <div className="flex items-center gap-2">
            <ShieldCheck className="h-4 w-4 text-cordum" />
            <h2 className="text-sm font-display font-semibold text-foreground">
              Entitlements & rights
            </h2>
          </div>

          <div className="grid gap-6 lg:grid-cols-2">
            <div className="space-y-3">
              <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
                Capabilities
              </p>
              <div className="space-y-2 rounded-3xl border border-border bg-surface-1/70 p-4">
                {FEATURE_ROWS.map((entry) => (
                  <div key={entry.key} className="flex items-center justify-between gap-3 text-sm">
                    <span className="text-foreground">{entry.label}</span>
                    {renderFeatureState(Boolean(licenseSummary?.entitlements[entry.key]))}
                  </div>
                ))}
              </div>
            </div>

            <div className="space-y-3">
              <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
                Commercial rights
              </p>
              <div className="space-y-2 rounded-3xl border border-border bg-surface-1/70 p-4">
                {RIGHTS_ROWS.map((entry) => (
                  <div key={entry.key} className="flex items-center justify-between gap-3 text-sm">
                    <span className="text-foreground">{entry.label}</span>
                    {renderFeatureState(Boolean(licenseSummary?.rights?.[entry.key]))}
                  </div>
                ))}
              </div>
            </div>
          </div>

          <div className="space-y-3">
            <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
              Hard limits
            </p>
            <div className="overflow-hidden rounded-3xl border border-border">
              <table className="w-full text-sm">
                <tbody>
                  {[
                    ["Approval mode", licenseSummary?.entitlements.approvalMode ?? usageSummary?.usage.approvalMode.allowed ?? "—"],
                    ["Max workers", formatNumber(licenseSummary?.entitlements.maxWorkers)],
                    ["Concurrent jobs", formatNumber(licenseSummary?.entitlements.maxConcurrentJobs)],
                    ["Active workflows", formatNumber(licenseSummary?.entitlements.maxActiveWorkflows)],
                    ["Workflow steps / run", formatNumber(licenseSummary?.entitlements.maxWorkflowSteps)],
                    ["Schemas", formatNumber(licenseSummary?.entitlements.maxSchemaCount)],
                    ["Policy bundles", formatNumber(licenseSummary?.entitlements.maxPolicyBundles)],
                    ["Requests / second", formatNumber(licenseSummary?.entitlements.requestsPerSecond)],
                    ["Prompt chars", formatNumber(licenseSummary?.entitlements.maxPromptChars)],
                    ["JSON body size", formatBytes(licenseSummary?.entitlements.maxBodyBytes)],
                    ["Artifact size", formatBytes(licenseSummary?.entitlements.maxArtifactBytes)],
                    ["Audit retention", typeof licenseSummary?.entitlements.auditRetentionDays === "number" ? `${licenseSummary.entitlements.auditRetentionDays} days` : "—"],
                  ].map(([label, value], index) => (
                    <tr
                      key={label}
                      className={cn(index !== 0 && "border-t border-border", "bg-surface-1/50")}
                    >
                      <td className="px-4 py-3 text-muted-foreground">{label}</td>
                      <td className="px-4 py-3 text-right font-mono text-foreground">{value}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </motion.section>

        <motion.section
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.24 }}
          className="instrument-card space-y-5"
        >
          <div className="flex items-center gap-2">
            <FileUp className="h-4 w-4 text-cordum" />
            <h2 className="text-sm font-display font-semibold text-foreground">
              License file updates
            </h2>
          </div>

          <p className="text-sm leading-relaxed text-muted-foreground">
            Cordum reads signed licenses from the host filesystem. Select a replacement file here to stage the update, then replace the file referenced by{" "}
            <span className="font-mono text-foreground">CORDUM_LICENSE_FILE</span> on the server and restart the gateway.
          </p>

          <input
            ref={fileInputRef}
            type="file"
            accept=".json,application/json"
            className="hidden"
            onChange={(event) => setSelectedFileName(event.target.files?.[0]?.name ?? "")}
          />

          <div className="rounded-3xl border border-border bg-surface-1/70 p-4">
            <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
              Selected replacement
            </p>
            <p className="mt-2 truncate text-sm text-foreground">
              {selectedFileName || "No file selected"}
            </p>
          </div>

          <div className="flex flex-wrap gap-2">
            <Button variant="outline" size="sm" onClick={() => fileInputRef.current?.click()}>
              Select file
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => window.open("https://cordum.io/docs", "_blank", "noopener,noreferrer")}
            >
              Open docs
              <ArrowUpRight className="ml-1 h-3.5 w-3.5" />
            </Button>
          </div>

          <InfoBanner variant="info" title="Runtime note">
            Applying a new signed license does not delete data. If the current license is expired, Cordum preserves audit visibility and break-glass admin access while enforcing Community limits.
          </InfoBanner>

          <div className="space-y-3 rounded-3xl border border-border bg-surface-1/70 p-4 text-sm">
            <div className="flex items-center justify-between gap-3">
              <span className="text-muted-foreground">Telemetry endpoint</span>
              <span className="truncate font-mono text-right text-foreground">
                {telemetryStatus.data?.endpoint ?? "Not reporting"}
              </span>
            </div>
            <div className="flex items-center justify-between gap-3">
              <span className="text-muted-foreground">Tenant scope</span>
              <span className="font-mono text-right text-foreground">
                {usageSummary?.tenantId ?? "default"}
              </span>
            </div>
            <div className="flex items-center justify-between gap-3">
              <span className="text-muted-foreground">Plan mode</span>
              <span className="font-mono text-right text-foreground">
                {licenseSummary?.license?.mode ?? normalizeLicensePlan(licenseSummary?.plan)}
              </span>
            </div>
          </div>
        </motion.section>
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        {[
          {
            label: "Workflow steps / run",
            value: formatNumber(usageSummary?.usage.workflowSteps.allowed),
            helper: "Maximum steps available to each workflow run.",
            icon: Activity,
          },
          {
            label: "Prompt chars",
            value: formatNumber(usageSummary?.usage.promptChars.allowed),
            helper: "Per-request prompt ceiling enforced by the gateway.",
            icon: Gauge,
          },
          {
            label: "Body bytes",
            value: formatBytes(usageSummary?.usage.bodyBytes.allowed),
            helper: "JSON body limit before requests are rejected.",
            icon: RadioTower,
          },
        ].map((item, index) => {
          const Icon = item.icon;
          return (
            <motion.div
              key={item.label}
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: 0.28 + index * 0.04 }}
              className="instrument-card"
            >
              <div className="flex items-center justify-between gap-3">
                <div>
                  <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
                    {item.label}
                  </p>
                  <p className="mt-2 font-mono text-2xl font-bold text-foreground">{item.value}</p>
                </div>
                <Icon className="h-5 w-5 text-cordum" />
              </div>
              <p className="mt-3 text-xs leading-relaxed text-muted-foreground">{item.helper}</p>
            </motion.div>
          );
        })}
      </div>
    </motion.div>
  );
}
