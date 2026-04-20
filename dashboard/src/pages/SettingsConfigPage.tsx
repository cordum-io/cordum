/*
 * DESIGN: "Control Surface" — System Configuration
 * PRD Section 27: Grouped settings with unsaved changes banner
 */
import { useState, useEffect } from "react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import { get, post } from "@/api/client";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { Checkbox } from "@/components/ui/Checkbox";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { Input } from "@/components/ui/Input";
import { LabeledField } from "@/components/ui/LabeledField";
import { Select } from "@/components/ui/Select";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { Tabs } from "@/components/ui/Tabs";
import { Save, RotateCcw, Settings, Shield, Database, Zap } from "lucide-react";
import { toast } from "sonner";
import { friendlyError } from "@/lib/friendlyError";
import { ErrorBanner } from "@/components/ui/ErrorBanner";

type ConfigValue = string | number | boolean;

interface ConfigGroup {
  id: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  fields: ConfigField[];
}

interface ConfigField {
  key: string;
  label: string;
  type: "text" | "number" | "toggle" | "select";
  value: ConfigValue;
  description?: string;
  options?: string[];
}

const GROUPS: ConfigGroup[] = [
  {
    id: "general", label: "General", icon: Settings,
    fields: [
      { key: "cluster_name", label: "Cluster Name", type: "text", value: "production", description: "Display name for this Cordum cluster" },
      { key: "log_level", label: "Log Level", type: "select", value: "info", options: ["debug", "info", "warn", "error"], description: "Minimum log level for server output" },
    ],
  },
  {
    id: "safety", label: "Safety", icon: Shield,
    fields: [
      { key: "safety_enabled", label: "Enable Safety Checks", type: "toggle", value: true, description: "Run input/output safety checks on all jobs" },
      { key: "safety_fail_mode", label: "Fail Mode", type: "select", value: "block", options: ["block", "warn", "log"], description: "Action when safety check fails" },
    ],
  },
  {
    id: "performance", label: "Performance", icon: Zap,
    fields: [
      { key: "max_concurrent_jobs", label: "Max Concurrent Jobs", type: "number", value: 100, description: "Maximum jobs running simultaneously" },
      { key: "job_timeout_seconds", label: "Default Job Timeout (s)", type: "number", value: 300, description: "Default timeout for jobs without explicit timeout" },
    ],
  },
  {
    id: "retention", label: "Data Retention", icon: Database,
    fields: [
      { key: "job_retention_days", label: "Job History (days)", type: "number", value: 90, description: "Days to retain completed job records" },
      { key: "audit_retention_days", label: "Audit Log (days)", type: "number", value: 365, description: "Days to retain audit log entries" },
    ],
  },
];

export function getChangedConfigKeys(
  currentValues: Record<string, ConfigValue>,
  originalValues: Record<string, ConfigValue>,
): string[] {
  return Object.keys(currentValues)
    .filter((key) => currentValues[key] !== originalValues[key])
    .sort();
}

