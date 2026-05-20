import { describe, expect, it } from "vitest";
import {
  APP_SHELL_G_KEY_MAP,
  APP_SHELL_NAV_SECTIONS,
  aggregateBadgeClassMap,
  aggregateSectionBadgeSeverity,
  deriveSystemStatus,
  findActiveSection,
  statusColorMap,
} from "./AppShell";

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
    // Verification nav entry was added under task-14d012e6 (chain-integrity
    // monitoring) — admin-gated at the route level via RequireRole, but
    // visible in nav. Delegations stays gated by FEATURE_FLAGS.delegationDashboard.
    expect(labels).toEqual([
      "Policy Studio",
      "Quarantine",
      "Verification",
    ]);
  });

  it("keeps the default governance nav paths aligned to the studio and quarantine views", () => {
    const govern = APP_SHELL_NAV_SECTIONS.find((section) => section.label === "Govern");
    expect(govern).toBeDefined();

    expect(govern?.items.map((item) => item.path)).toEqual([
      "/govern/overview",
      "/govern/quarantine",
      "/govern/verification",
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

describe("AppShell findActiveSection", () => {
  it("matches root '/' to Run via the end-flagged Dashboard item", () => {
    expect(findActiveSection("/", APP_SHELL_NAV_SECTIONS)).toBe("Run");
  });

  it("does NOT match /agents-foo to Run (avoids /agents prefix collision)", () => {
    expect(findActiveSection("/agents-foo", APP_SHELL_NAV_SECTIONS)).toBe(null);
  });

  it("matches /agents/abc to Run via /agents prefix", () => {
    expect(findActiveSection("/agents/abc", APP_SHELL_NAV_SECTIONS)).toBe("Run");
  });

  it("matches /edge/sessions to Run via the visible Edge Sessions item", () => {
    expect(findActiveSection("/edge/sessions", APP_SHELL_NAV_SECTIONS)).toBe("Run");
  });

  it("matches /edge/sessions/abc detail path to Run", () => {
    expect(findActiveSection("/edge/sessions/abc", APP_SHELL_NAV_SECTIONS)).toBe("Run");
  });

  it("does NOT match /edge/approvals because it is a hidden compatibility redirect", () => {
    expect(findActiveSection("/edge/approvals", APP_SHELL_NAV_SECTIONS)).toBe(null);
  });

  it("does NOT match /edge/audit because it is a hidden compatibility redirect", () => {
    expect(findActiveSection("/edge/audit", APP_SHELL_NAV_SECTIONS)).toBe(null);
  });

  it("matches /govern/overview to Govern", () => {
    expect(findActiveSection("/govern/overview", APP_SHELL_NAV_SECTIONS)).toBe("Govern");
  });

  it("matches non-visible /govern deep links to Govern", () => {
    expect(findActiveSection("/govern/bundles/bundle-1", APP_SHELL_NAV_SECTIONS)).toBe("Govern");
    expect(findActiveSection("/govern/replay", APP_SHELL_NAV_SECTIONS)).toBe("Govern");
    expect(findActiveSection("/govern/tenants/tenant-1", APP_SHELL_NAV_SECTIONS)).toBe("Govern");
  });

  it("does NOT match /governance to Govern (avoids /govern prefix collision)", () => {
    expect(findActiveSection("/governance", APP_SHELL_NAV_SECTIONS)).toBe(null);
  });

  it("matches /govern/quarantine to Govern (badge route)", () => {
    expect(findActiveSection("/govern/quarantine", APP_SHELL_NAV_SECTIONS)).toBe("Govern");
  });

  it("matches /packs/abc to Catalog", () => {
    expect(findActiveSection("/packs/abc", APP_SHELL_NAV_SECTIONS)).toBe("Catalog");
  });

  it("matches /audit to Audit", () => {
    expect(findActiveSection("/audit", APP_SHELL_NAV_SECTIONS)).toBe("Audit");
  });

  it("does NOT match /dlq to any section after Dead Letters sidebar removal", () => {
    // task-266f21ad: Dead Letters sidebar entry removed (DLQ folded into
    // JobsPage as ?status=dlq). The /dlq URL still redirects via
    // App.tsx::DlqRouteRedirect for bookmarked links, but no sidebar
    // section claims the pathname any more.
    expect(findActiveSection("/dlq", APP_SHELL_NAV_SECTIONS)).toBe(null);
  });

  it("matches /settings/* sub-routes to Settings", () => {
    expect(findActiveSection("/settings", APP_SHELL_NAV_SECTIONS)).toBe("Settings");
    expect(findActiveSection("/settings/users", APP_SHELL_NAV_SECTIONS)).toBe("Settings");
    expect(findActiveSection("/settings/audit-export", APP_SHELL_NAV_SECTIONS)).toBe("Settings");
  });

  it("returns null for unknown routes (e.g. /not-a-real-route)", () => {
    expect(findActiveSection("/not-a-real-route", APP_SHELL_NAV_SECTIONS)).toBe(null);
  });
});

describe("AppShell sidebar accordion structure", () => {
  it("groups items into 5 customer-language sections without a standalone Edge section", () => {
    expect(APP_SHELL_NAV_SECTIONS.map((s) => s.label)).toEqual([
      "Run",
      "Govern",
      "Catalog",
      "Audit",
      "Settings",
    ]);
    expect(APP_SHELL_NAV_SECTIONS.some((s) => s.label === "Edge")).toBe(false);
  });

  it("Run section owns the single visible Edge Sessions destination", () => {
    const run = APP_SHELL_NAV_SECTIONS.find((s) => s.label === "Run");
    expect(run?.items.map((i) => i.label)).toEqual([
      "Dashboard",
      "Agents",
      "Jobs",
      "Workflows",
      "Approvals",
      "Edge Sessions",
    ]);
    expect(run?.items.map((i) => i.path)).toContain("/edge/sessions");
    expect(run?.items.map((i) => i.path)).not.toContain("/edge/approvals");
    expect(run?.items.map((i) => i.path)).not.toContain("/edge/audit");
    expect(APP_SHELL_NAV_SECTIONS.flatMap((s) => s.items.map((i) => i.label))).not.toContain("Edge Approvals");
    expect(APP_SHELL_NAV_SECTIONS.flatMap((s) => s.items.map((i) => i.label))).not.toContain("Edge Audit");
  });

  it("Audit section no longer surfaces Dead Letters (folded into Jobs?status=dlq)", () => {
    const audit = APP_SHELL_NAV_SECTIONS.find((s) => s.label === "Audit");
    expect(audit?.items.map((i) => i.path)).toEqual(["/audit"]);
    expect(audit?.items.map((i) => i.path)).not.toContain("/dlq");
  });

  it("Settings section has the Hub item with end:true to avoid prefix-matching sub-routes", () => {
    const settings = APP_SHELL_NAV_SECTIONS.find((s) => s.label === "Settings");
    expect(settings).toBeDefined();
    expect(settings?.items[0]).toMatchObject({
      path: "/settings",
      label: "Hub",
      end: true,
    });
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

describe("aggregateSectionBadgeSeverity", () => {
  const counts: Record<string, number> = {
    approvals: 0,
    dlq: 0,
    quarantine: 0,
  };
  const getCount = (badge?: string) => (badge ? (counts[badge] ?? 0) : 0);

  it("returns 'warning' for an approvals-only section with non-zero count", () => {
    counts.approvals = 3;
    counts.dlq = 0;
    counts.quarantine = 0;
    const severity = aggregateSectionBadgeSeverity(
      [{ badge: "approvals" }],
      getCount,
    );
    expect(severity).toBe("warning");
    expect(aggregateBadgeClassMap.warning).toBe("bg-status-warning/20 text-status-warning");
  });

  it("returns 'error' for a quarantine-only section with non-zero count", () => {
    counts.approvals = 0;
    counts.dlq = 0;
    counts.quarantine = 5;
    const severity = aggregateSectionBadgeSeverity(
      [{ badge: "quarantine" }],
      getCount,
    );
    expect(severity).toBe("error");
    expect(aggregateBadgeClassMap.error).toBe("bg-status-error/20 text-status-error");
  });

  it("returns 'error' (highest tier) for a mixed approvals + dlq section", () => {
    counts.approvals = 2;
    counts.dlq = 4;
    counts.quarantine = 0;
    const severity = aggregateSectionBadgeSeverity(
      [{ badge: "approvals" }, { badge: "dlq" }],
      getCount,
    );
    expect(severity).toBe("error");
  });

  it("returns null when items have badges but all counts are zero", () => {
    counts.approvals = 0;
    counts.dlq = 0;
    counts.quarantine = 0;
    const severity = aggregateSectionBadgeSeverity(
      [{ badge: "dlq" }, { badge: "approvals" }],
      getCount,
    );
    expect(severity).toBeNull();
  });

  it("returns null when no items carry a badge prop", () => {
    counts.approvals = 99;
    const severity = aggregateSectionBadgeSeverity(
      [{}, { badge: undefined }],
      getCount,
    );
    expect(severity).toBeNull();
  });

  it("ignores items whose badge type is outside the known severity map", () => {
    // A future caller could pass a string outside the NavItem union (e.g.
    // via raw cast). The helper must NOT promote unknown badges to a
    // severity tier — the type system already covers the closed union, so
    // this guards against runtime drift.
    counts.approvals = 0;
    counts.dlq = 0;
    counts.quarantine = 0;
    const unknownBadge = "info" as unknown as NonNullable<{ badge?: "approvals" | "dlq" | "quarantine" }["badge"]>;
    const getInfoCount = (badge?: string) => (badge === "info" ? 7 : 0);
    const severity = aggregateSectionBadgeSeverity(
      [{ badge: unknownBadge }],
      getInfoCount,
    );
    expect(severity).toBeNull();
  });
});
