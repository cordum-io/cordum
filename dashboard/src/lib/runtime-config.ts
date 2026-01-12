export type RuntimeConfig = {
  apiBaseUrl: string;
  apiKey: string;
  tenantId: string;
  principalId: string;
  principalRole: string;
  traceUrlTemplate: string;
};

const defaultConfig: RuntimeConfig = {
  apiBaseUrl: "",
  apiKey: "",
  tenantId: "default",
  principalId: "",
  principalRole: "",
  traceUrlTemplate: "",
};

function envConfig(): Partial<RuntimeConfig> {
  const base = import.meta.env.VITE_API_BASE_URL as string | undefined;
  const apiKey = import.meta.env.VITE_API_KEY as string | undefined;
  const tenantId = import.meta.env.VITE_TENANT_ID as string | undefined;
  const principalId = import.meta.env.VITE_PRINCIPAL_ID as string | undefined;
  const principalRole = import.meta.env.VITE_PRINCIPAL_ROLE as string | undefined;
  const traceUrlTemplate = import.meta.env.VITE_TRACE_URL_TEMPLATE as string | undefined;
  return {
    apiBaseUrl: base?.trim() || undefined,
    apiKey: apiKey?.trim() || undefined,
    tenantId: tenantId?.trim() || undefined,
    principalId: principalId?.trim() || undefined,
    principalRole: principalRole?.trim() || undefined,
    traceUrlTemplate: traceUrlTemplate?.trim() || undefined,
  };
}

export async function loadRuntimeConfig(): Promise<RuntimeConfig> {
  const fromEnv = envConfig();
  try {
    const res = await fetch("/config.json", { cache: "no-store" });
    if (res.ok) {
      const data = (await res.json()) as Partial<RuntimeConfig>;
      return {
        ...defaultConfig,
        ...fromEnv,
        ...data,
      };
    }
  } catch {
    // Ignore and fall back to env + defaults.
  }
  return {
    ...defaultConfig,
    ...fromEnv,
  };
}
