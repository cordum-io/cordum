import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SettingsSCIMPage from "./SettingsSCIMPage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    license: {} as any,
    scim: {} as any,
    rotate: {} as any,
  },
}));

vi.mock("@/hooks/useLicense", () => ({
  useLicense: () => hookState.license,
}));

vi.mock("@/hooks/useSCIMConfig", () => ({
  useSCIMConfig: () => hookState.scim,
  useRotateSCIMToken: () => hookState.rotate,
}));

function renderPage() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={["/settings/scim"]}>
        <SettingsSCIMPage />
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

describe("SettingsSCIMPage", () => {
  beforeEach(() => {
    hookState.license = {
      data: {
        plan: "enterprise",
        entitlements: {
          scim: true,
        },
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    };
    hookState.scim = {
      data: {
        entitled: true,
        configured: true,
        endpointUrl: "https://gateway.cordum.test/api/v1/scim/v2/Users",
        bearerToken: "scim-secret-token",
        bearerTokenMasked: "scim-sec********oken",
        tokenManagedBy: "redis",
        users: [
          {
            id: "user-1",
            userName: "alice@example.com",
            displayName: "Alice Example",
            email: "alice@example.com",
            source: "scim",
            active: true,
            syncedAt: "2026-04-11T12:00:00Z",
          },
        ],
      },
      isLoading: false,
      isError: false,
      isFetching: false,
      refetch: vi.fn(),
    };
    hookState.rotate = {
      mutate: vi.fn(),
      isPending: false,
    };
  });

  it("shows the upgrade prompt when SCIM is not licensed", () => {
    hookState.license = {
      data: {
        plan: "community",
        entitlements: {
          scim: false,
        },
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    };

    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("SCIM provisioning is locked on Community");
      expect(container.textContent).toContain("Provisioning remains disabled on the active tier");
      expect(container.textContent).not.toContain("Provisioning endpoint and token");
    } finally {
      cleanup();
    }
  });

  it("renders the endpoint, token controls, and synced users when licensed", () => {
    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("SCIM provisioning");
      expect(container.textContent).toContain("Provisioning endpoint and token");
      expect(container.textContent).toContain("https://gateway.cordum.test/api/v1/scim/v2/Users");
      expect(container.textContent).toContain("scim-sec********oken");
      expect(container.textContent).toContain("Rotate token");
      expect(container.textContent).toContain("SCIM-managed users");
      expect(container.textContent).toContain("Alice Example");
      expect(container.textContent).toContain("alice@example.com");
    } finally {
      cleanup();
    }
  });
});
