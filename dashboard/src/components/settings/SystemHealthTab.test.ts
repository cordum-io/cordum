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

import {
  formatUptime,
  statusVariant,
  parseMaybeBool,
  parseOutputPolicy,
  mapGatewayStatus,
} from "./SystemHealthTab";
import type { GatewayStatus } from "./SystemHealthTab";

// ---------------------------------------------------------------------------
// formatUptime
// ---------------------------------------------------------------------------

describe("formatUptime", () => {
  it("returns dash for undefined", () => {
    expect(formatUptime()).toBe("\u2014");
  });

  it("returns dash for null", () => {
    expect(formatUptime(null as unknown as undefined)).toBe("\u2014");
  });

  it("returns seconds for < 60", () => {
    expect(formatUptime(42)).toBe("42s");
  });

  it("returns minutes for 60..3599", () => {
    expect(formatUptime(120)).toBe("2m");
    expect(formatUptime(3599)).toBe("59m");
  });

  it("returns hours for 3600..86399", () => {
    expect(formatUptime(3600)).toBe("1h");
    expect(formatUptime(7260)).toBe("2h 1m");
  });

  it("returns hours without remainder if even", () => {
    expect(formatUptime(7200)).toBe("2h");
  });

  it("returns days for >= 86400", () => {
    expect(formatUptime(86400)).toBe("1d");
    expect(formatUptime(90000)).toBe("1d 1h");
  });

  it("returns days without remainder if even", () => {
    expect(formatUptime(172800)).toBe("2d");
  });
});

// ---------------------------------------------------------------------------
// statusVariant
// ---------------------------------------------------------------------------

describe("statusVariant", () => {
  it("healthy -> success", () => {
    expect(statusVariant("healthy")).toBe("success");
  });

  it("degraded -> warning", () => {
    expect(statusVariant("degraded")).toBe("warning");
  });

  it("down -> danger", () => {
    expect(statusVariant("down")).toBe("danger");
  });

  it("unknown -> danger (default)", () => {
    expect(statusVariant("unknown")).toBe("danger");
  });
});

// ---------------------------------------------------------------------------
// parseMaybeBool
// ---------------------------------------------------------------------------

describe("parseMaybeBool", () => {
  it("returns true for boolean true", () => {
    expect(parseMaybeBool(true)).toBe(true);
  });

  it("returns false for boolean false", () => {
    expect(parseMaybeBool(false)).toBe(false);
  });

  it("returns true for truthy strings", () => {
    for (const v of ["true", "True", "TRUE", "1", "yes", "on"]) {
      expect(parseMaybeBool(v)).toBe(true);
    }
  });

  it("returns false for falsy strings", () => {
    for (const v of ["false", "False", "FALSE", "0", "no", "off"]) {
      expect(parseMaybeBool(v)).toBe(false);
    }
  });

  it("returns undefined for non-boolean non-string", () => {
    expect(parseMaybeBool(42)).toBeUndefined();
    expect(parseMaybeBool(null)).toBeUndefined();
    expect(parseMaybeBool(undefined)).toBeUndefined();
  });

  it("returns undefined for unrecognized strings", () => {
    expect(parseMaybeBool("maybe")).toBeUndefined();
    expect(parseMaybeBool("")).toBeUndefined();
  });
});

// ---------------------------------------------------------------------------
// parseOutputPolicy
// ---------------------------------------------------------------------------

describe("parseOutputPolicy", () => {
  it("reads from status.output_policy when present", () => {
    const status: GatewayStatus = {
      output_policy: { enabled: true, fail_mode: "closed" },
    };
    const result = parseOutputPolicy(status);
    expect(result.enabled).toBe(true);
    expect(result.failMode).toBe("closed");
  });

  it("falls back to cfg nested output_policy", () => {
    const status: GatewayStatus = {};
    const cfg = {
      output_policy: { enabled: false, fail_mode: "open" },
    };
    const result = parseOutputPolicy(status, cfg);
    expect(result.enabled).toBe(false);
    expect(result.failMode).toBe("open");
  });

  it("falls back to flat cfg keys", () => {
    const status: GatewayStatus = {};
    const cfg = {
      output_policy_enabled: "true",
      output_policy_fail_mode: "closed",
    };
    const result = parseOutputPolicy(status, cfg);
    expect(result.enabled).toBe(true);
    expect(result.failMode).toBe("closed");
  });

  it("falls back to camelCase cfg keys", () => {
    const status: GatewayStatus = {};
    const cfg = {
      outputPolicyEnabled: "yes",
      outputPolicyFailMode: "open",
    };
    const result = parseOutputPolicy(status, cfg);
    expect(result.enabled).toBe(true);
    expect(result.failMode).toBe("open");
  });

  it("returns empty object when nothing available", () => {
    const result = parseOutputPolicy({});
    expect(result).toEqual({});
  });

  it("returns empty object when cfg is undefined", () => {
    const result = parseOutputPolicy({}, undefined);
    expect(result).toEqual({});
  });
});

