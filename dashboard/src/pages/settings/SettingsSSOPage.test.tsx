import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SettingsSSOPage from "./SettingsSSOPage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    license: {} as any,
    saml: {} as any,
  },
}));

vi.mock("@/hooks/useLicense", () => ({
  useLicense: () => hookState.license,
}));

vi.mock("@/hooks/useSAMLConfig", () => ({
  useSAMLConfig: () => hookState.saml,
}));

vi.mock("@/components/settings/SamlConfigPanel", () => ({
  SamlConfigPanel: () => <div>Mocked SAML config panel</div>,
}));

vi.mock("@/components/ui/Button", () => ({
  Button: ({
    children,
    disabled,
    onClick,
  }: {
    children: React.ReactNode;
    disabled?: boolean;
    onClick?: () => void;
  }) => (
    <button type="button" disabled={disabled} onClick={onClick}>
      {children}
    </button>
  ),
}));

function renderPage() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={["/settings/sso"]}>
        <SettingsSSOPage />
      </MemoryRouter>,
    );
  });

  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

describe("SettingsSSOPage", () => {
  beforeEach(() => {
    hookState.license = {
      data: {
        plan: "enterprise",
        entitlements: {
          sso: true,
          saml: true,
        },
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    };
    hookState.saml = {
      data: {
        enabled: true,
        enterprise: true,
        loginUrl: "https://gateway.cordum.test/api/v1/auth/sso/saml/login",
        metadataUrl: "https://gateway.cordum.test/api/v1/auth/sso/saml/metadata",
        acsUrl: "https://gateway.cordum.test/api/v1/auth/sso/saml/acs",
        entityId: "https://gateway.cordum.test/api/v1/auth/sso/saml/metadata",
        sessionTtl: "24h",
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
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    };
  });

  it("shows the upgrade prompt when SAML is not licensed", () => {
    hookState.license = {
      data: {
        plan: "community",
        entitlements: {
          sso: false,
          saml: false,
        },
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    };

    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("SSO providers are locked on Community");
      expect(container.textContent).toContain("Enterprise identity setup is unavailable");
      expect(container.textContent).not.toContain("Mocked SAML config panel");
    } finally {
      cleanup();
    }
  });

  it("renders operator handoff details and the config panel when licensed", () => {
    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("SSO providers");
      expect(container.textContent).toContain("SAML operator handoff");
      expect(container.textContent).toContain("OIDC operator handoff");
      expect(container.textContent).toContain("https://gateway.cordum.test/api/v1/auth/sso/saml/metadata");
      expect(container.textContent).toContain("https://gateway.cordum.test/api/v1/auth/sso/saml/acs");
      expect(container.textContent).toContain("https://login.cordum.test/realms/main");
      expect(container.textContent).toContain("Mocked SAML config panel");
    } finally {
      cleanup();
    }
  });
});
