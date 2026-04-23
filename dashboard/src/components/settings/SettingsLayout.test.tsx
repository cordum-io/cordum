import React, { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import SettingsLayout from "./SettingsLayout";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock("../../hooks/useSetupStatus", () => ({
  useSetupStatus: () => ({
    isLoading: false,
    dismissed: true,
    isNewInstall: false,
    completedCount: 0,
    totalRequired: 0,
    items: [],
    dismiss: vi.fn(),
  }),
}));

interface RenderResult {
  container: HTMLDivElement;
  unmount: () => void;
  waitFor: (assertion: () => void, timeoutMs?: number) => Promise<void>;
}

function renderSettingsAt(path: string): RenderResult {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root: Root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/settings" element={<SettingsLayout />}>
            <Route path="health" element={<div>Health tab</div>} />
            <Route path="output-safety" element={<div>Output Safety tab</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
  });

  async function waitFor(assertion: () => void, timeoutMs = 2000): Promise<void> {
    const start = Date.now();
    while (true) {
      try {
        assertion();
        return;
      } catch (error) {
        if (Date.now() - start >= timeoutMs) throw error;
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 10));
        });
      }
    }
  }

  return {
    container,
    unmount: () => {
      act(() => {
        root.unmount();
      });
      container.remove();
    },
    waitFor,
  };
}

const EXPECTED_LABELS = [
  "System Health",
  "API Keys",
  "Users & Access",
  "Notifications",
  "Environments",
  "MCP Server",
  "Configuration",
];

describe("SettingsLayout navigation", () => {
  it("does not include Output Safety link and shows migration note", async () => {
    const view = renderSettingsAt("/settings/health");
    await view.waitFor(() => {
      expect(view.container.textContent).not.toContain("Output Safety");
      expect(view.container.textContent).toContain("Safety controls have moved");
      expect(view.container.textContent).toContain("Health tab");
    });

    const migrationLink = Array.from(view.container.querySelectorAll("a")).find((a) =>
      a.textContent?.includes("Go to Safety"),
    ) as HTMLAnchorElement | undefined;
    expect(migrationLink).toBeTruthy();
    expect(migrationLink?.getAttribute("href")).toBe("/security/safety");

    view.unmount();
  });

  it("renders nav links for all 7 settings sections", async () => {
    const view = renderSettingsAt("/settings/health");
    await view.waitFor(() => {
      for (const label of EXPECTED_LABELS) {
        expect(view.container.textContent).toContain(label);
      }
    });
    view.unmount();
  });

  it("active link gets highlighted class based on current route", async () => {
    const view = renderSettingsAt("/settings/health");
    await view.waitFor(() => {
      const links = Array.from(view.container.querySelectorAll("a"));
      const healthLink = links.find((a) => a.textContent?.includes("System Health"));
      expect(healthLink).toBeTruthy();
      // Active link should have accent class
      expect(healthLink?.className).toContain("text-accent");
      // Non-active links should have muted class
      const keysLink = links.find((a) => a.textContent?.includes("API Keys"));
      expect(keysLink?.className).toContain("text-muted");
    });
    view.unmount();
  });

  it("children rendered in content area via Outlet", async () => {
    const view = renderSettingsAt("/settings/health");
    await view.waitFor(() => {
      expect(view.container.textContent).toContain("Health tab");
    });
    view.unmount();
  });

  it("renders Settings heading", async () => {
    const view = renderSettingsAt("/settings/health");
    await view.waitFor(() => {
      expect(view.container.textContent).toContain("Settings");
    });
    view.unmount();
  });
});