export default function SettingsConfigPage() {
  const [values, setValues] = useState<Record<string, ConfigValue>>({});
  const [originalValues, setOriginalValues] = useState<Record<string, ConfigValue>>({});
  const [activeGroup, setActiveGroup] = useState("general");
  const [confirmSaveOpen, setConfirmSaveOpen] = useState(false);

  const { data: configData, isLoading, isError, error, refetch } = useQuery({
    queryKey: ["config"],
    queryFn: async () => {
      const res = await get<Record<string, unknown>>("/config");
      return res;
    },
  });

  // Initialize values from groups defaults, then overlay backend config when available
  useEffect(() => {
    const initial: Record<string, ConfigValue> = {};
    GROUPS.forEach(g => g.fields.forEach(f => { initial[f.key] = f.value; }));
    if (configData && typeof configData === "object") {
      for (const key of Object.keys(initial)) {
        const raw = (configData as Record<string, unknown>)[key];
        if (raw !== undefined && (typeof raw === "string" || typeof raw === "number" || typeof raw === "boolean")) {
          initial[key] = raw;
        }
      }
    }
    setValues(initial);
    setOriginalValues(initial);
  }, [configData]);

  const changedKeys = getChangedConfigKeys(values, originalValues);
  const hasChanges = changedKeys.length > 0;

  const saveMutation = useMutation({
    mutationFn: async () => post("/config", values),
    onSuccess: () => { setOriginalValues({ ...values }); toast.success("Configuration saved"); },
    onError: (err: unknown) => { const f = friendlyError(err, "save config"); toast.error(f.title, { description: f.description }); },
  });

  const updateValue = (key: string, value: ConfigValue) => {
    setValues(prev => ({ ...prev, [key]: value }));
  };

  const handleConfirmSave = () => {
    saveMutation.mutate(undefined, {
      onSuccess: () => {
        setConfirmSaveOpen(false);
      },
    });
  };

  const currentGroup = GROUPS.find(g => g.id === activeGroup);

  if (isError) {
    return <ErrorBanner message={error instanceof Error ? error.message : "Failed to load configuration"} onRetry={() => void refetch()} />;
  }

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader title="System Configuration" subtitle="Manage cluster-wide settings and defaults" />

      {/* Unsaved Changes Banner */}
      <AnimatePresence>
        {hasChanges && (
          <motion.div
            initial={{ opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: "auto" }}
            exit={{ opacity: 0, height: 0 }}
          >
            <InfoBanner variant="warning" title="You have unsaved changes">
              <div className="space-y-3">
                <p>
                  Review and confirm the cluster configuration changes before applying them live.
                </p>
                <div className="flex flex-wrap items-center gap-2">
                  <Button variant="ghost" size="sm" onClick={() => setValues({ ...originalValues })}>
                    <RotateCcw className="w-3 h-3 mr-1" />Discard
                  </Button>
                  <Button
                    variant="primary"
                    size="sm"
                    onClick={() => setConfirmSaveOpen(true)}
                    loading={saveMutation.isPending}
                  >
                    <Save className="w-3 h-3 mr-1" />Save Changes
                  </Button>
                </div>
              </div>
            </InfoBanner>
          </motion.div>
        )}
      </AnimatePresence>

      {isLoading ? (
        <div className="space-y-4">{Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)}</div>
      ) : (
        <div className="space-y-4">
          <Tabs
            tabs={GROUPS.map((group) => {
              const Icon = group.icon;
              return {
                id: group.id,
                label: group.label,
                icon: <Icon className="h-3.5 w-3.5" />,
              };
            })}
            activeTab={activeGroup}
            onChange={setActiveGroup}
            variant="segmented"
            ariaLabel="Configuration groups"
            className="w-full"
          />

          {/* Fields */}
          {currentGroup && (
            <div className="instrument-card space-y-6 p-6">
              <div>
                <h2 className="text-sm font-display font-semibold text-foreground">{currentGroup.label}</h2>
                <p className="mt-0.5 text-xs text-muted-foreground">Configure {currentGroup.label.toLowerCase()} settings</p>
              </div>
              <div className="space-y-5">
                {currentGroup.fields.map(field => {
                  const control = field.type === "text" ? (
                    <Input
                      type="text"
                      value={String(values[field.key] ?? "")}
                      onChange={(e) => updateValue(field.key, e.target.value)}
                      aria-label={field.label}
                    />
                  ) : field.type === "number" ? (
                    <Input
                      type="number"
                      value={Number(values[field.key] ?? 0)}
                      onChange={(e) => updateValue(field.key, Number(e.target.value))}
                      aria-label={field.label}
                    />
                  ) : field.type === "select" ? (
                    <Select
                      value={String(values[field.key] ?? "")}
                      onChange={(e) => updateValue(field.key, e.target.value)}
                      aria-label={field.label}
                      options={(field.options ?? []).map(o => ({ value: o, label: o }))}
                    />
                  ) : (
                    <Checkbox
                      checked={Boolean(values[field.key])}
                      onChange={(e) => updateValue(field.key, e.target.checked)}
                      aria-label={field.label}
                      label={Boolean(values[field.key]) ? "Enabled" : "Disabled"}
                    />
                  );

                  return (
                    <LabeledField
                      key={field.key}
                      label={field.label}
                      description={field.description}
                      action={<div className="w-full lg:w-56">{control}</div>}
                      className="rounded-2xl border border-border/60 bg-surface-0/40 p-4"
                    >
                      {null}
                    </LabeledField>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      )}

      <ConfirmDialog
        open={confirmSaveOpen}
        onClose={() => setConfirmSaveOpen(false)}
        onConfirm={handleConfirmSave}
        title="Apply production configuration changes?"
        description={
          <div className="space-y-3 text-sm text-muted-foreground">
            <p>
              This will change live cluster configuration and may affect active
              jobs, safety behavior, or retention policies.
            </p>
            {changedKeys.length > 0 && (
              <div>
                <p className="text-xs font-mono uppercase tracking-wide text-foreground">
                  Keys to update
                </p>
                <ul className="mt-2 space-y-1 text-xs font-mono text-foreground">
                  {changedKeys.map((key) => (
                    <li key={key}>{key}</li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        }
        confirmLabel="Save configuration"
        cancelLabel="Review changes"
        loading={saveMutation.isPending}
      />
    </motion.div>
  );
}
