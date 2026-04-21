import { describe, expect, it } from "vitest";
import { APP_SHELL_G_KEY_MAP, APP_SHELL_NAV_SECTIONS, deriveSystemStatus, statusColorMap } from "./AppShell";

describe("AppShell systemStatus derivation", () => {
  it("returns 'loading' with grey indicator when status data is undefined and still loading", () => {
    const status = deriveSystemStatus(undefined, false, true);
    expect(status).toBe("loading");
    expect(statusColorMap[status]).toBe("bg-muted-foreground/40");
  });

  it("returns 'down' with red indicator when status query errors", () => {
    const status = deriveSystemStatus(undefined, true, false);
    expect(status).toBe("down");
    expect(statusColorMap[status]).toBe("bg-status-error");
  });

  it("returns 'degraded' with amber indicator when NATS is disconnected", () => {
    const status = deriveSystemStatus({ nats: { connected: false }, redis: { ok: true } }, false, false);
    expect(status).toBe("degraded");
    expect(statusColorMap[status]).toBe("bg-status-warning");
  });

  it("returns 'degraded' with amber indicator when Redis is down", () => {
    const status = deriveSystemStatus({ nats: { connected: true }, redis: { ok: false } }, false, false);
    expect(status).toBe("degraded");
    expect(statusColorMap[status]).toBe("bg-status-warning");
  });

  it("returns 'healthy' with green indicator when all services are up", () => {
    const status = deriveSystemStatus({ nats: { connected: true }, redis: { ok: true } }, false, false);
    expect(status).toBe("healthy");
    expect(statusColorMap[status]).toBe("bg-status-healthy");
  });

  it("returns 'degraded' when no data, not loading, and no error (stale/unreachable)", () => {
    const status = deriveSystemStatus(undefined, false, false);
    expect(status).toBe("degraded");
    expect(statusColorMap[status]).toBe("bg-status-warning");
  });

  it("NEVER returns 'healthy' when data is undefined (the original fail-open bug)", () => {
    expect(deriveSystemStatus(undefined, false, true)).not.toBe("healthy");
    expect(deriveSystemStatus(undefined, false, false)).not.toBe("healthy");
    expect(deriveSystemStatus(undefined, true, false)).not.toBe("healthy");
  });
});

describe("AppShell GOVERN navigation", () => {
  it("keeps Delegations hidden until the delegation dashboard feature flag is enabled", () => {
    const govern = APP_SHELL_NAV_SECTIONS.find((section) => section.label === "Govern");
    expect(govern).toBeDefined();

    const labels = govern?.items.map((item) => item.label);
    expect(labels).toEqual([
      "Policy Studio",
      "Quarantine",
    ]);
  });

  it("keeps the default governance nav paths aligned to the studio and quarantine views", () => {
    const govern = APP_SHELL_NAV_SECTIONS.find((section) => section.label === "Govern");
    expect(govern).toBeDefined();

    expect(govern?.items.map((item) => item.path)).toEqual([
      "/govern/overview",
      "/govern/quarantine",
    ]);

    const quarantine = govern?.items.find((item) => item.label === "Quarantine");
    expect(quarantine?.path).toBe("/govern/quarantine");
    expect(quarantine?.badge).toBe("quarantine");
  });

  it("updates g+key navigation to GOVERN policy routes", () => {
    expect(APP_SHELL_G_KEY_MAP.p).toBe("/govern/overview?tab=input-rules");
    expect(APP_SHELL_G_KEY_MAP.v).toBe("/govern/overview?tab=velocity");
    expect(APP_SHELL_G_KEY_MAP.e).toBe("/govern/overview?tab=evaluation&mode=analytics");
    expect(APP_SHELL_G_KEY_MAP.t).toBe("/govern/overview?tab=scope");
    expect(APP_SHELL_G_KEY_MAP.q).toBe("/govern/quarantine");
    expect(APP_SHELL_G_KEY_MAP.b).toBe("/govern/overview?tab=bundles");
  });
});

describe("AppShell g-key map completeness", () => {
  it("does NOT contain stale /traces route", () => {
    expect(Object.values(APP_SHELL_G_KEY_MAP)).not.toContain("/traces");
  });

  it("includes approvals (g+k) and packs (g+x) shortcuts", () => {
    expect(APP_SHELL_G_KEY_MAP.k).toBe("/approvals");
    expect(APP_SHELL_G_KEY_MAP.x).toBe("/packs");
  });

  it("maps both h and o to home", () => {
    expect(APP_SHELL_G_KEY_MAP.h).toBe("/");
    expect(APP_SHELL_G_KEY_MAP.o).toBe("/");
  });
});
