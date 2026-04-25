import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test-utils/render";
import { http, HttpResponse, server } from "@/test-utils/msw";

const { permissionState } = vi.hoisted(() => ({
  permissionState: {
    isAdmin: false,
  },
}));

vi.mock("../../hooks/usePermission", () => ({
  useIsAdmin: () => permissionState.isAdmin,
}));

import { LockInspector } from "./LockInspector";

describe("LockInspector", () => {
  beforeEach(() => {
    permissionState.isAdmin = false;
    server.resetHandlers();
  });

  afterEach(() => {
    permissionState.isAdmin = false;
  });

  it("non-admin mount does not request /admin/locks and renders the admin-required state", async () => {
    let requestCount = 0;
    server.use(
      http.get("*/api/v1/admin/locks", () => {
        requestCount += 1;
        return HttpResponse.json({ locks: [] });
      }),
    );

    const view = renderWithProviders(<LockInspector />);

    await waitFor(() => {
      expect(screen.queryByText(/admin role required/i)).not.toBeNull();
    });

    expect(
      screen.queryByText(/lock inspection is restricted to admin users\./i),
    ).not.toBeNull();
    expect(
      screen.queryByText(/no active locks — normal for idle clusters/i),
    ).toBeNull();
    expect(view.container.querySelector(".animate-pulse")).toBeNull();
    expect(requestCount).toBe(0);
  });

  it("admin mount requests /admin/locks and renders the returned lock rows", async () => {
    permissionState.isAdmin = true;
    let requestCount = 0;

    server.use(
      http.get("*/api/v1/admin/locks", () => {
        requestCount += 1;
        return HttpResponse.json({
          locks: [
            {
              key: "tenant:locks:reconciler:job-123",
              holder: "scheduler-1",
              ttl_remaining_ms: 12_000,
              type: "reconciler",
            },
          ],
        });
      }),
    );

    renderWithProviders(<LockInspector />);

    await waitFor(() => {
      expect(screen.queryByText("scheduler-1")).not.toBeNull();
    });

    expect(screen.queryByText("reconciler")).not.toBeNull();
    expect(screen.queryByText("...reconciler:job-123")).not.toBeNull();
    expect(screen.queryByText(/admin role required/i)).toBeNull();
    expect(requestCount).toBe(1);
  });

  it("when role check is false, it shows the admin-required state without flashing the skeleton", async () => {
    const view = renderWithProviders(<LockInspector />);

    await waitFor(() => {
      expect(screen.queryByText(/admin role required/i)).not.toBeNull();
    });

    expect(view.container.querySelector(".animate-pulse")).toBeNull();
  });
});
