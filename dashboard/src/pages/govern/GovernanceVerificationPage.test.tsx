import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { Routes, Route } from "react-router-dom";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test-utils/render";
import { http, HttpResponse, server } from "@/test-utils/msw";

const { permissionMock } = vi.hoisted(() => ({
  permissionMock: { allowed: true, userRoles: ["admin"] as string[] },
}));

vi.mock("@/hooks/usePermission", () => ({
  usePermission: () => permissionMock,
  useIsAdmin: () => permissionMock.allowed,
}));

import GovernanceVerificationPage from "./GovernanceVerificationPage";

function renderRoute() {
  return renderWithProviders(
    <Routes>
      <Route path="/govern/verification" element={<GovernanceVerificationPage />} />
    </Routes>,
    { initialEntries: ["/govern/verification"] },
  );
}

describe("GovernanceVerificationPage (task-14d012e6)", () => {
  beforeEach(() => {
    permissionMock.allowed = true;
    permissionMock.userRoles = ["admin"];
    server.resetHandlers();
    server.use(
      http.get("*/api/v1/audit/verify", () =>
        HttpResponse.json({
          status: "ok",
          total_events: 100,
          verified_events: 100,
          gaps: [],
          retention_boundary_seq: 0,
        }),
      ),
    );
  });

  afterEach(() => {
    permissionMock.allowed = true;
    permissionMock.userRoles = ["admin"];
  });

  it("admin user: ChainIntegrityWidget mounts on the page", async () => {
    renderRoute();
    await waitFor(() => {
      expect(
        screen.queryByTestId("chain-integrity-widget"),
      ).not.toBeNull();
    });
  });

  it("non-admin user: page renders friendly EmptyState fallback (NOT a blank card)", async () => {
    permissionMock.allowed = false;
    permissionMock.userRoles = [];
    renderRoute();
    await waitFor(() => {
      expect(screen.queryByText(/admin role required/i)).not.toBeNull();
    });
    // Widget is NOT mounted for non-admin
    expect(screen.queryByTestId("chain-integrity-widget")).toBeNull();
  });

  it("admin user: page header is 'Chain Verification'", async () => {
    renderRoute();
    await waitFor(() => {
      expect(screen.queryByText(/chain verification/i)).not.toBeNull();
    });
  });
});
