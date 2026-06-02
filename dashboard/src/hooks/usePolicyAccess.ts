import { useMemo } from "react";
import { useAuthConfig } from "./useAuthConfig";
import { useConfigStore } from "../state/config";

const READ_ONLY_ROLES = new Set(["viewer", "auditor", "readonly", "read_only"]);
const EDIT_ROLES = new Set(["admin", "operator", "secops", "editor", "policy_editor", "owner"]);
const PUBLISH_ROLES = new Set(["admin", "operator", "secops", "publisher", "policy_publisher", "owner"]);
const RELEASE_ROLES = new Set(["admin", "operator", "secops", "release_manager", "owner"]);

function normalizeRole(value: string): string {
  return value.trim().toLowerCase();
}

export interface PolicyAccess {
  canEdit: boolean;
  canPublish: boolean;
  canRelease: boolean;
  canManageOutputRules: boolean;
  canManageTenants: boolean;
  isReadOnly: boolean;
  requiresAuth: boolean;
  userRoles: string[];
  principalRole: string;
}

export interface DerivePolicyAccessInput {
  requiresAuth: boolean;
  roles: string[];
  principalRole?: string;
}

export function derivePolicyAccess(input: DerivePolicyAccessInput): PolicyAccess {
  const normalizedRoles = new Set(
    [...input.roles, input.principalRole ?? ""]
      .map((role) => normalizeRole(role ?? ""))
      .filter(Boolean),
  );
  const roles = [...normalizedRoles];

  if (!input.requiresAuth) {
    return {
      canEdit: true,
      canPublish: true,
      canRelease: true,
      canManageOutputRules: true,
      canManageTenants: true,
      isReadOnly: false,
      requiresAuth: input.requiresAuth,
      userRoles: roles,
      principalRole: normalizeRole(input.principalRole ?? ""),
    };
  }

  const hasAnyRole = (allowed: Set<string>) => roles.some((role) => allowed.has(role));
  const isReadOnly =
    roles.length > 0 &&
    roles.every((role) => READ_ONLY_ROLES.has(role));

  return {
    canEdit: !isReadOnly && hasAnyRole(EDIT_ROLES),
    canPublish: !isReadOnly && hasAnyRole(PUBLISH_ROLES),
    canRelease: !isReadOnly && hasAnyRole(RELEASE_ROLES),
    canManageOutputRules: !isReadOnly && hasAnyRole(EDIT_ROLES),
    canManageTenants: !isReadOnly && hasAnyRole(EDIT_ROLES),
    isReadOnly,
    requiresAuth: input.requiresAuth,
    userRoles: roles,
    principalRole: normalizeRole(input.principalRole ?? ""),
  };
}

export function usePolicyAccess(): PolicyAccess {
  const { data: authConfig } = useAuthConfig();
  const user = useConfigStore((s) => s.user);
  const principalRole = useConfigStore((s) => s.principalRole);

  return useMemo(() => {
    // Auth config not yet loaded (or the /auth/config fetch failed) — fail
    // CLOSED, mirroring usePermission. We don't yet know whether auth is
    // required, so deny every privileged capability rather than paint
    // affordances we'd have to retract. The no-auth "allow everything" branch
    // (derivePolicyAccess with requiresAuth=false) must only trigger once the
    // config has RESOLVED with every mode confirmed disabled — never on the
    // undefined window. The deny is unconditional (role-independent), so an
    // admin mid-load is denied too, exactly as usePermission denies them.
    if (!authConfig) {
      return {
        canEdit: false,
        canPublish: false,
        canRelease: false,
        canManageOutputRules: false,
        canManageTenants: false,
        isReadOnly: false,
        requiresAuth: true,
        userRoles: (user?.roles ?? []).map(normalizeRole).filter(Boolean),
        principalRole: normalizeRole(principalRole ?? ""),
      };
    }
    const requiresAuth = Boolean(
      authConfig.password_enabled ||
        !!authConfig.user_auth_enabled ||
        authConfig.saml_enabled ||
        authConfig.oidc_enabled,
    );
    return derivePolicyAccess({
      requiresAuth,
      roles: user?.roles ?? [],
      principalRole,
    });
  }, [authConfig, user?.roles, principalRole]);
}
