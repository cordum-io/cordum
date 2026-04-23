import { useConfig } from "../../hooks/useSettings";
import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Environment mapping
// ---------------------------------------------------------------------------

interface EnvConfig {
  label: string;
  tone: "success" | "warning" | "danger";
  pulse?: boolean;
}

const ENV_MAP: Record<string, EnvConfig> = {
  production: { label: "PROD", tone: "danger", pulse: true },
  prod: { label: "PROD", tone: "danger", pulse: true },
  staging: { label: "STAGING", tone: "warning" },
  stag: { label: "STAGING", tone: "warning" },
  development: { label: "DEV", tone: "success" },
  dev: { label: "DEV", tone: "success" },
  local: { label: "LOCAL", tone: "success" },
};

function useEnvironment(): EnvConfig | null {
  const { data: config } = useConfig();

  // Primary: VITE_ENVIRONMENT env var
  const envVar = import.meta.env.VITE_ENVIRONMENT as string | undefined;
  // Fallback: system config environment field
  const configEnv =
    typeof config?.environment === "string" ? config.environment : undefined;

  const raw = (envVar || configEnv || "").toLowerCase().trim();
  if (!raw) return null;
  return ENV_MAP[raw] ?? null;
}

// ---------------------------------------------------------------------------
// Components
// ---------------------------------------------------------------------------

/** Thin colored border at the very top of the header. */
export function EnvironmentBorder() {
  const env = useEnvironment();
  if (!env) return null;

  const bgClass =
    env.tone === "danger"
      ? "bg-danger"
      : env.tone === "warning"
        ? "bg-warning"
        : "bg-success";

  return (
    <div className={cn("h-[2px] w-full", bgClass)} aria-hidden />
  );
}

/** Small pill badge showing environment name. */
export function EnvironmentBadge() {
  const env = useEnvironment();
  if (!env) return null;

  const toneClass =
    env.tone === "danger"
      ? "border-status-danger-border bg-status-danger-bg text-danger"
      : env.tone === "warning"
        ? "border-status-warning-border bg-status-warning-bg text-warning"
        : "border-status-success-border bg-status-success-bg text-success";

  return (
    <span
      className={cn(
        "inline-flex items-center rounded-sm border px-2 py-0.5 font-mono text-[10px] font-bold uppercase tracking-[0.14em]",
        toneClass,
        env.pulse && "animate-pulse motion-reduce:animate-none",
      )}
      aria-label={`Environment ${env.label}`}
    >
      {env.label}
    </span>
  );
}
