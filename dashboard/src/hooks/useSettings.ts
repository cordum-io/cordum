import { useMemo, useRef } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, del, put } from "../api/client";
import { logger } from "../lib/logger";
import { useToastStore } from "../state/toast";
import type {
  ApiKey,
  User,
  ApiResponse,
  AuthConfig,
  NotificationChannel,
  NotificationRule,
  Environment,
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
    return fallback;
  }
}

function writeLocalStorage<T>(key: string, value: T): void {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // localStorage quota exceeded — silently ignore
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
