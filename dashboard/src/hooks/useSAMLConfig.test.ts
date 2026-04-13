import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import { useSAMLConfig } from "./useSAMLConfig";

const { loggerMock } = vi.hoisted(() => ({
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

const { mockConfigState } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "",
    tenantId: "",
    principalId: "",
    principalRole: "",
    user: null,
    logout: vi.fn(),
  },
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

describe("useSAMLConfig", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000456");
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("derives SAML URLs from auth/config defaults", async () => {
    mockFetch([
      {
        match: "/auth/config",
        method: "GET",
        body: {
          password_enabled: true,
          saml_enabled: true,
          saml_enterprise: true,
          default_tenant: "default",
          session_ttl: "12h",
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useSAMLConfig(), createTestQueryClient());

    try {
      await hook.waitFor(() => {
        expect(hook.result.current?.isSuccess).toBe(true);
      });

      expect(hook.result.current?.data).toMatchObject({
        enabled: true,
        configured: true,
        enterprise: true,
        loginUrl: `${window.location.origin}/api/v1/auth/sso/saml/login`,
        metadataUrl: `${window.location.origin}/api/v1/auth/sso/saml/metadata`,
        acsUrl: `${window.location.origin}/api/v1/auth/sso/saml/acs`,
        entityId: `${window.location.origin}/api/v1/auth/sso/saml/metadata`,
        sessionTtl: "12h",
        oidc: {
          enabled: false,
          configured: false,
          loginUrl: `${window.location.origin}/api/v1/auth/sso/oidc/login`,
          redirectUri: `${window.location.origin}/api/v1/auth/sso/oidc/callback`,
          scopes: [],
        },
      });
      expect(hook.result.current?.data?.raw).toMatchObject({
        saml_enabled: true,
        saml_enterprise: true,
        session_ttl: "12h",
      });
    } finally {
      hook.unmount();
    }
  });

  it("preserves explicit SAML URLs from the backend", async () => {
    mockFetch([
      {
        match: "/auth/config",
        method: "GET",
        body: {
          password_enabled: false,
          saml_enabled: false,
          saml_enterprise: true,
          saml_login_url: "https://gateway.cordum.test/api/v1/auth/sso/saml/login",
          saml_metadata_url: "https://gateway.cordum.test/api/v1/auth/sso/saml/metadata",
          oidc_enabled: true,
          oidc_issuer: "https://login.cordum.test/realms/main",
          oidc_login_url: "https://gateway.cordum.test/api/v1/auth/sso/oidc/login",
          oidc_client_id: "cordum-dashboard",
          oidc_redirect_uri: "https://gateway.cordum.test/api/v1/auth/sso/oidc/callback",
          oidc_scopes: ["openid", "profile", "email"],
          oidc_client_secret_masked: "supe********alue",
          default_tenant: "default",
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useSAMLConfig(), createTestQueryClient());

    try {
      await hook.waitFor(() => {
        expect(hook.result.current?.isSuccess).toBe(true);
      });

      expect(hook.result.current?.data).toMatchObject({
        enabled: false,
        configured: true,
        enterprise: true,
        loginUrl: "https://gateway.cordum.test/api/v1/auth/sso/saml/login",
        metadataUrl: "https://gateway.cordum.test/api/v1/auth/sso/saml/metadata",
        acsUrl: "https://gateway.cordum.test/api/v1/auth/sso/saml/acs",
        oidc: {
          enabled: true,
          configured: true,
          issuer: "https://login.cordum.test/realms/main",
          loginUrl: "https://gateway.cordum.test/api/v1/auth/sso/oidc/login",
          clientId: "cordum-dashboard",
          redirectUri: "https://gateway.cordum.test/api/v1/auth/sso/oidc/callback",
          clientSecretMasked: "supe********alue",
          scopes: ["openid", "profile", "email"],
        },
      });
    } finally {
      hook.unmount();
    }
  });
});
