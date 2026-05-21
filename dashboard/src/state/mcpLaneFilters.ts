/*
 * EDGE-105 — Zustand slice + URL-sync helpers for the MCP-lane chip-toggle
 * filter on Edge Session detail.
 *
 * Four chip keys ("servers", "tools", "approvals", "failures") gate which
 * MCP-lane rows are visible. Default state is "all four chips active"
 * (the URL omits `?mcp_lane=` entirely so users land on the discoverable
 * full view). User toggles update both the Zustand state and the URL
 * search param. Invalid URL tokens are silently ignored — when no valid
 * token remains, we fall back to all-active defaults rather than render
 * the all-deselected empty-state on a URL accident.
 */

import { create } from "zustand";

export type McpLaneChip = "servers" | "tools" | "approvals" | "failures";

const CHIP_ORDER: readonly McpLaneChip[] = ["servers", "tools", "approvals", "failures"] as const;
export const ALL_MCP_LANE_CHIPS: ReadonlySet<McpLaneChip> = new Set(CHIP_ORDER);

interface McpLaneFiltersStore {
  chips: Set<McpLaneChip>;
  toggle: (chip: McpLaneChip) => void;
  setChips: (chips: Set<McpLaneChip>) => void;
  reset: () => void;
}

export const useMcpLaneFiltersStore = create<McpLaneFiltersStore>((set) => ({
  chips: new Set(ALL_MCP_LANE_CHIPS),
  toggle: (chip) =>
    set((state) => {
      const next = new Set(state.chips);
      if (next.has(chip)) next.delete(chip);
      else next.add(chip);
      return { chips: next };
    }),
  setChips: (chips) => set({ chips: new Set(chips) }),
  reset: () => set({ chips: new Set(ALL_MCP_LANE_CHIPS) }),
}));

/**
 * resetMcpLaneFiltersForTests is the test-cleanup hook — every MCPLane
 * test calls this in beforeEach to start from the default-active state.
 * Production code never calls this.
 */
export function resetMcpLaneFiltersForTests(): void {
  useMcpLaneFiltersStore.getState().reset();
}

/**
 * parseMcpLaneFromUrl converts the `?mcp_lane=` query value into a set
 * of valid chip keys. Invalid tokens are dropped silently; an all-empty
 * parse falls back to the default all-active set so a URL-typo doesn't
 * lock the user into the empty-filter state.
 */
export function parseMcpLaneFromUrl(rawSearchValue: string | null): Set<McpLaneChip> {
  if (!rawSearchValue) {
    return new Set(ALL_MCP_LANE_CHIPS);
  }
  const tokens = rawSearchValue
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
  const valid: McpLaneChip[] = [];
  for (const token of tokens) {
    if (ALL_MCP_LANE_CHIPS.has(token as McpLaneChip)) {
      valid.push(token as McpLaneChip);
    }
  }
  if (valid.length === 0) {
    return new Set(ALL_MCP_LANE_CHIPS);
  }
  return new Set(valid);
}

/**
 * serializeMcpLaneToUrl turns the chip set into a stable URL value.
 * When every chip is active (the default), returns undefined so the
 * caller can omit the query param entirely.
 */
export function serializeMcpLaneToUrl(chips: Set<McpLaneChip>): string | undefined {
  if (chips.size === CHIP_ORDER.length && CHIP_ORDER.every((c) => chips.has(c))) {
    return undefined;
  }
  return CHIP_ORDER.filter((c) => chips.has(c)).join(",");
}
