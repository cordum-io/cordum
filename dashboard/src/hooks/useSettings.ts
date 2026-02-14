import { useMemo, useRef } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError, get, post, del, put } from "../api/client";
import { logger } from "../lib/logger";
import { useToastStore } from "../state/toast";
import type {
  ApiKey,
  User,
  ApiResponse,
  ChangePasswordPayload,
  ResetUserPasswordPayload,
  AuthConfig,
  NotificationChannel,
  NotificationRule,
  Environment,
  McpConfig,
  McpResource,
  McpStatus,
  McpTool,
  GeneralConfig,
  MaintenanceWindow,
  MaintenanceSchedule,
} from "../api/types";
import type { ComboboxSuggestion } from "../components/ui/ComboboxInput";

// ---------------------------------------------------------------------------
// System config
// ---------------------------------------------------------------------------

export interface SystemConfig {
  [key: string]: unknown;
}

export function useConfig() {
  return useQuery<SystemConfig>({
    queryKey: ["config"],
    queryFn: () => get<SystemConfig>("/config"),
    staleTime: 60_000,
  });
}

// ---------------------------------------------------------------------------
// Effective (merged) config
// ---------------------------------------------------------------------------

export interface EffectiveConfigParams {
  orgId?: string;
  teamId?: string;
  workflowId?: string;
  stepId?: string;
}

export function useEffectiveConfig(params?: EffectiveConfigParams) {
  const qs = new URLSearchParams();
  if (params?.orgId) qs.set("org_id", params.orgId);
  if (params?.teamId) qs.set("team_id", params.teamId);
  if (params?.workflowId) qs.set("workflow_id", params.workflowId);
  if (params?.stepId) qs.set("step_id", params.stepId);
  const query = qs.toString();
  return useQuery<Record<string, unknown>>({
    queryKey: ["effective-config", params ?? {}],
    queryFn: () => get<Record<string, unknown>>(`/config/effective${query ? "?" + query : ""}`),
    staleTime: 10_000,
  });
}