// ---------------------------------------------------------------------------
// mapGatewayStatus — overall health derivation
// ---------------------------------------------------------------------------

describe("mapGatewayStatus", () => {
  it("all healthy: overall is healthy", () => {
    const status: GatewayStatus = {
      redis: { ok: true, latency_ms: 1 },
      nats: { connected: true, latency_ms: 2 },
      workers: { count: 3 },
      uptime_seconds: 600,
      build: { version: "1.0.0" },
      output_policy: { enabled: true },
    };
    const result = mapGatewayStatus(status);
    expect(result.overall).toBe("healthy");
    expect(result.components).toHaveLength(5);
  });

  it("Redis down: overall is down", () => {
    const status: GatewayStatus = {
      redis: { ok: false, error: "connection refused" },
      nats: { connected: true },
      workers: { count: 1 },
      output_policy: { enabled: true },
    };
    const result = mapGatewayStatus(status);
    expect(result.overall).toBe("down");
    const redis = result.components.find((c) => c.name === "Redis");
    expect(redis?.status).toBe("down");
  });

  it("NATS disconnected: overall is degraded", () => {
    const status: GatewayStatus = {
      redis: { ok: true },
      nats: { connected: false },
      workers: { count: 1 },
      output_policy: { enabled: true },
    };
    const result = mapGatewayStatus(status);
    expect(result.overall).toBe("degraded");
    const nats = result.components.find((c) => c.name === "NATS");
    expect(nats?.status).toBe("degraded");
  });

  it("zero workers: overall is degraded", () => {
    const status: GatewayStatus = {
      redis: { ok: true },
      nats: { connected: true },
      workers: { count: 0 },
      output_policy: { enabled: true },
    };
    const result = mapGatewayStatus(status);
    expect(result.overall).toBe("degraded");
    const workers = result.components.find((c) => c.name === "Workers");
    expect(workers?.status).toBe("degraded");
  });

  it("Gateway is always healthy", () => {
    const status: GatewayStatus = {};
    const result = mapGatewayStatus(status);
    const gw = result.components.find((c) => c.name === "Gateway");
    expect(gw?.status).toBe("healthy");
  });

  it("output policy disabled: degraded", () => {
    const status: GatewayStatus = {
      redis: { ok: true },
      nats: { connected: true },
      workers: { count: 1 },
      output_policy: { enabled: false },
    };
    const result = mapGatewayStatus(status);
    const op = result.components.find((c) => c.name === "Output Policy");
    expect(op?.status).toBe("degraded");
  });

  it("output policy enabled from cfg: healthy", () => {
    const status: GatewayStatus = {
      redis: { ok: true },
      nats: { connected: true },
      workers: { count: 1 },
    };
    const cfg = { output_policy: { enabled: true } };
    const result = mapGatewayStatus(status, cfg);
    const op = result.components.find((c) => c.name === "Output Policy");
    expect(op?.status).toBe("healthy");
  });

  it("down beats degraded for overall", () => {
    const status: GatewayStatus = {
      redis: { ok: false }, // down
      nats: { connected: false }, // degraded
      workers: { count: 0 }, // degraded
    };
    const result = mapGatewayStatus(status);
    expect(result.overall).toBe("down");
  });

  it("sets checkedAt to ISO string", () => {
    const before = Date.now();
    const result = mapGatewayStatus({});
    const after = Date.now();
    const ts = new Date(result.checkedAt).getTime();
    expect(ts).toBeGreaterThanOrEqual(before);
    expect(ts).toBeLessThanOrEqual(after);
  });

  it("passes latency and version through to components", () => {
    const status: GatewayStatus = {
      redis: { ok: true, latency_ms: 5 },
      nats: { connected: true, latency_ms: 3 },
      build: { version: "2.0.0" },
      uptime_seconds: 1200,
    };
    const result = mapGatewayStatus(status);
    const redis = result.components.find((c) => c.name === "Redis");
    expect(redis?.latencyMs).toBe(5);
    const nats = result.components.find((c) => c.name === "NATS");
    expect(nats?.latencyMs).toBe(3);
    const gw = result.components.find((c) => c.name === "Gateway");
    expect(gw?.version).toBe("2.0.0");
    expect(gw?.uptime).toBe(1200);
  });

  it("produces 5 components (Redis, NATS, Workers, Gateway, Output Policy)", () => {
    const result = mapGatewayStatus({});
    const names = result.components.map((c) => c.name);
    expect(names).toEqual(["Redis", "NATS", "Workers", "Gateway", "Output Policy"]);
  });
});
