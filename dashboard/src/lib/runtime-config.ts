import { useConfigStore } from "../state/config";

// ---------------------------------------------------------------------------
// Runtime config loader (from /config.json)
// ---------------------------------------------------------------------------

interface RuntimeConfig {
  apiBaseUrl?: string;
  apiKey?: string;
  tenantId?: string;
  principalId?: string;
  principalRole?: string;
  traceUrlTemplate?: string;
}

const CONFIG_PATH = "/config.json";
const CONFIG_TIMEOUT_MS = 2000;

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0;
}

function normalizeBaseUrl(value: unknown): string | undefined {
  if (!isNonEmptyString(value)) return undefined;
  const trimmed = value.trim();
  if (trimmed.startsWith("/")) {
    return trimmed.replace(/\/+$/, "") || "/";
  }
  try {
    const url = new URL(trimmed);
    if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
    return trimmed.replace(/\/+$/, "");
  } catch {
    return undefined;
  }
}

function normalizeSimpleString(value: unknown): string | undefined {
  if (!isNonEmptyString(value)) return undefined;
  return value.trim();
}

function normalizeTemplate(value: unknown): string | undefined {
  if (!isNonEmptyString(value)) return undefined;
  const trimmed = value.trim();
  const lower = trimmed.toLowerCase();
  if (lower.startsWith("javascript:") || lower.startsWith("data:")) return undefined;
  return trimmed;
}

async function fetchWithTimeout(url: string, timeoutMs: number): Promise<Response | null> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(url, {
      signal: controller.signal,
      cache: "no-store",
      headers: { Accept: "application/json" },
    });
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

function parseRuntimeConfig(raw: Record<string, unknown>): RuntimeConfig {
  const cfg: RuntimeConfig = {};

  const apiBaseUrl = normalizeBaseUrl(raw.apiBaseUrl);
  if (apiBaseUrl) cfg.apiBaseUrl = apiBaseUrl;

  const apiKey = normalizeSimpleString(raw.apiKey);
  if (apiKey) cfg.apiKey = apiKey;

  const tenantId = normalizeSimpleString(raw.tenantId);
  if (tenantId) cfg.tenantId = tenantId;

  const principalId = normalizeSimpleString(raw.principalId);
  if (principalId) cfg.principalId = principalId;

  const principalRole = normalizeSimpleString(raw.principalRole);
  if (principalRole) cfg.principalRole = principalRole;

  const traceUrlTemplate = normalizeTemplate(raw.traceUrlTemplate);
  if (traceUrlTemplate) cfg.traceUrlTemplate = traceUrlTemplate;

  return cfg;
}

export async function loadRuntimeConfig(): Promise<void> {
  if (typeof window === "undefined") return;

  const res = await fetchWithTimeout(CONFIG_PATH, CONFIG_TIMEOUT_MS);
  if (!res || !res.ok) return;

  let raw: unknown;
  try {
    raw = await res.json();
  } catch {
    return;
  }

  if (!raw || typeof raw !== "object") return;
  const patch = parseRuntimeConfig(raw as Record<string, unknown>);

  if (Object.keys(patch).length === 0) return;
  useConfigStore.getState().update(patch);
}
