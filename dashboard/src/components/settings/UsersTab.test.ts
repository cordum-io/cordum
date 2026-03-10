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

import { ROLES, roleBadgeVariant, timeAgo, createUserSchema } from "./UsersTab";

// ---------------------------------------------------------------------------
// roleBadgeVariant
// ---------------------------------------------------------------------------

describe("roleBadgeVariant", () => {
  it("Admin → success", () => {
    expect(roleBadgeVariant("Admin")).toBe("success");
  });

  it("Operator → info", () => {
    expect(roleBadgeVariant("Operator")).toBe("info");
  });

  it("Approver → warning", () => {
    expect(roleBadgeVariant("Approver")).toBe("warning");
  });

  it("Viewer → default", () => {
    expect(roleBadgeVariant("Viewer")).toBe("default");
  });

  it("unknown role → default", () => {
    expect(roleBadgeVariant("SuperAdmin")).toBe("default");
  });
});

// ---------------------------------------------------------------------------
// timeAgo
// ---------------------------------------------------------------------------

describe("timeAgo", () => {
  it("returns dash for undefined", () => {
    expect(timeAgo()).toBe("\u2014");
  });

  it("returns dash for empty string", () => {
    expect(timeAgo("")).toBe("\u2014");
  });

  it("returns seconds ago for recent timestamps", () => {
    const fiveSecsAgo = new Date(Date.now() - 5_000).toISOString();
    expect(timeAgo(fiveSecsAgo)).toMatch(/^\d+s ago$/);
  });

  it("returns minutes ago for timestamps 1-59 minutes old", () => {
    const threeMinAgo = new Date(Date.now() - 3 * 60_000).toISOString();
    expect(timeAgo(threeMinAgo)).toBe("3m ago");
  });

  it("returns hours ago for timestamps 1-23 hours old", () => {
    const twoHrsAgo = new Date(Date.now() - 2 * 3_600_000).toISOString();
    expect(timeAgo(twoHrsAgo)).toBe("2h ago");
  });

  it("returns days ago for timestamps 24+ hours old", () => {
    const threeDaysAgo = new Date(Date.now() - 3 * 86_400_000).toISOString();
    expect(timeAgo(threeDaysAgo)).toBe("3d ago");
  });
});

// ---------------------------------------------------------------------------
// ROLES constant
// ---------------------------------------------------------------------------

describe("ROLES", () => {
  it("has 4 entries", () => {
    expect(ROLES).toHaveLength(4);
  });

  it("contains expected roles", () => {
    expect(ROLES).toContain("Admin");
    expect(ROLES).toContain("Operator");
    expect(ROLES).toContain("Viewer");
    expect(ROLES).toContain("Approver");
  });
});

// ---------------------------------------------------------------------------
// createUserSchema (Zod)
// ---------------------------------------------------------------------------

describe("createUserSchema", () => {
  it("accepts valid data", () => {
    const result = createUserSchema.safeParse({
      username: "jane.doe",
      password: "StrongP@ss1234",
      role: "Viewer",
    });
    expect(result.success).toBe(true);
  });

  it("rejects short username", () => {
    const result = createUserSchema.safeParse({
      username: "ab",
      password: "StrongP@ss1234",
      role: "Viewer",
    });
    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.issues[0].path).toContain("username");
    }
  });

  it("rejects short password", () => {
    const result = createUserSchema.safeParse({
      username: "jane.doe",
      password: "Short1!",
      role: "Viewer",
    });
    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.issues[0].path).toContain("password");
    }
  });

  it("rejects password without uppercase", () => {
    const result = createUserSchema.safeParse({
      username: "jane.doe",
      password: "alllowercase1!",
      role: "Viewer",
    });
    expect(result.success).toBe(false);
  });

  it("rejects password without digit", () => {
    const result = createUserSchema.safeParse({
      username: "jane.doe",
      password: "NoDigitsHere!!A",
      role: "Viewer",
    });
    expect(result.success).toBe(false);
  });

  it("rejects password without special character", () => {
    const result = createUserSchema.safeParse({
      username: "jane.doe",
      password: "NoSpecialChar12A",
      role: "Viewer",
    });
    expect(result.success).toBe(false);
  });

  it("rejects empty role", () => {
    const result = createUserSchema.safeParse({
      username: "jane.doe",
      password: "StrongP@ss1234",
      role: "",
    });
    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.issues[0].path).toContain("role");
    }
  });
});
