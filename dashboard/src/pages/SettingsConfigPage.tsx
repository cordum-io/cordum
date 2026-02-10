import { useEffect } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Shield, Database, Gauge, Save, RotateCcw } from "lucide-react";
import { useGeneralConfig, useSetGeneralConfig } from "../hooks/useSettings";
import { Card } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { ProgressBar } from "../components/ProgressBar";
import { MaintenanceModeSection } from "../components/settings/MaintenanceModeSection";
import { cn } from "../lib/utils";
import type { GeneralConfig } from "../api/types";
import { usePageTitle } from "../hooks/usePageTitle";
import { RequireRole } from "../components/RequireRole";

// ---------------------------------------------------------------------------
// Form schema
// ---------------------------------------------------------------------------

const configSchema = z.object({
  safetyStance: z.enum(["permissive", "balanced", "strict"]),
  approvalTimeoutMin: z.number().min(5).max(60),
  autoDenyOnTimeout: z.boolean(),
  logRetentionDays: z.number().min(7).max(365),
  auditRetentionDays: z.number().min(7).max(365),
  dlqRetentionDays: z.number().min(1).max(90),
  rateLimitPerKey: z.number().min(1).max(10000),
  concurrentJobsLimit: z.number().min(1).max(1000),
  wsConnectionsLimit: z.number().min(1).max(500),
});

type ConfigFormValues = z.infer<typeof configSchema>;

// ---------------------------------------------------------------------------
// Stance options
// ---------------------------------------------------------------------------

const STANCES = [
  {
    value: "permissive" as const,
    label: "Permissive",
    description: "Allow by default, deny explicitly",
    border: "border-emerald-500",
    bg: "bg-emerald-500/10",
  },
  {
    value: "balanced" as const,
    label: "Balanced",
    description: "Require approval for high-risk actions",
    border: "border-accent",
    bg: "bg-accent/10",
  },
  {
    value: "strict" as const,
    label: "Strict",
    description: "Deny by default, allow explicitly",
    border: "border-red-500",
    bg: "bg-red-500/10",
  },
];

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function toFormValues(config: GeneralConfig): ConfigFormValues {
  return {
    safetyStance: config.safetyStance,
    approvalTimeoutMin: Math.round(config.approvalTimeoutMs / 60_000),
    autoDenyOnTimeout: config.autoDenyOnTimeout,
    logRetentionDays: config.logRetentionDays,
    auditRetentionDays: config.auditRetentionDays,
    dlqRetentionDays: config.dlqRetentionDays,
    rateLimitPerKey: config.rateLimitPerKey,
    concurrentJobsLimit: config.concurrentJobsLimit,
    wsConnectionsLimit: config.wsConnectionsLimit,
  };
}

function toConfigPatch(values: ConfigFormValues): Partial<GeneralConfig> {
  return {
    safetyStance: values.safetyStance,
    approvalTimeoutMs: values.approvalTimeoutMin * 60_000,
    autoDenyOnTimeout: values.autoDenyOnTimeout,
    logRetentionDays: values.logRetentionDays,
    auditRetentionDays: values.auditRetentionDays,
    dlqRetentionDays: values.dlqRetentionDays,
    rateLimitPerKey: values.rateLimitPerKey,
    concurrentJobsLimit: values.concurrentJobsLimit,
    wsConnectionsLimit: values.wsConnectionsLimit,
  };
}

function estimateStorage(log: number, audit: number, dlq: number): string {
  // Rough estimate: ~5MB/day logs, ~1MB/day audit, ~0.5MB/day DLQ
  const totalMb = log * 5 + audit * 1 + dlq * 0.5;
  if (totalMb >= 1000) return `~${(totalMb / 1000).toFixed(1)} GB`;
  return `~${Math.round(totalMb)} MB`;
}

// ---------------------------------------------------------------------------
// Section header
// ---------------------------------------------------------------------------

