import { useMemo } from "react";
import { useApiKeys, useUsers, useConfig, useNotificationChannels, useAuthConfigAdmin } from "./useSettings";
import { usePolicyBundles } from "./usePolicies";
import { useStatus } from "./useStatus";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ChecklistItem {
  id: string;
  label: string;
  route: string;
  completed: boolean;
  optional: boolean;
}

export interface SetupStatus {
  isNewInstall: boolean;
  items: ChecklistItem[];
  completedCount: number;
  totalRequired: number;
  dismissed: boolean;
  dismiss: () => void;
  isLoading: boolean;
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const LS_DISMISSED_KEY = "cordum-setup-dismissed";

// ---------------------------------------------------------------------------
// useSetupStatus
// ---------------------------------------------------------------------------

export function useSetupStatus(): SetupStatus {
  const { data: keysData, isLoading: keysLoading } = useApiKeys();
  const { data: usersData, isLoading: usersLoading } = useUsers();
  const { data: config, isLoading: configLoading } = useConfig();
  const { data: bundlesData, isLoading: bundlesLoading } = usePolicyBundles();
  const { data: statusData, isLoading: statusLoading } = useStatus();
  const { data: channels } = useNotificationChannels();
  const { data: authConfig } = useAuthConfigAdmin();

  const isLoading = keysLoading || usersLoading || configLoading || bundlesLoading || statusLoading;

  const dismissed = useMemo(() => {
    try {
      return localStorage.getItem(LS_DISMISSED_KEY) === "true";
    } catch {
      return false;
    }
  }, []);

  const items = useMemo<ChecklistItem[]>(() => {
    const apiKeys = keysData?.items ?? [];
    const users = usersData?.items ?? [];
    const bundles = bundlesData?.items ?? [];
    const workerCount = statusData?.workers?.count ?? 0;
    const safetyStance = config
      ? (config as Record<string, unknown>).safetyStance
      : undefined;

    return [
      {
        id: "api-key",
        label: "Create first API key",
        route: "/settings/keys",
        completed: apiKeys.length > 0,
        optional: false,
      },
      {
        id: "admin-password",
        label: "Configure admin password",
        route: "/settings/users",
        completed: users.length > 0,
        optional: false,
      },
      {
        id: "safety-stance",
        label: "Set safety stance",
        route: "/settings/config",
        completed: safetyStance !== undefined,
        optional: false,
      },
      {
        id: "policy-bundle",
        label: "Load first policy bundle",
        route: "/policies",
        completed: bundles.length > 0,
        optional: false,
      },
      {
        id: "worker-pool",
        label: "Connect first worker pool",
        route: "/settings/health",
        completed: workerCount > 0,
        optional: false,
      },
      {
        id: "sso",
        label: "Configure SSO (optional)",
        route: "/settings/users",
        completed: !!(authConfig?.saml_enabled || authConfig?.oidc_enabled),
        optional: true,
      },
      {
        id: "notifications",
        label: "Set up notifications (optional)",
        route: "/settings/notifications",
        completed: (channels ?? []).length > 0,
        optional: true,
      },
    ];
  }, [keysData, usersData, config, bundlesData, statusData, channels, authConfig]);

  const requiredItems = items.filter((i) => !i.optional);
  const completedCount = requiredItems.filter((i) => i.completed).length;
  const totalRequired = requiredItems.length;

  const isNewInstall = useMemo(() => {
    if (isLoading) return false;
    const apiKeys = keysData?.items ?? [];
    const users = usersData?.items ?? [];
    const bundles = bundlesData?.items ?? [];
    return apiKeys.length === 0 && users.length <= 1 && bundles.length === 0;
  }, [isLoading, keysData, usersData, bundlesData]);

  function dismiss() {
    try {
      localStorage.setItem(LS_DISMISSED_KEY, "true");
    } catch {
      // ignore
    }
  }

  return {
    isNewInstall,
    items,
    completedCount,
    totalRequired,
    dismissed,
    dismiss,
    isLoading,
  };
}
