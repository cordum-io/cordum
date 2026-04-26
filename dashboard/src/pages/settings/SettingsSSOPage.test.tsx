import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SettingsSSOPage from "./SettingsSSOPage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { apiState, hookState } = vi.hoisted(() => ({
  apiState: {
    put: vi.fn(),
  },
  hookState: {
    license: {} as any,
    saml: {} as any,
  },
}));

vi.mock("@/api/client", () => ({
  put: apiState.put,
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

async function waitFor(assertion: () => void, timeoutMs = 2000) {
  const start = Date.now();
  while (true) {
    try {
      assertion();
      return;
    } catch (error) {
      if (Date.now() - start >= timeoutMs) {
        throw error;
      }
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 10));
      });
    }
  }
}

function click(element: Element | null) {
  if (!(element instanceof HTMLElement)) {
    throw new Error("Expected clickable HTMLElement");
  }
  // Use the native HTMLElement.click() so disabled-button semantics are
  // preserved; dispatching a synthetic MouseEvent fires handlers even when
  // the real click would have been suppressed by the browser.
  act(() => {
    element.click();
  });
}

function changeInput(element: HTMLInputElement | HTMLTextAreaElement, value: string) {
  act(() => {
    const prototype = element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    const setter = Object.getOwnPropertyDescriptor(prototype, "value")?.set;
    setter?.call(element, value);
    element.dispatchEvent(new Event("input", { bubbles: true }));
    element.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

describe("SettingsSSOPage", () => {
  beforeEach(() => {
    apiState.put.mockReset();
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
          groupsClaim: "okta_groups",
          groupRoleMapping: {
            "cordum-admins": "admin",
            "cordum-viewers": "viewer",
          },
        },
        raw: {
          oidc_client_secret: "super-secret-value",
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
      expect(container.textContent).toContain("OIDC RBAC mapping");
      expect(container.textContent).toContain("Groups claim name");
      expect(container.textContent).toContain("Groups → roles mapping");
      expect(container.textContent).toContain("Mocked SAML config panel");
      const claimInput = container.querySelector('input[name="oidcGroupsClaim"]') as HTMLInputElement | null;
      expect(claimInput?.value).toBe("okta_groups");
      const mappingEditor = container.querySelector('textarea[name="oidcGroupRoleMapping"]') as HTMLTextAreaElement | null;
      expect(mappingEditor?.value).toContain('"cordum-admins": "admin"');
    } finally {
      cleanup();
    }
  });

  it("rejects invalid OIDC group-role mapping JSON before network save", async () => {
    const { container, cleanup } = renderPage();

    try {
      const mappingEditor = container.querySelector('textarea[name="oidcGroupRoleMapping"]') as HTMLTextAreaElement | null;
      expect(mappingEditor).not.toBeNull();

      changeInput(mappingEditor!, `{"cordum-admins":`);
      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Save OIDC RBAC"),
        ) ?? null,
      );

      await waitFor(() => {
        expect(container.textContent).toContain("Mapping must be valid JSON");
      });
      expect(apiState.put).not.toHaveBeenCalled();
    } finally {
      cleanup();
    }
  });

  it("saves OIDC group-role mapping and rerenders sanitized response", async () => {
    apiState.put.mockResolvedValue({
      oidc_enabled: true,
      oidc_groups_claim: "groups",
      oidc_group_role_mapping: {
        "cordum-admins": "admin",
        "cordum-operators": "operator",
      },
      oidc_client_secret_masked: "supe********alue",
    });

    const { container, cleanup } = renderPage();

    try {
      const claimInput = container.querySelector('input[name="oidcGroupsClaim"]') as HTMLInputElement | null;
      const mappingEditor = container.querySelector('textarea[name="oidcGroupRoleMapping"]') as HTMLTextAreaElement | null;
      expect(claimInput).not.toBeNull();
      expect(mappingEditor).not.toBeNull();

      changeInput(claimInput!, "groups");
      changeInput(mappingEditor!, `{"Cordum-Admins":"admin","cordum-operators":"operator"}`);
      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Save OIDC RBAC"),
        ) ?? null,
      );

      await waitFor(() => {
        expect(apiState.put).toHaveBeenCalledTimes(1);
      });
      expect(apiState.put).toHaveBeenCalledWith("/auth/oidc/group-role-mapping", {
        oidc_groups_claim: "groups",
        oidc_group_role_mapping: {
          "cordum-admins": "admin",
          "cordum-operators": "operator",
        },
      });
      await waitFor(() => {
        expect(container.textContent).toContain("OIDC RBAC mapping saved");
      });
      expect((container.querySelector('input[name="oidcGroupsClaim"]') as HTMLInputElement | null)?.value).toBe("groups");
      expect((container.querySelector('textarea[name="oidcGroupRoleMapping"]') as HTMLTextAreaElement | null)?.value).toContain('"cordum-operators": "operator"');
      expect(hookState.saml.refetch).toHaveBeenCalledTimes(1);
    } finally {
      cleanup();
    }
  });

  it("never renders a raw OIDC client secret in the RBAC editor", () => {
    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("OIDC RBAC mapping");
      expect(container.textContent).toContain("supe********alue");
      expect(container.textContent).not.toContain("super-secret-value");
      // textContent only inspects *visible* text. A leak through input
      // `value`, hidden form controls, `data-*`, or `aria-*` attributes
      // would slip past the visible-text check while still being
      // recoverable from the DOM. Assert against markup and live form
      // values so non-visible surfaces are also covered.
      expect(container.innerHTML).not.toContain("super-secret-value");
      const leakedControl = Array.from(
        container.querySelectorAll("input, textarea"),
      ).find(
        (el) =>
          (el as HTMLInputElement | HTMLTextAreaElement).value ===
          "super-secret-value",
      );
      expect(leakedControl).toBeUndefined();
    } finally {
      cleanup();
    }
  });
});