function SectionHeader({
  icon: Icon,
  title,
  description,
}: {
  icon: typeof Shield;
  title: string;
  description: string;
}) {
  return (
    <div className="flex items-start gap-3 mb-4">
      <Icon className="mt-0.5 h-5 w-5 shrink-0 text-accent" />
      <div>
        <h3 className="font-display text-base font-semibold text-ink">{title}</h3>
        <p className="text-xs text-muted">{description}</p>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export default function SettingsConfigPage() {
  usePageTitle("Settings - Configuration");
  const { data: config, isLoading } = useGeneralConfig();
  const saveConfig = useSetGeneralConfig();

  const {
    register,
    handleSubmit,
    watch,
    reset,
    setValue,
    formState: { errors, isDirty },
  } = useForm<ConfigFormValues>({
    resolver: zodResolver(configSchema),
    defaultValues: config ? toFormValues(config) : undefined,
  });

  // Sync form when config loads
  useEffect(() => {
    if (config) {
      reset(toFormValues(config));
    }
  }, [config, reset]);

  const stance = watch("safetyStance");
  const timeoutMin = watch("approvalTimeoutMin");
  const logDays = watch("logRetentionDays");
  const auditDays = watch("auditRetentionDays");
  const dlqDays = watch("dlqRetentionDays");
  const rateLimit = watch("rateLimitPerKey");
  const jobsLimit = watch("concurrentJobsLimit");
  const wsLimit = watch("wsConnectionsLimit");

  const onSubmit = (values: ConfigFormValues) => {
    saveConfig.mutate(toConfigPatch(values), {
      onSuccess: () => reset(values),
    });
  };

  if (isLoading) {
    return (
      <div className="space-y-4">
        {Array.from({ length: 3 }, (_, i) => (
          <div key={i} className="h-48 animate-pulse rounded-2xl bg-surface2" />
        ))}
      </div>
    );
  }

  return (
    <div className="space-y-6 pb-20">
      {/* Maintenance Mode — first for high-urgency visibility */}
      <MaintenanceModeSection />

      <form onSubmit={handleSubmit(onSubmit)} className="space-y-6">
      {/* Safety Defaults */}
      <Card>
        <SectionHeader
          icon={Shield}
          title="Safety Defaults"
          description="Default safety posture and approval behavior"
        />

        <div className="space-y-5">
          {/* Stance selector */}
          <div className="space-y-2">
            <label className="text-xs font-semibold text-muted">Safety Stance</label>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
              {STANCES.map((s) => (
                <button
                  key={s.value}
                  type="button"
                  onClick={() => setValue("safetyStance", s.value, { shouldDirty: true })}
                  className={cn(
                    "rounded-xl border-2 p-4 text-left transition",
                    stance === s.value
                      ? `${s.border} ${s.bg}`
                      : "border-border hover:border-ink/20",
                  )}
                >
                  <p className="text-sm font-semibold text-ink">{s.label}</p>
                  <p className="mt-1 text-xs text-muted">{s.description}</p>
                </button>
              ))}
            </div>
          </div>

          {/* Approval timeout slider */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-xs font-semibold text-muted">
                Approval Timeout
              </label>
              <span className="text-xs font-mono text-ink">{timeoutMin} min</span>
            </div>
            <input
              type="range"
              min={5}
              max={60}
              step={5}
              {...register("approvalTimeoutMin", { valueAsNumber: true })}
              className="w-full accent-accent"
            />
            <div className="flex justify-between text-[10px] text-muted">
              <span>5 min</span>
              <span>60 min</span>
            </div>
            {errors.approvalTimeoutMin && (
              <p className="text-xs text-danger">{errors.approvalTimeoutMin.message}</p>
            )}
          </div>

          {/* Auto-deny toggle */}
          <label className="flex items-start gap-3 cursor-pointer">
            <input
              type="checkbox"
              {...register("autoDenyOnTimeout")}
              className="mt-1 h-4 w-4 rounded border-border text-accent focus:ring-accent"
            />
            <div>
              <p className="text-sm font-medium text-ink">Auto-deny on timeout</p>
              <p className="text-xs text-warning">
                Jobs will be automatically denied if not approved within the timeout period.
              </p>
            </div>
          </label>
        </div>
      </Card>

      {/* Retention */}
      <Card>
        <SectionHeader
          icon={Database}
          title="Retention"
          description="How long data is kept before automatic cleanup"
        />

        <div className="space-y-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <div className="space-y-1">
              <label className="text-xs font-semibold text-muted">Log Retention (days)</label>
              <Input
                type="number"
                {...register("logRetentionDays", { valueAsNumber: true })}
                min={7}
                max={365}
              />
              {errors.logRetentionDays && (
                <p className="text-xs text-danger">{errors.logRetentionDays.message}</p>
              )}
            </div>
            <div className="space-y-1">
              <label className="text-xs font-semibold text-muted">Audit Retention (days)</label>
              <Input
                type="number"
                {...register("auditRetentionDays", { valueAsNumber: true })}
                min={7}
                max={365}
              />
              {errors.auditRetentionDays && (
                <p className="text-xs text-danger">{errors.auditRetentionDays.message}</p>
              )}
            </div>
            <div className="space-y-1">
              <label className="text-xs font-semibold text-muted">DLQ Retention (days)</label>
              <Input
                type="number"
                {...register("dlqRetentionDays", { valueAsNumber: true })}
                min={1}
                max={90}
              />
              {errors.dlqRetentionDays && (
                <p className="text-xs text-danger">{errors.dlqRetentionDays.message}</p>
              )}
            </div>
          </div>

          <p className="text-xs text-muted">
            Estimated storage:{" "}
            <span className="font-semibold text-ink">
              {estimateStorage(logDays ?? 30, auditDays ?? 90, dlqDays ?? 14)}
            </span>{" "}
            at current ingestion rate
          </p>
        </div>
      </Card>

      {/* Rate Limits */}
      <Card>
        <SectionHeader
          icon={Gauge}
          title="Rate Limits"
          description="Throttling limits for API keys, jobs, and connections"
        />

        <div className="space-y-5">
          {/* Requests per second */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-xs font-semibold text-muted">Requests/sec per key</label>
              <span className="text-xs font-mono text-ink">{rateLimit ?? 600}</span>
            </div>
            <Input
              type="number"
              {...register("rateLimitPerKey", { valueAsNumber: true })}
              min={1}
              max={10000}
            />
            {errors.rateLimitPerKey && (
              <p className="text-xs text-danger">{errors.rateLimitPerKey.message}</p>
            )}
            <ProgressBar value={0} className="h-1.5" />
            <p className="text-[10px] text-muted">Current usage: N/A</p>
          </div>

          {/* Concurrent jobs */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-xs font-semibold text-muted">Concurrent jobs limit</label>
              <span className="text-xs font-mono text-ink">{jobsLimit ?? 100}</span>
            </div>
            <Input
              type="number"
              {...register("concurrentJobsLimit", { valueAsNumber: true })}
              min={1}
              max={1000}
            />
            {errors.concurrentJobsLimit && (
              <p className="text-xs text-danger">{errors.concurrentJobsLimit.message}</p>
            )}
            <ProgressBar value={0} className="h-1.5" />
            <p className="text-[10px] text-muted">Current usage: N/A</p>
          </div>

          {/* WebSocket connections */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-xs font-semibold text-muted">WebSocket connections limit</label>
              <span className="text-xs font-mono text-ink">{wsLimit ?? 50}</span>
            </div>
            <Input
              type="number"
              {...register("wsConnectionsLimit", { valueAsNumber: true })}
              min={1}
              max={500}
            />
            {errors.wsConnectionsLimit && (
              <p className="text-xs text-danger">{errors.wsConnectionsLimit.message}</p>
            )}
            <ProgressBar value={0} className="h-1.5" />
            <p className="text-[10px] text-muted">Current usage: N/A</p>
          </div>
        </div>
      </Card>

      {/* Unsaved changes bar */}
      <RequireRole roles={["admin"]}>
        {isDirty && (
          <div className="fixed inset-x-0 bottom-0 z-20 border-t border-border bg-surface px-6 py-3 shadow-lift">
            <div className="mx-auto flex max-w-4xl items-center justify-between">
              <p className="text-sm font-medium text-warning">You have unsaved changes</p>
              <div className="flex items-center gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  type="button"
                  onClick={() => config && reset(toFormValues(config))}
                >
                  <RotateCcw className="h-3.5 w-3.5" />
                  Discard
                </Button>
                <Button
                  variant="primary"
                  size="sm"
                  type="submit"
                  disabled={saveConfig.isPending}
                >
                  <Save className="h-3.5 w-3.5" />
                  {saveConfig.isPending ? "Saving..." : "Save Changes"}
                </Button>
              </div>
            </div>
          </div>
        )}
      </RequireRole>

      {/* Success/error feedback */}
      {saveConfig.isSuccess && !isDirty && (
        <p className="text-center text-xs text-success">Configuration saved successfully.</p>
      )}
      {saveConfig.isError && (
        <p className="text-center text-xs text-danger">
          Failed to save: {saveConfig.error.message}
        </p>
      )}
    </form>
    </div>
  );
}
