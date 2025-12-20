import { readJSON, writeJSON } from "./storage";

export type StudioSettings = {
  apiBase: string;
  wsBase: string;
  apiKey: string;
};

const settingsKey = "cortexos.settings.v1";

function normalizeBaseURL(url: string): string {
  return url.replace(/\/+$/, "");
}

function getRuntimeConfig(): NonNullable<Window["__CORETEXOS_STUDIO_CONFIG__"]> {
  if (typeof window === "undefined") {
    return {};
  }
  return window.__CORETEXOS_STUDIO_CONFIG__ || {};
}

export function defaultSettings(): StudioSettings {
  const runtime = getRuntimeConfig();

  const originFallback = typeof window !== "undefined" ? window.location.origin : "http://localhost:8081";
  const apiBase =
    runtime.apiBase ||
    import.meta.env.VITE_API_BASE ||
    (import.meta.env.DEV ? "http://localhost:8081" : originFallback);

  const wsBase =
    runtime.wsBase ||
    import.meta.env.VITE_WS_BASE ||
    normalizeBaseURL(apiBase).replace(/^http/, "ws") + "/api/v1/stream";

  const apiKey = runtime.apiKey || import.meta.env.VITE_API_KEY || "";

  return {
    apiBase: normalizeBaseURL(apiBase),
    wsBase: normalizeBaseURL(wsBase),
    apiKey,
  };
}

export function loadSettings(): StudioSettings {
  return readJSON<StudioSettings>(settingsKey) ?? defaultSettings();
}

export function saveSettings(settings: StudioSettings) {
  writeJSON(settingsKey, settings);
}

export function resetSettings() {
  writeJSON(settingsKey, defaultSettings());
}
