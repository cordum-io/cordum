import { useQuery } from "@tanstack/react-query";
import { get } from "@/api/client";
import type { AuthConfig } from "@/api/types";

const SAML_LOGIN_PATH = "/api/v1/auth/sso/saml/login";
const SAML_METADATA_PATH = "/api/v1/auth/sso/saml/metadata";
const SAML_ACS_PATH = "/api/v1/auth/sso/saml/acs";

export interface SAMLConfigView {
  enabled: boolean;
  configured: boolean;
  enterprise: boolean;
  loginUrl: string;
  metadataUrl: string;
  acsUrl: string;
  entityId: string;
  sessionTtl: string;
  oidc: {
    enabled: boolean;
    configured: boolean;
    issuer: string;
    loginUrl: string;
    clientId: string;
    redirectUri: string;
    clientSecretMasked: string;
    scopes: string[];
    groupsClaim: string;
    groupRoleMapping: Record<string, string>;
  };
  raw: AuthConfig;
}

function baseOrigin(raw?: AuthConfig): string {
  const fallback =
    typeof window !== "undefined" && window.location?.origin
      ? window.location.origin
      : "http://localhost";

  for (const candidate of [raw?.saml_login_url, raw?.saml_metadata_url]) {
    const trimmed = candidate?.trim();
    if (!trimmed) continue;
    try {
      return new URL(trimmed, fallback).origin;
    } catch {
      continue;
    }
  }

  return fallback;
}

function resolveUrl(candidate: string | undefined, path: string, origin: string): string {
  const trimmed = candidate?.trim();
  if (trimmed) {
    try {
      return new URL(trimmed, origin).toString();
    } catch {
      // Fall back to the derived service-provider path below.
    }
  }
  return new URL(path, origin).toString();
}

function normalizeGroupRoleMapping(raw: AuthConfig["oidc_group_role_mapping"]): Record<string, string> {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return {};

  return Object.entries(raw).reduce<Record<string, string>>((acc, [group, role]) => {
    const groupKey = group.trim().toLowerCase();
    const roleValue = typeof role === "string" ? role.trim().toLowerCase() : "";
    if (!groupKey || !["admin", "operator", "viewer"].includes(roleValue)) {
      return acc;
    }
    acc[groupKey] = roleValue;
    return acc;
  }, {});
}

function toSAMLConfigView(raw?: AuthConfig): SAMLConfigView | undefined {
  if (!raw) return undefined;

  const origin = baseOrigin(raw);
  const metadataUrl = resolveUrl(raw.saml_metadata_url, SAML_METADATA_PATH, origin);
  const loginUrl = resolveUrl(raw.saml_login_url, SAML_LOGIN_PATH, origin);
  const acsUrl = new URL(SAML_ACS_PATH, origin).toString();

  return {
    enabled: Boolean(raw.saml_enabled),
    configured: Boolean(raw.saml_enabled || raw.saml_login_url || raw.saml_metadata_url),
    enterprise: Boolean(raw.saml_enterprise),
    loginUrl,
    metadataUrl,
    acsUrl,
    entityId: metadataUrl,
    sessionTtl: raw.session_ttl?.trim() || "24h",
    oidc: {
      enabled: Boolean(raw.oidc_enabled),
      configured: Boolean(
        raw.oidc_enabled ||
          raw.oidc_login_url ||
          raw.oidc_client_id ||
          raw.oidc_redirect_uri ||
          raw.oidc_issuer,
      ),
      issuer: raw.oidc_issuer?.trim() || "",
      loginUrl: raw.oidc_login_url?.trim() || new URL("/api/v1/auth/sso/oidc/login", origin).toString(),
      clientId: raw.oidc_client_id?.trim() || "",
      redirectUri: raw.oidc_redirect_uri?.trim() || new URL("/api/v1/auth/sso/oidc/callback", origin).toString(),
      clientSecretMasked: raw.oidc_client_secret_masked?.trim() || "",
      scopes: Array.isArray(raw.oidc_scopes) ? raw.oidc_scopes.filter((scope) => typeof scope === "string").map((scope) => scope.trim()).filter(Boolean) : [],
      // Preserve an intentionally-empty backend claim instead of forcing
      // "groups". An empty claim signals that the legacy `cordum_role` fallback
      // is in effect; rewriting it here would silently switch users to
      // groups-claim precedence the next time the form is saved.
      groupsClaim: raw.oidc_groups_claim?.trim() ?? "",
      groupRoleMapping: normalizeGroupRoleMapping(raw.oidc_group_role_mapping),
    },
    raw,
  };
}

async function fetchSAMLConfig(): Promise<SAMLConfigView | undefined> {
  const raw = await get<AuthConfig>("/auth/config");
  return toSAMLConfigView(raw);
}

export function useSAMLConfig() {
  return useQuery<SAMLConfigView | undefined, Error>({
    queryKey: ["auth-config"],
    queryFn: fetchSAMLConfig,
    staleTime: 5 * 60_000,
  });
}
