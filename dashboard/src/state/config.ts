import { create } from "zustand";
import type { RuntimeConfig } from "../lib/runtime-config";

const STORAGE_KEY = "cordum.dashboard.config";

type ConfigState = RuntimeConfig & {
  loaded: boolean;
  defaults: RuntimeConfig;
  init: (runtime: RuntimeConfig) => void;
  update: (patch: Partial<RuntimeConfig>) => void;
  reset: () => void;
};

function readStored(): Partial<RuntimeConfig> {
  if (typeof window === "undefined") {
    return {};
  }
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) {
      return {};
    }
    const data = JSON.parse(raw) as Partial<RuntimeConfig>;
    return data || {};
  } catch {
    return {};
  }
}

function writeStored(cfg: RuntimeConfig) {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(cfg));
  } catch {
    // Ignore storage errors.
  }
}

function mergeOverrides(base: RuntimeConfig, overrides: Partial<RuntimeConfig>): RuntimeConfig {
  return {
    apiBaseUrl: base.apiBaseUrl?.trim() || overrides.apiBaseUrl?.trim() || "",
    apiKey: overrides.apiKey?.trim() || base.apiKey,
    tenantId: overrides.tenantId?.trim() || base.tenantId,
    principalId: overrides.principalId?.trim() || base.principalId,
    principalRole: overrides.principalRole?.trim() || base.principalRole,
    traceUrlTemplate: overrides.traceUrlTemplate?.trim() || base.traceUrlTemplate,
  };
}

export const useConfigStore = create<ConfigState>((set, get) => ({
  apiBaseUrl: "",
  apiKey: "",
  tenantId: "default",
  principalId: "",
  principalRole: "",
  traceUrlTemplate: "",
  loaded: false,
  defaults: {
    apiBaseUrl: "",
    apiKey: "",
    tenantId: "default",
    principalId: "",
    principalRole: "",
    traceUrlTemplate: "",
  },
  init: (runtime) => {
    const stored = readStored();
    const merged = mergeOverrides(runtime, stored);
    set({
      ...merged,
      defaults: runtime,
      loaded: true,
    });
    writeStored(merged);
  },
  update: (patch) => {
    const current = get();
    const updated: RuntimeConfig = {
      apiBaseUrl: patch.apiBaseUrl?.trim() ?? current.apiBaseUrl,
      apiKey: patch.apiKey?.trim() ?? current.apiKey,
      tenantId: patch.tenantId?.trim() ?? current.tenantId,
      principalId: patch.principalId?.trim() ?? current.principalId,
      principalRole: patch.principalRole?.trim() ?? current.principalRole,
      traceUrlTemplate: patch.traceUrlTemplate?.trim() ?? current.traceUrlTemplate,
    };
    writeStored(updated);
    set(updated);
  },
  reset: () => {
    const defaults = get().defaults;
    writeStored(defaults);
    set(defaults);
  },
}));
