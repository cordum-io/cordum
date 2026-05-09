import { describe, it, expect, vi, beforeEach } from "vitest";
import { act } from "react";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test-utils/render";
import { UserMenu } from "./UserMenu";

const { configState } = vi.hoisted(() => ({
  configState: {
    user: null as null | {
      id: string;
      username: string;
      email: string;
      display_name: string;
      roles: string[];
      tenant: string;
    },
    principalRole: "",
    logout: vi.fn(),
  },
}));

vi.mock("@/state/config", () => ({
  registerQueryClient: vi.fn(),
  useConfigStore: <T,>(selector: (state: typeof configState) => T): T =>
    selector(configState),
}));

const navigateMock = vi.fn();
vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>(
    "react-router-dom",
  );
  return { ...actual, useNavigate: () => navigateMock };
});

function renderMenu() {
  return renderWithProviders(<UserMenu />);
}

beforeEach(() => {
  configState.user = {
    id: "u1",
    username: "admin",
    email: "admin@cordum.local",
    display_name: "admin",
    roles: ["admin"],
    tenant: "default",
  };
  configState.principalRole = "admin";
  configState.logout = vi.fn();
  navigateMock.mockReset();
});

describe("UserMenu", () => {
  it("renders an avatar button when the user is logged in (axe-clean)", async () => {
    // Phase 5a: opt-in axe gate via renderWithProviders { runAxe: true }.
    // UserMenu's idle (closed-menu) state is the canonical render — no async
    // data, no waitFor preamble required.
    await renderWithProviders(<UserMenu />, { runAxe: true });
    const trigger = screen.getByRole("button", {
      name: /account menu for admin/i,
    });
    expect(trigger).toBeTruthy();
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    expect(screen.getByText("A")).toBeTruthy();
  });

  it("falls back to a static brand avatar when no user is signed in", () => {
    configState.user = null;
    renderMenu();
    expect(screen.queryByRole("button", { name: /account menu/i })).toBeNull();
    expect(screen.getByText("C")).toBeTruthy();
  });

  it("opens a menu with identity, role, and tenant on click", () => {
    renderMenu();
    fireEvent.click(
      screen.getByRole("button", { name: /account menu for admin/i }),
    );
    expect(screen.getByRole("menu")).toBeTruthy();
    expect(screen.getByText("admin@cordum.local")).toBeTruthy();
    const adminBadges = screen.getAllByText("admin");
    expect(adminBadges.length).toBeGreaterThan(0);
    expect(screen.getByText("default")).toBeTruthy();
  });

  it("shows the admin-only Chain verification entry for admin users", () => {
    renderMenu();
    fireEvent.click(
      screen.getByRole("button", { name: /account menu for admin/i }),
    );
    expect(screen.getByRole("menuitem", { name: /chain verification/i })).toBeTruthy();
  });

  it("hides the admin-only entries for non-admin users", () => {
    configState.user = {
      id: "u2",
      username: "viewer",
      email: "v@cordum.local",
      display_name: "viewer",
      roles: ["viewer"],
      tenant: "default",
    };
    configState.principalRole = "viewer";
    renderMenu();
    fireEvent.click(
      screen.getByRole("button", { name: /account menu for viewer/i }),
    );
    expect(screen.queryByRole("menuitem", { name: /chain verification/i })).toBeNull();
    expect(screen.getByRole("menuitem", { name: /^settings$/i })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: /sign out/i })).toBeTruthy();
  });

  it("navigates to /settings when Settings is clicked", () => {
    renderMenu();
    fireEvent.click(
      screen.getByRole("button", { name: /account menu for admin/i }),
    );
    fireEvent.click(screen.getByRole("menuitem", { name: /^settings$/i }));
    expect(navigateMock).toHaveBeenCalledWith("/settings");
  });

  it("invokes logout when Sign out is clicked", () => {
    renderMenu();
    fireEvent.click(
      screen.getByRole("button", { name: /account menu for admin/i }),
    );
    fireEvent.click(screen.getByRole("menuitem", { name: /sign out/i }));
    expect(configState.logout).toHaveBeenCalledTimes(1);
  });

  it("closes on Escape", async () => {
    renderMenu();
    fireEvent.click(
      screen.getByRole("button", { name: /account menu for admin/i }),
    );
    expect(screen.queryByRole("menu")).toBeTruthy();
    act(() => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape" }),
      );
    });
    // AnimatePresence runs an exit animation, so the menu is removed async.
    await waitFor(() => {
      expect(screen.queryByRole("menu")).toBeNull();
    });
  });
});
