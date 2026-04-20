import { describe, it, expect, beforeEach } from "vitest";
import {
  loadSavedFilters,
  saveSavedFilter,
  deleteSavedFilter,
  updateSavedFilter,
  generateFilterId,
  summarizeFilters,
} from "./audit-filters";
import type { SavedAuditFilter, SerializedFilterState } from "./audit-filters";

beforeEach(() => {
  localStorage.clear();
});

describe("loadSavedFilters", () => {
  it("returns built-in presets when localStorage is empty", () => {
    const filters = loadSavedFilters();
    expect(filters.length).toBeGreaterThanOrEqual(3);
    expect(filters.some((f) => f.builtIn)).toBe(true);
  });

  it("includes user-saved filters", () => {
    const custom: SavedAuditFilter = {
      id: "user-1",
      name: "My Filter",
      filters: { actor: "admin" },
      createdAt: new Date().toISOString(),
    };
    localStorage.setItem("cordum:audit:savedFilters", JSON.stringify([custom]));
    const all = loadSavedFilters();
    expect(all.some((f) => f.id === "user-1")).toBe(true);
  });

  it("handles corrupt localStorage gracefully", () => {
    localStorage.setItem("cordum:audit:savedFilters", "not-json");
    const filters = loadSavedFilters();
    // Should still return built-in presets
    expect(filters.length).toBeGreaterThanOrEqual(3);
  });
});

describe("saveSavedFilter", () => {
  it("persists a new filter", () => {
    saveSavedFilter({
      id: "new-1",
      name: "New",
      filters: {},
      createdAt: new Date().toISOString(),
    });
    const all = loadSavedFilters();
    expect(all.some((f) => f.id === "new-1")).toBe(true);
  });
});

describe("deleteSavedFilter", () => {
  it("removes a user filter", () => {
    saveSavedFilter({
      id: "del-1",
      name: "ToDelete",
      filters: {},
      createdAt: new Date().toISOString(),
    });
    deleteSavedFilter("del-1");
    const all = loadSavedFilters();
    expect(all.some((f) => f.id === "del-1")).toBe(false);
  });

  it("does not delete built-in presets", () => {
    const before = loadSavedFilters();
    const builtIn = before.find((f) => f.builtIn);
    if (builtIn) {
      deleteSavedFilter(builtIn.id);
      const after = loadSavedFilters();
      expect(after.some((f) => f.id === builtIn.id)).toBe(true);
    }
  });
});

describe("updateSavedFilter", () => {
  it("updates a user filter's name", () => {
    saveSavedFilter({
      id: "upd-1",
      name: "Old Name",
      filters: {},
      createdAt: new Date().toISOString(),
    });
    updateSavedFilter("upd-1", { name: "New Name" });
    const all = loadSavedFilters();
    const updated = all.find((f) => f.id === "upd-1");
    expect(updated?.name).toBe("New Name");
  });

  it("does not update built-in presets", () => {
    const before = loadSavedFilters();
    const builtIn = before.find((f) => f.builtIn);
    if (builtIn) {
      updateSavedFilter(builtIn.id, { name: "Hacked" });
      const after = loadSavedFilters();
      const same = after.find((f) => f.id === builtIn.id);
      expect(same?.name).toBe(builtIn.name);
    }
  });
});

describe("generateFilterId", () => {
  it("returns a non-empty string", () => {
    expect(generateFilterId().length).toBeGreaterThan(0);
  });

  it("generates unique ids", () => {
    const ids = new Set(Array.from({ length: 100 }, () => generateFilterId()));
    expect(ids.size).toBe(100);
  });
});

describe("summarizeFilters", () => {
  it("returns 'No filters' for empty state", () => {
    expect(summarizeFilters({})).toBe("No filters");
  });

  it("includes event types", () => {
    const filters: SerializedFilterState = { eventType: ["allow", "deny"] };
    expect(summarizeFilters(filters)).toContain("allow");
    expect(summarizeFilters(filters)).toContain("deny");
  });

  it("includes actor", () => {
    expect(summarizeFilters({ actor: "admin" })).toContain("actor: admin");
  });

  it("includes severity", () => {
    expect(summarizeFilters({ severity: ["high"] })).toContain("high severity");
  });

  it("includes search in quotes", () => {
    expect(summarizeFilters({ search: "test" })).toContain('"test"');
  });

  it("joins multiple parts with dot separator", () => {
    const result = summarizeFilters({ actor: "admin", timeRange: "24h" });
    expect(result).toContain(" · ");
  });
});
