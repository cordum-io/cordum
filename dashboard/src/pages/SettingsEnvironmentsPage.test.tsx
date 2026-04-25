import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SettingsEnvironmentsPage from "./SettingsEnvironmentsPage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const hookState = vi.hoisted(() => ({
  environments: {
    data: [],
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  } as any,
}));

vi.mock("@/hooks/useSettings", () => ({
  useEnvironments: () => hookState.environments,
}));

function renderPage() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={["/settings/environments"]}>
        <SettingsEnvironmentsPage />
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

describe("SettingsEnvironmentsPage", () => {
  beforeEach(() => {
    hookState.environments = {
      data: [
        {
          id: "staging",
          name: "Staging",
          status: "maintenance",
          endpoint: "https://staging.cordum.test",
          config: { region: "us-east-1", deploy_target: "blue" },
          lastDeployedAt: "2026-04-18T08:00:00Z",
          lastPromotedAt: "2026-04-18T09:00:00Z",
        },
      ],
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    };
  });

  it("renders the config-backed environment inventory instead of the removed dead endpoint view", () => {
    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("Config-backed inventory");
      expect(container.textContent).toContain("Staging");
      expect(container.textContent).toContain("staging");
      expect(container.textContent).toContain("https://staging.cordum.test");
      expect(container.textContent).toContain("Config entries");
      expect(container.textContent).not.toContain("Workers");
      expect(container.textContent).not.toContain("Version");
      expect(container.textContent).not.toContain("Region");
    } finally {
      cleanup();
    }
  });

  it("renders an empty state instead of inventing a default production environment", () => {
    hookState.environments = {
      data: [],
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    };

    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("No configured environments");
      expect(container.textContent).toContain("does not invent a default production environment");
      expect(container.textContent).not.toContain("Environment ID");
    } finally {
      cleanup();
    }
  });
});
