import { useConfig } from "../../hooks/useSettings";
import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Environment mapping
// ---------------------------------------------------------------------------

interface EnvConfig {
  label: string;
  color: string;
  pulse?: boolean;
}

const ENV_MAP: Record<string, EnvConfig> = {
  production: { label: "PROD", color: "var(--danger)", pulse: true },
  prod: { label: "PROD", color: "var(--danger)", pulse: true },
  staging: { label: "STAGING", color: "var(--warning)" },
  stag: { label: "STAGING", color: "var(--warning)" },
  development: { label: "DEV", color: "var(--success)" },
  dev: { label: "DEV", color: "var(--success)" },
  local: { label: "LOCAL", color: "var(--success)" },
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
  return (
    <div
      className="h-[3px] w-full"
      style={{ backgroundColor: env.color }}
    />
  );
}

/** Small pill badge showing environment name. */
export function EnvironmentBadge() {
  const env = useEnvironment();
  if (!env) return null;
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-primary-foreground",
        env.pulse && "animate-pulse",
      )}
      style={{ backgroundColor: env.color }}
    >
      {env.label}
    </span>
  );
}
