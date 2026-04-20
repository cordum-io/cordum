import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SettingsHubPage from "../SettingsHubPage";
import VelocityRulesPage from "../govern/VelocityRulesPage";
import SettingsSCIMPage from "./SettingsSCIMPage";
import SettingsSSOPage from "./SettingsSSOPage";

const { hookState, navigateMock } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: () => ({
      matches: false,
      media: "",
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });

  return {
    hookState: {
      license: {} as any,
      saml: {} as any,
      scim: {} as any,
      policyAccess: {} as any,
      velocityRules: {} as any,
      velocityStats: {} as any,
    },
    navigateMock: vi.fn(),
  };
});

vi.mock("@/hooks/useLicense", () => ({
  useLicense: () => hookState.license,
}));

vi.mock("@/hooks/useSAMLConfig", () => ({
  useSAMLConfig: () => hookState.saml,
}));

vi.mock("@/hooks/useSCIMConfig", () => ({
  useSCIMConfig: () => hookState.scim,
  useRotateSCIMToken: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
}));

vi.mock("@/hooks/usePolicyAccess", () => ({
  usePolicyAccess: () => hookState.policyAccess,
}));

vi.mock("@/hooks/usePageTitle", () => ({
  usePageTitle: () => {},
}));

vi.mock("@/hooks/usePolicies", () => ({
  useVelocityRules: () => hookState.velocityRules,
  useVelocityRuleStats: () => hookState.velocityStats,
  useCreateVelocityRule: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateVelocityRule: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteVelocityRule: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("@/components/settings/SamlConfigPanel", () => ({
  SamlConfigPanel: ({ entitled }: { entitled: boolean }) => (
    <div>{`SAML config panel entitled=${String(entitled)}`}</div>
  ),
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

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => navigateMock,
  };
});

function renderPage(node: React.ReactNode, route = "/") {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={[route]}>
        {node}
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

function findButtonByText(container: HTMLElement, text: string): HTMLButtonElement | null {
  return (
    Array.from(container.querySelectorAll("button")).find((button) =>
      button.textContent?.includes(text),
    ) ?? null
  );
}

function findSettingsCard(container: HTMLElement, title: string): HTMLButtonElement | null {
  const heading = Array.from(container.querySelectorAll("h2")).find(
    (element) => element.textContent?.trim() === title,
  );
  return (heading?.closest("button") as HTMLButtonElement | null) ?? null;
}

describe("enterprise entitlement matrix dashboard truthfulness", () => {
  beforeEach(() => {
    navigateMock.mockReset();

    hookState.license = {
      data: {
        plan: "enterprise",
        entitlements: {
          sso: true,
          saml: true,
          scim: true,
          rbac: true,
          auditExport: true,
          siemExport: true,
          legalHold: true,
          velocityRules: true,
        },
      },
      isLoading: false,
      isError: false,
      error: null,
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
      error: null,
      refetch: vi.fn(),
    };

    hookState.scim = {
      data: {
        entitled: true,
        configured: true,
        endpointUrl: "https://gateway.cordum.test/scim/v2",
        bearerTokenMasked: "scim******oken",
        tokenManagedBy: "dashboard",
        users: [],
      },
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    };

    hookState.policyAccess = {
      canEdit: true,
      canPublish: true,
      canRelease: true,
      canManageOutputRules: true,
      canManageTenants: true,
      isReadOnly: false,
      requiresAuth: true,
      userRoles: ["admin"],
      principalRole: "admin",
    };

    hookState.velocityRules = {
      data: { items: [], limit: 20 },
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    };

    hookState.velocityStats = {
      data: { items: [] },
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    };
  });

  it("keeps users and legal-hold audit settings reachable from the settings hub without RBAC or export entitlements", () => {
    hookState.license.data = {
      plan: "team",
      entitlements: {
        rbac: false,
        auditExport: false,
        siemExport: false,
        legalHold: true,
        sso: true,
      },
    };

    const { container, cleanup } = renderPage(<SettingsHubPage />, "/settings");

    try {
      const usersCard = findSettingsCard(container, "Users & RBAC");
      const auditCard = findSettingsCard(container, "Audit Export");

      expect(usersCard).not.toBeNull();
      expect(usersCard?.getAttribute("data-locked")).toBe("false");

      expect(auditCard).not.toBeNull();
      expect(auditCard?.getAttribute("data-locked")).toBe("false");
    } finally {
      cleanup();
    }
  });

  it("locks the settings hub SSO card unless the base SSO entitlement is present", () => {
    hookState.license.data = {
      plan: "team",
      entitlements: {
        sso: false,
        saml: true,
      },
    };

    const { container, cleanup } = renderPage(<SettingsHubPage />, "/settings");

    try {
      const ssoCard = findSettingsCard(container, "SSO & SAML");
      expect(ssoCard).not.toBeNull();
      expect(ssoCard?.getAttribute("data-locked")).toBe("true");
    } finally {
      cleanup();
    }
  });

  it("keeps OIDC visible while locking SAML-specific controls when the SAML add-on is missing", () => {
    hookState.license.data = {
      plan: "team",
      entitlements: {
        sso: true,
        saml: false,
      },
    };

    const { container, cleanup } = renderPage(<SettingsSSOPage />, "/settings/sso");

    try {
      expect(container.textContent).toContain("OIDC operator handoff");
      expect(container.textContent).toContain("SAML add-on required");
      expect(container.textContent).toContain("SAML config panel entitled=false");
      expect(container.textContent).not.toContain("Open metadata");

      const samlTestButton = findButtonByText(container, "Test SAML login");
      expect(samlTestButton).not.toBeNull();
      expect(samlTestButton?.disabled).toBe(true);
    } finally {
      cleanup();
    }
  });

  it("shows the SCIM upgrade prompt when the entitlement is missing", () => {
    hookState.license.data = {
      plan: "community",
      entitlements: {
        scim: false,
      },
    };

    const { container, cleanup } = renderPage(<SettingsSCIMPage />, "/settings/scim");

    try {
      expect(container.textContent).toContain("SCIM provisioning is locked on Community");
      expect(container.textContent).toContain("Provisioning remains disabled on the active tier");
    } finally {
      cleanup();
    }
  });

  it("disables the primary velocity-rules CTA when the entitlement is missing", () => {
    hookState.license.data = {
      plan: "team",
      entitlements: {
        velocityRules: false,
      },
    };

    const { container, cleanup } = renderPage(<VelocityRulesPage />, "/govern/velocity-rules");

    try {
      expect(container.textContent).toContain("Velocity-rule fragments and live counters require a licensed deployment.");
      const newRuleButton = findButtonByText(container, "New rule");
      expect(newRuleButton).not.toBeNull();
      expect(newRuleButton?.disabled).toBe(true);
    } finally {
      cleanup();
    }
  });
});
