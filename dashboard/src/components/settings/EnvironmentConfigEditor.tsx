import { useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Save, X, AlertTriangle } from "lucide-react";
import { Input } from "../ui/Input";
import { Button } from "../ui/Button";
import { KeyValueEditor } from "../ui/KeyValueEditor";
import { useSaveEnvironment } from "../../hooks/useSettings";
import type { Environment } from "../../api/types";

const envSchema = z.object({
  name: z.string().min(1, "Name is required").max(32),
  endpoint: z.string().max(256).optional(),
  status: z.enum(["active", "maintenance", "degraded"]),
});

type EnvFormValues = z.infer<typeof envSchema>;

interface KeyValuePair {
  key: string;
  value: string;
}

function configToKv(config: Record<string, unknown>): KeyValuePair[] {
  return Object.entries(config).map(([key, value]) => ({
    key,
    value: typeof value === "string" ? value : JSON.stringify(value),
  }));
}

function kvToConfig(pairs: KeyValuePair[]): Record<string, unknown> {
  const config: Record<string, unknown> = {};
  for (const { key, value } of pairs) {
    if (!key.trim()) continue;
    config[key.trim()] = value;
  }
  return config;
}

interface EnvironmentConfigEditorProps {
  env: Environment;
  onClose: () => void;
}

export function EnvironmentConfigEditor({ env, onClose }: EnvironmentConfigEditorProps) {
  const saveEnv = useSaveEnvironment();

  const {
    register,
    handleSubmit,
    formState: { errors, isDirty },
  } = useForm<EnvFormValues>({
    resolver: zodResolver(envSchema),
    defaultValues: {
      name: env.name,
      endpoint: env.endpoint ?? "",
      status: env.status,
    },
  });

  const [kvPairs, setKvPairs] = useState<KeyValuePair[]>(() => configToKv(env.config ?? {}));

  const originalConfig = useMemo(() => JSON.stringify(env.config ?? {}), [env.config]);
  const currentConfig = JSON.stringify(kvToConfig(kvPairs));
  const configChanged = originalConfig !== currentConfig;
  const hasChanges = isDirty || configChanged;

  const onSubmit = (values: EnvFormValues) => {
    const updated: Environment = {
      ...env,
      name: values.name,
      endpoint: values.endpoint || undefined,
      status: values.status,
      config: kvToConfig(kvPairs),
    };
    saveEnv.mutate(updated, { onSuccess: onClose });
  };

  return (
    <form onSubmit={handleSubmit(onSubmit)} className="space-y-6">
      <div className="flex items-center justify-between">
        <h3 className="font-display text-lg font-semibold text-ink">
          Edit Environment: {env.name}
        </h3>
        <button type="button" onClick={onClose} className="rounded-full p-1 hover:bg-surface2">
          <X className="h-5 w-5 text-muted" />
        </button>
      </div>

      <div className="space-y-4">
        <div className="space-y-1">
          <label className="text-xs font-semibold text-muted">Name</label>
          <Input {...register("name")} placeholder="production" />
          {errors.name && <p className="text-xs text-danger">{errors.name.message}</p>}
        </div>

        <div className="space-y-1">
          <label className="text-xs font-semibold text-muted">Endpoint URL</label>
          <Input {...register("endpoint")} placeholder="https://api.example.com" />
          {errors.endpoint && <p className="text-xs text-danger">{errors.endpoint.message}</p>}
        </div>

        <div className="space-y-1">
          <label className="text-xs font-semibold text-muted">Status</label>
          <select
            {...register("status")}
            className="w-full rounded-xl border border-border bg-surface px-4 py-2.5 text-sm text-ink focus:border-accent focus:outline-none"
          >
            <option value="active">Active</option>
            <option value="maintenance">Maintenance</option>
            <option value="degraded">Degraded</option>
          </select>
        </div>
      </div>

      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <label className="text-xs font-semibold text-muted">Configuration Overrides</label>
          {configChanged && (
            <span className="flex items-center gap-1 text-xs text-warning">
              <AlertTriangle className="h-3 w-3" />
              Config modified
            </span>
          )}
        </div>
        <KeyValueEditor
          value={kvPairs}
          onChange={setKvPairs}
          keyPlaceholder="Config key"
          valuePlaceholder="Value"
        />
      </div>

      <div className="flex items-center justify-end gap-2 border-t border-border pt-4">
        <Button variant="ghost" size="sm" type="button" onClick={onClose}>
          Cancel
        </Button>
        <Button
          variant="primary"
          size="sm"
          type="submit"
          disabled={!hasChanges || saveEnv.isPending}
        >
          <Save className="h-3.5 w-3.5" />
          {saveEnv.isPending ? "Saving..." : "Save Environment"}
        </Button>
      </div>
    </form>
  );
}
