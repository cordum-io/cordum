import { useEffect, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { AlertTriangle, Shield, ShieldCheck, ShieldOff } from "lucide-react";
import { ApiError, post, put } from "../../api/client";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../components/ui/Card";
import { Select } from "../../components/ui/Select";
import { usePageTitle } from "../../hooks/usePageTitle";
import { useConfig } from "../../hooks/useSettings";
import { useStatus } from "../../hooks/useStatus";
import { FailOpenCounter } from "../../components/settings/FailOpenCounter";
import { logger } from "../../lib/logger";
import { useToastStore } from "../../state/toast";
import { ErrorBanner } from "../../components/ui/ErrorBanner";

type FailMode = "open" | "closed";

interface InputSafetyFormState {
  failMode: FailMode;
}

interface InputPolicyStatus {
  fail_mode?: string;
  kernel_connected?: boolean;
}

const DEFAULT_FORM_STATE: InputSafetyFormState = {
  failMode: "closed",
};

function toObject(value: unknown): Record<string, unknown> | undefined {
  if (value && typeof value === "object") {
    return value as Record<string, unknown>;
  }
  return undefined;
}

function parseInputSafetyConfig(raw?: Record<string, unknown>): InputSafetyFormState {
  if (!raw) return DEFAULT_FORM_STATE;

  const inputPolicy = toObject(raw.input_policy);

  const failModeRaw =
    (typeof inputPolicy?.fail_mode === "string" ? inputPolicy.fail_mode : undefined) ??
    (typeof raw.policy_check_fail_mode === "string" ? raw.policy_check_fail_mode : undefined) ??
    (typeof raw.POLICY_CHECK_FAIL_MODE === "string" ? raw.POLICY_CHECK_FAIL_MODE : undefined) ??
    DEFAULT_FORM_STATE.failMode;

  return {
    failMode: failModeRaw === "open" ? "open" : "closed",
  };
}

function mergeInputSafetyConfig(
  existingConfig: Record<string, unknown> | undefined,
  state: InputSafetyFormState,
): Record<string, unknown> {
  const current = { ...(existingConfig ?? {}) };
  const currentInputPolicy = toObject(current.input_policy) ?? {};

  return {
    ...current,
    input_policy: {
      ...currentInputPolicy,
      fail_mode: state.failMode,
    },
    policy_check_fail_mode: state.failMode,
  };
}

function wrapConfigPayload(
  data: Record<string, unknown>,
  scopeTag: string,
): Record<string, unknown> {
  return {
    scope: "system",
    scope_id: "default",
    data,
    meta: { scope: scopeTag, source: "dashboard" },
  };
}

async function persistConfig(payload: Record<string, unknown>): Promise<void> {
  try {
    await put<void>("/config", payload);
  } catch (err) {
    if (err instanceof ApiError && (err.status === 404 || err.status === 405)) {
      await post<void>("/config", payload);
      return;
    }
    throw err;
  }
}

export default function InputSafetySettings() {
  usePageTitle("Settings - Input Safety");

  const queryClient = useQueryClient();
  const { data: configData, isLoading: configLoading, isError: configError, error: configErr, refetch: refetchConfig } = useConfig();
  const { data: status } = useStatus();
  const addToast = useToastStore((s) => s.addToast);

  const [form, setForm] = useState<InputSafetyFormState>(DEFAULT_FORM_STATE);

  useEffect(() => {
    setForm(parseInputSafetyConfig(configData));
  }, [configData]);

  const baseline = useMemo(() => parseInputSafetyConfig(configData), [configData]);
  const isDirty = JSON.stringify(form) !== JSON.stringify(baseline);

  const saveMutation = useMutation<void, Error, InputSafetyFormState>({
    mutationFn: async (nextState) => {
      const merged = mergeInputSafetyConfig(toObject(configData), nextState);
      await persistConfig(wrapConfigPayload(merged, "input_policy"));
    },
    onSuccess: () => {
      addToast({ type: "success", title: "Input Safety settings saved" });
      queryClient.invalidateQueries({ queryKey: ["config"] });
      queryClient.invalidateQueries({ queryKey: ["status"] });
    },
    onError: (err) => {
      logger.error("input-safety", "failed to save input safety settings", {
        error: err.message,
      });
      addToast({
        type: "error",
        title: "Failed to save Input Safety settings",
        description: err.message,
      });
    },
  });

  const inputStatus = (status as { input_policy?: InputPolicyStatus } | undefined)?.input_policy;
  const kernelConnected = inputStatus?.kernel_connected;

  const onSave = () => {
    saveMutation.mutate(form);
  };

  if (configError) {
    return <ErrorBanner message={configErr instanceof Error ? configErr.message : "Failed to load input safety settings"} onRetry={() => void refetchConfig()} />;
  }

  if (configLoading) {
    return (
      <div className="space-y-4">
        {Array.from({ length: 2 }, (_, i) => (
          <div key={i} className="h-32 animate-pulse rounded-2xl bg-surface2" />
        ))}
      </div>
    );
  }

  return (
    <motion.div
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      className="space-y-6 pb-12"
    >
      <Card className="instrument-card">
        <CardHeader className="flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <CardTitle>Input Safety — Fail Mode</CardTitle>
            <CardDescription>
              Controls scheduler behavior when the safety kernel is unreachable during pre-dispatch policy checks.
            </CardDescription>
          </div>
          <Badge variant={form.failMode === "closed" ? "success" : "warning"}>
            Fail-{form.failMode}
          </Badge>
        </CardHeader>

        <div className="space-y-4">
          <div className="space-y-1">
            <label className="text-xs font-semibold text-muted-foreground">Fail Mode</label>
            <Select
              value={form.failMode}
              onChange={(e) =>
                setForm((curr) => ({
                  ...curr,
                  failMode: e.target.value === "open" ? "open" : "closed",
                }))
              }
            >
              <option value="closed">Closed (requeue jobs when kernel is down)</option>
              <option value="open">Open (allow jobs through when kernel is down)</option>
            </Select>
          </div>

          {form.failMode === "open" && (
            <div className="flex items-start gap-3 rounded-2xl border border-warning/40 bg-warning/5 p-4">
              <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-warning" />
              <div>
                <p className="text-sm font-semibold text-ink">Fail-open mode bypasses safety checks</p>
                <p className="mt-1 text-xs text-muted-foreground">
                  When the safety kernel is unreachable, jobs will be allowed to proceed without
                  policy evaluation. This may be acceptable for availability-critical workloads
                  but introduces risk. Monitor the <code className="text-xs">cordum_scheduler_input_fail_open_total</code> metric.
                </p>
              </div>
            </div>
          )}

          <div className="flex flex-wrap items-center gap-2 text-xs">
            <Badge variant={kernelConnected ? "success" : kernelConnected === false ? "danger" : "default"}>
              <Shield className="mr-1 inline h-3 w-3" />
              Kernel {kernelConnected ? "connected" : kernelConnected === false ? "disconnected" : "unknown"}
            </Badge>
            <Badge variant={form.failMode === "closed" ? "success" : "warning"}>
              {form.failMode === "closed" ? (
                <ShieldCheck className="mr-1 inline h-3 w-3" />
              ) : (
                <ShieldOff className="mr-1 inline h-3 w-3" />
              )}
              Runtime: fail-{inputStatus?.fail_mode || form.failMode}
            </Badge>
          </div>

          <FailOpenCounter
            count={status?.input_fail_open_total}
            failMode={form.failMode}
            inputCB={status?.circuit_breakers?.input}
          />

          <div className="flex items-center gap-3">
            <Button
              type="button"
              onClick={onSave}
              disabled={!isDirty || saveMutation.isPending}
            >
              {saveMutation.isPending ? "Saving..." : "Save Input Safety Settings"}
            </Button>
            {isDirty ? (
              <button
                type="button"
                className="text-xs font-semibold text-muted-foreground hover:text-ink"
                onClick={() => setForm(baseline)}
              >
                Reset
              </button>
            ) : (
              <span className="text-xs text-muted-foreground">No unsaved changes</span>
            )}
          </div>
        </div>
      </Card>

      <Card className="instrument-card">
        <CardHeader>
          <div>
            <CardTitle>How It Works</CardTitle>
            <CardDescription>
              Pre-dispatch policy evaluation and fail-mode behavior.
            </CardDescription>
          </div>
        </CardHeader>
        <div className="space-y-3 text-sm text-muted-foreground">
          <p>
            Before every job is dispatched to a worker pool, the scheduler sends the job request
            to the safety kernel for policy evaluation (allow, deny, require approval, throttle).
          </p>
          <p>
            <strong className="text-ink">Fail-closed (default):</strong> When the safety kernel is unreachable,
            the scheduler requeues the job with exponential backoff. Jobs are never dispatched without
            a policy decision. This is the recommended mode for production.
          </p>
          <p>
            <strong className="text-ink">Fail-open:</strong> When the safety kernel is unreachable,
            the scheduler allows the job through with a warning log and increments
            the <code className="text-xs">input_fail_open_total</code> Prometheus counter.
            Use this only when availability takes priority over safety enforcement.
          </p>
          <p>
            Set via <code className="text-xs">POLICY_CHECK_FAIL_MODE</code> environment variable
            or through this dashboard page.
          </p>
        </div>
      </Card>
    </motion.div>
  );
}
