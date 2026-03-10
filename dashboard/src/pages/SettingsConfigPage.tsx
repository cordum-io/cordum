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
import { SkeletonCard } from "@/components/ui/Skeleton";
import { Save, RotateCcw, AlertTriangle, Settings, Shield, Database, Zap } from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";

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

export default function SettingsConfigPage() {
  const [values, setValues] = useState<Record<string, ConfigValue>>({});
  const [originalValues, setOriginalValues] = useState<Record<string, ConfigValue>>({});
  const [activeGroup, setActiveGroup] = useState("general");

  const { data: configData, isLoading } = useQuery({
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

  const hasChanges = JSON.stringify(values) !== JSON.stringify(originalValues);

  const saveMutation = useMutation({
    mutationFn: async () => post("/config", values),
    onSuccess: () => { setOriginalValues({ ...values }); toast.success("Configuration saved"); },
    onError: () => toast.error("Failed to save configuration"),
  });

  const updateValue = (key: string, value: ConfigValue) => {
    setValues(prev => ({ ...prev, [key]: value }));
  };

  const currentGroup = GROUPS.find(g => g.id === activeGroup);

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
            className="flex items-center justify-between px-4 py-3 rounded-2xl bg-[var(--color-warning)]/10 border border-[var(--color-warning)]/20"
          >
            <div className="flex items-center gap-2">
              <AlertTriangle className="w-4 h-4 text-[var(--color-warning)]" />
              <span className="text-sm text-[var(--color-warning)]">You have unsaved changes</span>
            </div>
            <div className="flex items-center gap-2">
              <Button variant="ghost" size="sm" onClick={() => setValues({ ...originalValues })}>
                <RotateCcw className="w-3 h-3 mr-1" />Discard
              </Button>
              <Button variant="primary" size="sm" onClick={() => saveMutation.mutate()} loading={saveMutation.isPending}>
                <Save className="w-3 h-3 mr-1" />Save Changes
              </Button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {isLoading ? (
        <div className="space-y-4">{Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)}</div>
      ) : (
        <div className="flex gap-6">
          {/* Group Nav */}
          <div className="w-48 shrink-0 space-y-1">
            {GROUPS.map(g => {
              const Icon = g.icon;
              return (
                <button
                  key={g.id}
                  onClick={() => setActiveGroup(g.id)}
                  className={cn(
                    "w-full flex items-center gap-2 px-3 py-2 rounded-2xl text-xs font-medium transition-colors text-left",
                    activeGroup === g.id ? "bg-cordum/10 text-cordum" : "text-muted-foreground hover:text-foreground hover:bg-surface-1",
                  )}
                >
                  <Icon className="w-3.5 h-3.5" />{g.label}
                </button>
              );
            })}
          </div>

          {/* Fields */}
          {currentGroup && (
            <div className="flex-1 instrument-card p-6 space-y-6">
              <div>
                <h2 className="text-sm font-display font-semibold text-foreground">{currentGroup.label}</h2>
                <p className="text-xs text-muted-foreground mt-0.5">Configure {currentGroup.label.toLowerCase()} settings</p>
              </div>
              <div className="space-y-5">
                {currentGroup.fields.map(field => (
                  <div key={field.key} className="flex items-start justify-between gap-8">
                    <div className="flex-1">
                      <label className="text-xs font-medium text-foreground block">{field.label}</label>
                      {field.description && <p className="text-[10px] text-muted-foreground mt-0.5">{field.description}</p>}
                    </div>
                    <div className="w-48 shrink-0">
                      {field.type === "text" && (
                        <input
                          type="text"
                          value={String(values[field.key] ?? "")}
                          onChange={(e) => updateValue(field.key, e.target.value)}
                          className="h-9 w-full px-3 text-sm bg-surface-2 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
                        />
                      )}
                      {field.type === "number" && (
                        <input
                          type="number"
                          value={Number(values[field.key] ?? 0)}
                          onChange={(e) => updateValue(field.key, Number(e.target.value))}
                          className="h-9 w-full px-3 text-sm bg-surface-2 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
                        />
                      )}
                      {field.type === "select" && (
                        <select
                          value={String(values[field.key] ?? "")}
                          onChange={(e) => updateValue(field.key, e.target.value)}
                          className="h-9 w-full px-3 text-sm bg-surface-2 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
                        >
                          {field.options?.map(o => <option key={o} value={o}>{o}</option>)}
                        </select>
                      )}
                      {field.type === "toggle" && (
                        <button
                          onClick={() => updateValue(field.key, !values[field.key])}
                          className={cn(
                            "w-9 h-5 rounded-full relative transition-colors",
                            values[field.key] ? "bg-cordum" : "bg-surface-2",
                          )}
                        >
                          <div className={cn(
                            "absolute top-0.5 w-4 h-4 rounded-full bg-card transition-transform",
                            values[field.key] ? "left-[18px]" : "left-0.5",
                          )} />
                        </button>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </motion.div>
  );
}