export function useSetConfig() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, Partial<SystemConfig>>({
    mutationFn: (patch) => {
      logger.info("settings", "Updating system config");
      return post<void>("/config", patch);
    },
    onSuccess: () => {
      logger.info("settings", "System config updated");
      useToastStore.getState().addToast({ type: "success", title: "Settings saved" });
      queryClient.invalidateQueries({ queryKey: ["config"] });
    },
    onError: (err) => {
      logger.error("settings", "System config update failed", { error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to save settings", description: err.message });
    },
  });
}

// ---------------------------------------------------------------------------
// Topics (extracted from system config pool mappings)
// ---------------------------------------------------------------------------

export function useTopics(): ComboboxSuggestion[] {
  const { data: config } = useConfig();

  return useMemo(() => {
    if (!config) return [];
    // config.pools is expected to be Record<poolName, { topics: string[] }> or
    // config.topics is Record<topicName, poolName>
    const suggestions: ComboboxSuggestion[] = [];

    // Format 1: flat topic→pool map (e.g. { "job.default": "default-pool" })
    const topics = config.topics as Record<string, string> | undefined;
    if (topics && typeof topics === "object") {
      for (const [topic, pool] of Object.entries(topics)) {
        if (typeof topic === "string" && typeof pool === "string") {
          suggestions.push({ value: topic, label: topic, description: `pool: ${pool}` });
        }
      }
    }

    // Format 2: pools with topic arrays (e.g. { pools: { "default-pool": { topics: ["job.default"] } } })
    const pools = config.pools as Record<string, { topics?: string[] }> | undefined;
    if (pools && typeof pools === "object") {
      const seen = new Set(suggestions.map((s) => s.value));
      for (const [poolName, poolCfg] of Object.entries(pools)) {
        if (Array.isArray(poolCfg?.topics)) {
          for (const topic of poolCfg.topics) {
            if (typeof topic === "string" && !seen.has(topic)) {
              seen.add(topic);
              suggestions.push({ value: topic, label: topic, description: `pool: ${poolName}` });
            }
          }
        }
      }
    }

    return suggestions;
  }, [config]);
}

// ---------------------------------------------------------------------------
// Auth config (re-export for consistency)
// ---------------------------------------------------------------------------

export { useAuthConfig } from "./useAuthConfig";

export function useAuthConfigAdmin() {
  return useQuery<AuthConfig>({
    queryKey: ["auth-config-admin"],
    queryFn: () => get<AuthConfig>("/auth/config"),
    staleTime: 60_000,
  });
}

// ---------------------------------------------------------------------------
// API keys
// ---------------------------------------------------------------------------

export function useApiKeys() {
  return useQuery<ApiResponse<ApiKey[]>>({
    queryKey: ["api-keys"],
    queryFn: () => get<ApiResponse<ApiKey[]>>("/auth/keys"),
    staleTime: 30_000,
  });
}

interface CreateApiKeyInput {
  name: string;
  scopes: string[];
  expiresAt?: string;
}

interface CreateApiKeyResponse {
  key: ApiKey;
  secret: string;
}

export function useCreateApiKey() {
  const queryClient = useQueryClient();
  return useMutation<CreateApiKeyResponse, Error, CreateApiKeyInput>({
    mutationFn: (input) => {
      logger.info("settings", "Creating API key", { name: input.name });
      return post<CreateApiKeyResponse>("/auth/keys", input);
    },
    onSuccess: () => {
      logger.info("settings", "API key created");
      useToastStore.getState().addToast({ type: "success", title: "API key created" });
      queryClient.invalidateQueries({ queryKey: ["api-keys"] });
    },
    onError: (err) => {
      logger.error("settings", "API key creation failed", { error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to create API key", description: err.message });
    },
  });
}

export function useRevokeApiKey() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => {
      logger.info("settings", "Revoking API key", { id });
      return del(`/auth/keys/${id}`);
    },
    onSuccess: (_, id) => {
      logger.info("settings", "API key revoked", { id });
      useToastStore.getState().addToast({ type: "success", title: "API key revoked" });
      queryClient.invalidateQueries({ queryKey: ["api-keys"] });
    },
    onError: (err, id) => {
      logger.error("settings", "API key revocation failed", { id, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to revoke key", description: err.message });
    },
  });
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

export function useUsers() {
  return useQuery<ApiResponse<User[]>>({
    queryKey: ["users"],
    queryFn: () => get<ApiResponse<User[]>>("/users"),
    staleTime: 30_000,
  });
}

interface CreateUserInput {
  username: string;
  password: string;
  role: string;
}

export function useCreateUser() {
  const queryClient = useQueryClient();
  return useMutation<User, Error, CreateUserInput>({
    mutationFn: (input) => {
      logger.info("settings", "Creating user", { username: input.username });
      return post<User>("/users", input);
    },
    onSuccess: () => {
      logger.info("settings", "User created");
      useToastStore.getState().addToast({ type: "success", title: "User created" });
      queryClient.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (err) => {
      logger.error("settings", "User creation failed", { error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to create user", description: err.message });
    },
  });
}

interface UpdateUserInput {
  id: string;
  data: Partial<Pick<User, "email" | "display_name" | "roles">>;
}

export function useUpdateUser() {
  const queryClient = useQueryClient();
  return useMutation<User, Error, UpdateUserInput>({
    mutationFn: ({ id, data }) => {
      logger.info("settings", "Updating user", { id });
      return put<User>(`/users/${id}`, data);
    },
    onSuccess: (_, { id }) => {
      logger.info("settings", "User updated", { id });
      useToastStore.getState().addToast({ type: "success", title: "User updated" });
      queryClient.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (err, { id }) => {
      logger.error("settings", "User update failed", { id, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to update user", description: err.message });
    },
  });
}

export function useDeleteUser() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => {
      logger.info("settings", "Deleting user", { id });
      return del(`/users/${id}`);
    },
    onSuccess: (_, id) => {
      logger.info("settings", "User deleted", { id });
      useToastStore.getState().addToast({ type: "success", title: "User deleted" });
      queryClient.invalidateQueries({ queryKey: ["users"] });
    },
    onError: (err, id) => {
      logger.error("settings", "User delete failed", { id, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to delete user", description: err.message });
    },
  });
}

interface ResetUserPasswordInput extends ResetUserPasswordPayload {
  userId: string;
}

export function useChangePassword() {
  return useMutation<void, ApiError, ChangePasswordPayload>({
    mutationFn: (input) => {
      logger.info("settings", "Changing current user password");
      return post<void>("/auth/password", input);
    },
  });
}

export function useResetUserPassword() {
  return useMutation<void, ApiError, ResetUserPasswordInput>({
    mutationFn: ({ userId, password }) => {
      logger.info("settings", "Resetting user password", { userId });
      return post<void>(`/users/${encodeURIComponent(userId)}/password`, { password });
    },
  });
}

// ---------------------------------------------------------------------------
// Notification localStorage keys (fallback when backend has no notification data)
// ---------------------------------------------------------------------------

const LS_CHANNELS_KEY = "cordum-notification-channels";
const LS_RULES_KEY = "cordum-notification-rules";

function readLocalStorage<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return fallback;
    return JSON.parse(raw) as T;
  } catch {
    logger.debug("settings", "localStorage JSON parse failed, using fallback");
    return fallback;
  }
}

function writeLocalStorage<T>(key: string, value: T): void {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    logger.debug("settings", "localStorage write failed, quota may be exceeded");
  }
}

// ---------------------------------------------------------------------------
// Notification channels (derived from config with localStorage fallback)
// ---------------------------------------------------------------------------

export function useNotificationChannels() {
  const { data: config, isLoading } = useConfig();

  const channels = useMemo<NotificationChannel[]>(() => {
    if (!config) return readLocalStorage<NotificationChannel[]>(LS_CHANNELS_KEY, []);
    const raw = (config as Record<string, unknown>).notifications as
      | { channels?: unknown[] }
      | undefined;
    if (!Array.isArray(raw?.channels) || raw.channels.length === 0) {
      return readLocalStorage<NotificationChannel[]>(LS_CHANNELS_KEY, []);
    }
    return raw.channels as NotificationChannel[];
  }, [config]);

  return { data: channels, isLoading };
}

export function useNotificationRules() {
  const { data: config, isLoading } = useConfig();

  const rules = useMemo<NotificationRule[]>(() => {
    if (!config) return readLocalStorage<NotificationRule[]>(LS_RULES_KEY, []);
    const raw = (config as Record<string, unknown>).notifications as
      | { rules?: unknown[] }
      | undefined;
    if (!Array.isArray(raw?.rules) || raw.rules.length === 0) {
      return readLocalStorage<NotificationRule[]>(LS_RULES_KEY, []);
    }
    return raw.rules as NotificationRule[];
  }, [config]);

  return { data: rules, isLoading };
}

export function useSaveNotificationChannel() {
  const setConfig = useSetConfig();
  const queryClient = useQueryClient();
  const { data: channels } = useNotificationChannels();
  const channelsRef = useRef(channels);
  channelsRef.current = channels;

  return useMutation<NotificationChannel[], Error, NotificationChannel>({
    mutationFn: async (channel) => {
      const existing = channelsRef.current ?? [];
      const idx = existing.findIndex((c) => c.id === channel.id);
      const updated = idx >= 0
        ? existing.map((c, i) => (i === idx ? channel : c))
        : [...existing, channel];
      logger.info("settings", "Saving notification channel", { id: channel.id });
      await setConfig.mutateAsync({ notifications: { channels: updated } });
      return updated;
    },
    onSuccess: (updated) => {
      writeLocalStorage(LS_CHANNELS_KEY, updated);
      queryClient.invalidateQueries({ queryKey: ["config"] });
    },
    onError: (err) => {
      logger.error("settings", "Failed to save notification channel", { error: err.message });
      useToastStore.getState().addToast({
        type: "error",
        title: "Failed to save channel",
        description: err.message,
      });
      queryClient.invalidateQueries({ queryKey: ["config"] });
    },
  });
}

export function useDeleteNotificationChannel() {
  const setConfig = useSetConfig();
  const queryClient = useQueryClient();
  const { data: channels } = useNotificationChannels();
  const channelsRef = useRef(channels);
  channelsRef.current = channels;

  return useMutation<NotificationChannel[], Error, string>({
    mutationFn: async (id) => {
      const updated = (channelsRef.current ?? []).filter((c) => c.id !== id);
      logger.info("settings", "Deleting notification channel", { id });
      await setConfig.mutateAsync({ notifications: { channels: updated } });
      return updated;
    },
    onSuccess: (updated) => {
      writeLocalStorage(LS_CHANNELS_KEY, updated);
      queryClient.invalidateQueries({ queryKey: ["config"] });
    },
    onError: (err) => {
      logger.error("settings", "Failed to delete notification channel", { error: err.message });
      useToastStore.getState().addToast({
        type: "error",
        title: "Failed to delete channel",
        description: err.message,
      });
      queryClient.invalidateQueries({ queryKey: ["config"] });
    },
  });
}

export function useSaveNotificationRules() {
  const setConfig = useSetConfig();
  const queryClient = useQueryClient();

  return useMutation<void, Error, NotificationRule[]>({
    mutationFn: (rules) => {
      logger.info("settings", "Saving notification rules");
      writeLocalStorage(LS_RULES_KEY, rules);
      return setConfig.mutateAsync({ notifications: { rules } });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["config"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Environments (derived from config — backend endpoint TBD)
// ---------------------------------------------------------------------------

const DEFAULT_ENVIRONMENT: Environment = {
  id: "production",
  name: "production",
  status: "active",
  config: {},
};

export function useEnvironments() {
  const { data: config, isLoading } = useConfig();

  const environments = useMemo<Environment[]>(() => {
    if (!config) return [DEFAULT_ENVIRONMENT];
    const raw = (config as Record<string, unknown>).environments;
    if (!Array.isArray(raw) || raw.length === 0) return [DEFAULT_ENVIRONMENT];
    return raw as Environment[];
  }, [config]);

  return { data: environments, isLoading };
}

export function useSaveEnvironment() {
  const setConfig = useSetConfig();
  const { data: environments } = useEnvironments();

  return useMutation<void, Error, Environment>({
    mutationFn: (env) => {
      const existing = environments ?? [];
      const idx = existing.findIndex((e) => e.id === env.id);
      const updated = idx >= 0
        ? existing.map((e, i) => (i === idx ? env : e))
        : [...existing, env];
      logger.info("settings", "Saving environment", { id: env.id });
      return setConfig.mutateAsync({ environments: updated });
    },
  });
}

// ---------------------------------------------------------------------------
// MCP config + catalog
// ---------------------------------------------------------------------------

const DEFAULT_MCP_CONFIG: McpConfig = {
  enabled: false,
  transport: "http",
  port: 3001,
  requireAuth: true,
  allowedOrigins: [], // Empty by default — operator must configure allowed origins explicitly
  tools: {},
  resources: {},
};

const MCP_TOOL_CATALOG: Array<Omit<McpTool, "enabled">> = [
  {
    name: "cordum_submit_job",
    description: "Submit a new Cordum job",
    inputSchema: {
      type: "object",
      required: ["topic", "prompt"],
      properties: {
        topic: { type: "string" },
        prompt: { type: "string" },
        priority: { type: "string", enum: ["low", "normal", "high", "critical"] },
        risk_tags: { type: "array", items: { type: "string" } },
      },
    },
  },
  {
    name: "cordum_cancel_job",
    description: "Cancel an in-progress job",
    inputSchema: {
      type: "object",
      required: ["job_id"],
      properties: {
        job_id: { type: "string" },
        reason: { type: "string" },
      },
    },
  },
  {
    name: "cordum_trigger_workflow",
    description: "Start a workflow run",
    inputSchema: {
      type: "object",
      required: ["workflow_id"],
      properties: {
        workflow_id: { type: "string" },
        input: { type: "object" },
      },
    },
  },
  {
    name: "cordum_approve_job",
    description: "Approve a pending job",
    inputSchema: {
      type: "object",
      required: ["job_id"],
      properties: {
        job_id: { type: "string" },
        reason: { type: "string" },
      },
    },
  },
  {
    name: "cordum_reject_job",
    description: "Reject a pending job",
    inputSchema: {
      type: "object",
      required: ["job_id"],
      properties: {
        job_id: { type: "string" },
        reason: { type: "string" },
      },
    },
  },
  {
    name: "cordum_query_policy",
    description: "Simulate policy evaluation for request metadata",
    inputSchema: {
      type: "object",
      required: ["topic"],
      properties: {
        topic: { type: "string" },
        capability: { type: "string" },
        risk_tags: { type: "array", items: { type: "string" } },
      },
    },
  },
];

type McpResourceCatalogEntry = Omit<McpResource, "enabled"> & { key: string };

const MCP_RESOURCE_CATALOG: McpResourceCatalogEntry[] = [
  {
    key: "jobs",
    uri: "cordum://jobs/{id}",
    name: "Jobs",
    description: "Fetch job details and status",
    mimeType: "application/json",
  },
  {
    key: "workflows",
    uri: "cordum://workflows/{id}/runs",
    name: "Workflow Runs",
    description: "List workflow run status and recent history",
    mimeType: "application/json",
  },
  {
    key: "audit",
    uri: "cordum://audit?limit={n}",
    name: "Audit Log",
    description: "Read recent audit entries",
    mimeType: "application/json",
  },
  {
    key: "health",
    uri: "cordum://health",
    name: "Health",
    description: "Read control-plane health status",
    mimeType: "application/json",
  },
  {
    key: "policies",
    uri: "cordum://policies",
    name: "Policies",
    description: "List active safety policy bundles",
    mimeType: "application/json",
  },
];

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  return value as Record<string, unknown>;
}

function asBool(value: unknown, fallback: boolean): boolean {
  if (typeof value === "boolean") return value;
  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    if (["1", "true", "yes", "on"].includes(normalized)) return true;
    if (["0", "false", "no", "off"].includes(normalized)) return false;
  }
  return fallback;
}

function asNumber(value: unknown, fallback: number): number {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return fallback;
}

function asString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function asStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((item) => (typeof item === "string" ? item.trim() : ""))
    .filter(Boolean);
}

function normalizeMcpTransport(value: unknown): McpConfig["transport"] {
  const normalized = asString(value).toLowerCase();
  if (normalized === "stdio" || normalized === "both") return normalized;
  return "http";
}

function readMcpValue(config: Record<string, unknown>, key: string): unknown {
  const mcp = asRecord(config.mcp);
  if (mcp && key in mcp) return mcp[key];
  return config[`mcp.${key}`];
}

function parseToggleMap(value: unknown): Record<string, { enabled: boolean }> {
  const record = asRecord(value);
  if (!record) return {};
  const parsed: Record<string, { enabled: boolean }> = {};
  for (const [key, rawValue] of Object.entries(record)) {
    const valueObj = asRecord(rawValue);
    parsed[key] = { enabled: asBool(valueObj?.enabled, true) };
  }
  return parsed;
}

function resolveMcpConfig(config: SystemConfig | undefined): McpConfig {
  const raw = (config ?? {}) as Record<string, unknown>;
  const allowedOriginsRaw =
    readMcpValue(raw, "allowedOrigins") ??
    readMcpValue(raw, "allowed_origins");
  const allowedOrigins = asStringArray(allowedOriginsRaw);
  return {
    enabled: asBool(readMcpValue(raw, "enabled"), DEFAULT_MCP_CONFIG.enabled),
    transport: normalizeMcpTransport(readMcpValue(raw, "transport")),
    port: asNumber(readMcpValue(raw, "port"), DEFAULT_MCP_CONFIG.port),
    requireAuth: asBool(
      readMcpValue(raw, "requireAuth") ?? readMcpValue(raw, "require_auth"),
      DEFAULT_MCP_CONFIG.requireAuth,
    ),
    allowedOrigins: allowedOrigins.length > 0 ? allowedOrigins : DEFAULT_MCP_CONFIG.allowedOrigins,
    apiKeyMasked:
      asString(readMcpValue(raw, "apiKeyMasked") ?? readMcpValue(raw, "api_key_masked")) || undefined,
    tools: parseToggleMap(readMcpValue(raw, "tools")),
    resources: parseToggleMap(readMcpValue(raw, "resources")),
  };
}

function mergeMcpConfig(base: McpConfig, patch: Partial<McpConfig>): McpConfig {
  return {
    ...base,
    ...patch,
    tools: patch.tools ?? base.tools,
    resources: patch.resources ?? base.resources,
    allowedOrigins: patch.allowedOrigins ?? base.allowedOrigins,
  };
}

function toMcpPayload(config: McpConfig): Record<string, unknown> {
  return {
    mcp: {
      enabled: config.enabled,
      transport: config.transport,
      port: config.port,
      requireAuth: config.requireAuth,
      allowedOrigins: config.allowedOrigins,
      tools: config.tools,
      resources: config.resources,
    },
  };
}

export function useMcpConfig() {
  const { data: config, isLoading } = useConfig();
  const mcpConfig = useMemo(() => resolveMcpConfig(config), [config]);
  return { data: mcpConfig, isLoading };
}

export function useSetMcpConfig() {
  const setConfig = useSetConfig();
  const { data: currentMcp } = useMcpConfig();

  return useMutation<void, Error, Partial<McpConfig>>({
    mutationFn: (patch) => {
      const merged = mergeMcpConfig(currentMcp ?? DEFAULT_MCP_CONFIG, patch);
      logger.info("settings", "Updating MCP config", {
        enabled: merged.enabled,
        transport: merged.transport,
      });
      return setConfig.mutateAsync(toMcpPayload(merged));
    },
  });
}

export function useMcpStatus() {
  const { data: mcpConfig, isLoading: mcpConfigLoading } = useMcpConfig();
  const mcpEnabled = Boolean(mcpConfig?.enabled);
  const transport = String(mcpConfig?.transport ?? "http").toLowerCase();
  const statusSupported = mcpEnabled && (transport === "http" || transport === "both");

  return useQuery<McpStatus>({
    queryKey: ["mcp-status", mcpEnabled, transport],
    enabled: !mcpConfigLoading && statusSupported,
    initialData: {
      running: false,
      connectedClients: 0,
      uptime: 0,
    },
    initialDataUpdatedAt: 0,
    queryFn: async () => {
      try {
        const response = await get<{
          running?: boolean;
          connected_clients?: number;
          uptime_seconds?: number;
          transport?: string;
          enabled_tools?: number;
          enabled_resources?: number;
        }>("/mcp/status");
        return {
          running: !!response.running,
          connectedClients: Number(response.connected_clients ?? 0),
          uptime: Number(response.uptime_seconds ?? 0),
          transport: response.transport,
          enabledTools: Number(response.enabled_tools ?? 0),
          enabledResources: Number(response.enabled_resources ?? 0),
        };
      } catch (error) {
        if (error instanceof ApiError && error.status === 404) {
          return {
            running: false,
            connectedClients: 0,
            uptime: 0,
          };
        }
        throw error;
      }
    },
    staleTime: 10_000,
  });
}

export function useMcpTools() {
  const { data: mcpConfig, isLoading } = useMcpConfig();
  const items = useMemo<McpTool[]>(() => {
    const tools = mcpConfig?.tools ?? {};
    return MCP_TOOL_CATALOG.map((tool) => ({
      ...tool,
      enabled: tools[tool.name]?.enabled ?? true,
    }));
  }, [mcpConfig]);
  return { data: items, isLoading };
}

export function useMcpResources() {
  const { data: mcpConfig, isLoading } = useMcpConfig();
  const items = useMemo<McpResource[]>(() => {
    const resources = mcpConfig?.resources ?? {};
    return MCP_RESOURCE_CATALOG.map((resource) => ({
      uri: resource.uri,
      name: resource.name,
      description: resource.description,
      mimeType: resource.mimeType,
      enabled: resources[resource.key]?.enabled ?? true,
    }));
  }, [mcpConfig]);
  return { data: items, isLoading };
}

// ---------------------------------------------------------------------------
// General config (derived from config — maps to top-level config fields)
// ---------------------------------------------------------------------------

const GENERAL_CONFIG_DEFAULTS: GeneralConfig = {
  safetyStance: "balanced",
  approvalTimeoutMs: 900_000,
  autoDenyOnTimeout: false,
  logRetentionDays: 30,
  auditRetentionDays: 90,
  dlqRetentionDays: 14,
  rateLimitPerKey: 600,
  concurrentJobsLimit: 100,
  wsConnectionsLimit: 50,
  maintenanceMode: false,
};

export function useGeneralConfig() {
  const { data: config, isLoading } = useConfig();

  const generalConfig = useMemo<GeneralConfig>(() => {
    if (!config) return GENERAL_CONFIG_DEFAULTS;
    const raw = config as Record<string, unknown>;
    return {
      safetyStance: (raw.safetyStance as GeneralConfig["safetyStance"]) ?? GENERAL_CONFIG_DEFAULTS.safetyStance,
      approvalTimeoutMs: (raw.approvalTimeoutMs as number) ?? (raw.approvalSlaMs as number) ?? GENERAL_CONFIG_DEFAULTS.approvalTimeoutMs,
      autoDenyOnTimeout: (raw.autoDenyOnTimeout as boolean) ?? GENERAL_CONFIG_DEFAULTS.autoDenyOnTimeout,
      logRetentionDays: (raw.logRetentionDays as number) ?? GENERAL_CONFIG_DEFAULTS.logRetentionDays,
      auditRetentionDays: (raw.auditRetentionDays as number) ?? GENERAL_CONFIG_DEFAULTS.auditRetentionDays,
      dlqRetentionDays: (raw.dlqRetentionDays as number) ?? GENERAL_CONFIG_DEFAULTS.dlqRetentionDays,
      rateLimitPerKey: (raw.rateLimitPerKey as number) ?? GENERAL_CONFIG_DEFAULTS.rateLimitPerKey,
      concurrentJobsLimit: (raw.concurrentJobsLimit as number) ?? GENERAL_CONFIG_DEFAULTS.concurrentJobsLimit,
      wsConnectionsLimit: (raw.wsConnectionsLimit as number) ?? GENERAL_CONFIG_DEFAULTS.wsConnectionsLimit,
      maintenanceMode: (raw.maintenanceMode as boolean) ?? GENERAL_CONFIG_DEFAULTS.maintenanceMode,
      maintenanceMessage: raw.maintenanceMessage as string | undefined,
      maintenanceStartedAt: raw.maintenanceStartedAt as string | undefined,
      maintenanceHistory: Array.isArray(raw.maintenanceHistory) ? raw.maintenanceHistory as MaintenanceWindow[] : [],
      maintenanceSchedule: Array.isArray(raw.maintenanceSchedule) ? raw.maintenanceSchedule as MaintenanceSchedule[] : [],
    };
  }, [config]);

  return { data: generalConfig, isLoading };
}

export function useSetGeneralConfig() {
  const setConfig = useSetConfig();

  return useMutation<void, Error, Partial<GeneralConfig>>({
    mutationFn: (patch) => {
      logger.info("settings", "Updating general config");
      return setConfig.mutateAsync(patch as Partial<SystemConfig>);
    },
  });
}

/** @internal exported for unit tests */
export const __settingsInternal = {
  readLocalStorage,
  writeLocalStorage,
  DEFAULT_ENVIRONMENT,
  GENERAL_CONFIG_DEFAULTS,
  DEFAULT_MCP_CONFIG,
  MCP_TOOL_CATALOG,
  MCP_RESOURCE_CATALOG,
  resolveMcpConfig,
  mergeMcpConfig,
  toMcpPayload,
};
