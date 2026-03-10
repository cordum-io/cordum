import { describe, expect, it } from "vitest";
import { LEGACY_POLICY_ROUTE_REDIRECTS } from "./App";
import { COMMAND_PALETTE_COMMANDS } from "./components/CommandPalette";
import { useConfigStore, registerQueryClient } from "./state/config";
import { useEventStore } from "./state/events";

describe("App policy route redirects", () => {
  it("redirects legacy /policies/builder route to govern input rules", () => {
    expect(LEGACY_POLICY_ROUTE_REDIRECTS.builder).toBe("/govern/input-rules");
  });

  it("uses /govern/input-rules as the default legacy /policies target", () => {
    expect(LEGACY_POLICY_ROUTE_REDIRECTS.root).toBe("/govern/input-rules");
  });

  it("keeps all legacy policy redirects on /govern routes to prevent redirect loops", () => {
    const targets = Object.values(LEGACY_POLICY_ROUTE_REDIRECTS);
    expect(targets.length).toBeGreaterThan(0);
    expect(targets.every((target) => target.startsWith("/govern/"))).toBe(true);
    expect(targets.some((target) => target.startsWith("/policies"))).toBe(false);
  });
});

describe("Command palette navigation integrity", () => {
  it("has no commands pointing to legacy /policies routes", () => {
    const legacyPaths = COMMAND_PALETTE_COMMANDS.filter((c) =>
      c.path.startsWith("/policies"),
    );
    expect(legacyPaths).toEqual([]);
  });

  it("has no commands pointing to legacy /quarantine route", () => {
    const legacyQuarantine = COMMAND_PALETTE_COMMANDS.filter(
      (c) => c.path === "/quarantine",
    );
    expect(legacyQuarantine).toEqual([]);
  });

  it("includes Govern section commands for all six govern pages", () => {
    const governCommands = COMMAND_PALETTE_COMMANDS.filter(
      (c) => c.section === "Govern",
    );
    const paths = governCommands.map((c) => c.path).sort();
    expect(paths).toEqual([
      "/govern/bundles",
      "/govern/input-rules",
      "/govern/output-rules",
      "/govern/quarantine",
      "/govern/simulator",
      "/govern/tenants",
    ]);
  });
});

describe("Auth/tenant cache isolation", () => {
  it("logout clears React Query cache via registered queryClient", () => {
    let cleared = false;
    registerQueryClient({ clear: () => { cleared = true; } });

    // Set up a logged-in state
    useConfigStore.setState({
      apiKey: "test-key",
      isAuthenticated: true,
      tenantId: "tenant-1",
      user: { id: "u1", username: "test", email: "", display_name: "", roles: ["admin"], tenant: "tenant-1" },
    });

    useConfigStore.getState().logout();

    expect(cleared).toBe(true);
    expect(useConfigStore.getState().isAuthenticated).toBe(false);
    expect(useConfigStore.getState().apiKey).toBe("");
    expect(useConfigStore.getState().tenantId).toBe("");
  });

  it("logout resets event store buffers", () => {
    useEventStore.getState().addEvent({ id: "e1", type: "test", timestamp: "", payload: {} });
    expect(useEventStore.getState().events.length).toBeGreaterThan(0);

    useConfigStore.getState().logout();

    expect(useEventStore.getState().events).toEqual([]);
    expect(useEventStore.getState().safetyDecisions).toEqual([]);
    expect(useEventStore.getState().status).toBe("disconnected");
  });

  it("tenant switch via update() clears query cache", () => {
    let cleared = false;
    registerQueryClient({ clear: () => { cleared = true; } });

    useConfigStore.setState({
      apiKey: "key",
      isAuthenticated: true,
      tenantId: "tenant-a",
      tenantLocked: false,
    });

    useConfigStore.getState().update({ tenantId: "tenant-b" });

    expect(cleared).toBe(true);
  });

  it("tenantLocked prevents tenant change via update()", () => {
    useConfigStore.setState({
      apiKey: "key",
      isAuthenticated: true,
      tenantId: "tenant-locked",
      tenantLocked: true,
    });

    useConfigStore.getState().update({ tenantId: "tenant-evil" });

    expect(useConfigStore.getState().tenantId).toBe("tenant-locked");
  });
});
