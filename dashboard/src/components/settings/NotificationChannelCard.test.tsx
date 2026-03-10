import { describe, expect, it, vi } from "vitest";

// matchMedia must be defined before any component import (ui.ts uses it at module scope)
vi.hoisted(() => {
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
});

import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { NotificationChannelCard } from "./NotificationChannelCard";
import type { NotificationChannel } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeChannel(overrides: Partial<NotificationChannel> = {}): NotificationChannel {
  return {
    id: "ch-1",
    name: "Ops Alerts",
    type: "slack",
    config: {},
    enabled: true,
    ...overrides,
  };
}

function render(props: {
  channel?: NotificationChannel;
  onEdit?: (ch: NotificationChannel) => void;
  onTest?: (ch: NotificationChannel) => void;
  onDelete?: (id: string) => void;
  isDeleting?: boolean;
}) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(
      <NotificationChannelCard
        channel={props.channel ?? makeChannel()}
        onEdit={props.onEdit ?? (() => {})}
        onTest={props.onTest ?? (() => {})}
        onDelete={props.onDelete ?? (() => {})}
        isDeleting={props.isDeleting}
      />,
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

// ---------------------------------------------------------------------------
// Renders channel info
// ---------------------------------------------------------------------------

describe("NotificationChannelCard display", () => {
  it("renders channel name", () => {
    const { container, cleanup } = render({ channel: makeChannel({ name: "My Channel" }) });
    expect(container.textContent).toContain("My Channel");
    cleanup();
  });

  it("renders type label for slack", () => {
    const { container, cleanup } = render({ channel: makeChannel({ type: "slack" }) });
    expect(container.textContent).toContain("Slack");
    cleanup();
  });

  it("renders type label for email", () => {
    const { container, cleanup } = render({ channel: makeChannel({ type: "email" }) });
    expect(container.textContent).toContain("Email");
    cleanup();
  });

  it("renders type label for webhook", () => {
    const { container, cleanup } = render({ channel: makeChannel({ type: "webhook" }) });
    expect(container.textContent).toContain("Webhook");
    cleanup();
  });

  it("renders type label for pagerduty", () => {
    const { container, cleanup } = render({ channel: makeChannel({ type: "pagerduty" }) });
    expect(container.textContent).toContain("PagerDuty");
    cleanup();
  });

  it("shows Active badge when enabled", () => {
    const { container, cleanup } = render({ channel: makeChannel({ enabled: true }) });
    expect(container.textContent).toContain("Active");
    cleanup();
  });

  it("shows Disabled badge when not enabled", () => {
    const { container, cleanup } = render({ channel: makeChannel({ enabled: false }) });
    expect(container.textContent).toContain("Disabled");
    cleanup();
  });

  it("shows Error badge when error present", () => {
    const { container, cleanup } = render({
      channel: makeChannel({ enabled: true, error: "Connection timeout" }),
    });
    expect(container.textContent).toContain("Error");
    expect(container.textContent).toContain("Connection timeout");
    cleanup();
  });

  it("shows last sent time", () => {
    const { container, cleanup } = render({
      channel: makeChannel({ lastSentAt: undefined }),
    });
    expect(container.textContent).toContain("Never");
    cleanup();
  });
});

// ---------------------------------------------------------------------------
// Edit button
// ---------------------------------------------------------------------------

describe("NotificationChannelCard edit", () => {
  it("calls onEdit with channel when Edit is clicked", () => {
    const onEdit = vi.fn();
    const ch = makeChannel();
    const { container, cleanup } = render({ channel: ch, onEdit });
    const editBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.includes("Edit"),
    );
    act(() => editBtn?.click());
    expect(onEdit).toHaveBeenCalledOnce();
    expect(onEdit).toHaveBeenCalledWith(ch);
    cleanup();
  });
});

// ---------------------------------------------------------------------------
// Test button
// ---------------------------------------------------------------------------

describe("NotificationChannelCard test", () => {
  it("calls onTest with channel when Test is clicked", () => {
    const onTest = vi.fn();
    const ch = makeChannel();
    const { container, cleanup } = render({ channel: ch, onTest });
    const testBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.includes("Test"),
    );
    act(() => testBtn?.click());
    expect(onTest).toHaveBeenCalledOnce();
    expect(onTest).toHaveBeenCalledWith(ch);
    cleanup();
  });
});

// ---------------------------------------------------------------------------
// Delete confirmation flow
// ---------------------------------------------------------------------------

describe("NotificationChannelCard delete", () => {
  it("shows confirmation dialog when trash is clicked", () => {
    const { container, cleanup } = render({});
    // Initially no "Delete Channel" text (dialog closed)
    expect(container.textContent).not.toContain("Delete Channel");
    // Click trash button (last button, no text)
    const buttons = container.querySelectorAll("button");
    const trashBtn = Array.from(buttons).find(
      (b) => !b.textContent?.includes("Edit") && !b.textContent?.includes("Test") && b.className.includes("danger"),
    );
    act(() => trashBtn?.click());
    // Confirmation dialog should now be visible
    expect(container.textContent).toContain("Delete Channel");
    expect(container.textContent).toContain("Delete notification channel");
    cleanup();
  });

  it("calls onDelete when confirmed", () => {
    const onDelete = vi.fn();
    const ch = makeChannel({ id: "ch-42" });
    const { container, cleanup } = render({ channel: ch, onDelete });
    // Open confirm dialog
    const trashBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.className.includes("danger") && !b.textContent?.includes("Edit"),
    );
    act(() => trashBtn?.click());
    // Click "Delete" confirm button
    const deleteBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.trim() === "Delete",
    );
    act(() => deleteBtn?.click());
    expect(onDelete).toHaveBeenCalledOnce();
    expect(onDelete).toHaveBeenCalledWith("ch-42");
    cleanup();
  });

  it("closes dialog on cancel without deleting", () => {
    const onDelete = vi.fn();
    const { container, cleanup } = render({ onDelete });
    // Open confirm dialog
    const trashBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.className.includes("danger") && !b.textContent?.includes("Edit"),
    );
    act(() => trashBtn?.click());
    expect(container.textContent).toContain("Delete Channel");
    // Click Cancel
    const cancelBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.trim() === "Cancel",
    );
    act(() => cancelBtn?.click());
    expect(onDelete).not.toHaveBeenCalled();
    cleanup();
  });
});
