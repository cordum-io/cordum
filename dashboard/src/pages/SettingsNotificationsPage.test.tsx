import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SettingsNotificationsPage from "./SettingsNotificationsPage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const hookState = vi.hoisted(() => ({
  channels: {
    data: [],
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  } as any,
  rules: {
    data: [],
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  } as any,
  deleteChannel: {
    mutate: vi.fn(),
    isPending: false,
  } as any,
  saveRules: {
    mutate: vi.fn(),
    isPending: false,
  } as any,
}));

vi.mock("@/hooks/useSettings", () => ({
  useNotificationChannels: () => hookState.channels,
  useNotificationRules: () => hookState.rules,
  useDeleteNotificationChannel: () => hookState.deleteChannel,
  useSaveNotificationRules: () => hookState.saveRules,
}));

function renderPage() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={["/settings/notifications"]}>
        <SettingsNotificationsPage />
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

describe("SettingsNotificationsPage", () => {
  beforeEach(() => {
    hookState.channels = {
      data: [
        {
          id: "ch-1",
          name: "Ops Slack",
          type: "slack",
          config: { channel: "#ops-alerts" },
          enabled: true,
          lastSentAt: "2026-04-19T12:00:00Z",
        },
      ],
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    };
    hookState.rules = {
      data: [
        {
          id: "rule-1",
          eventPattern: "approval.*",
          channelIds: ["ch-1"],
          throttleMs: 300000,
          enabled: true,
        },
      ],
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    };
    hookState.deleteChannel = { mutate: vi.fn(), isPending: false };
    hookState.saveRules = { mutate: vi.fn(), isPending: false };
  });

  it("renders config-backed rules and channels instead of the old dead preferences flow", () => {
    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("Saved in system config");
      expect(container.textContent).toContain("Routing rules");
      expect(container.textContent).toContain("approval.*");
      expect(container.textContent).toContain("Ops Slack");
      expect(container.textContent).toContain("5 min");
      expect(container.textContent).toContain("Add rule");
      expect(container.textContent).not.toContain("Save Preferences");
      expect(container.textContent).not.toContain("inventory only");
    } finally {
      cleanup();
    }
  });
});
