import { describe, it, expect, beforeEach } from "vitest";
import { useUiStore } from "../state/ui";

describe("CommandPalette (store integration)", () => {
  beforeEach(() => {
    useUiStore.setState({ commandOpen: false });
  });

  it("starts closed", () => {
    expect(useUiStore.getState().commandOpen).toBe(false);
  });

  it("opens via setCommandOpen(true)", () => {
    useUiStore.getState().setCommandOpen(true);
    expect(useUiStore.getState().commandOpen).toBe(true);
  });

  it("closes via setCommandOpen(false)", () => {
    useUiStore.getState().setCommandOpen(true);
    useUiStore.getState().setCommandOpen(false);
    expect(useUiStore.getState().commandOpen).toBe(false);
  });

  it("toggles open/close", () => {
    const { setCommandOpen } = useUiStore.getState();
    setCommandOpen(true);
    expect(useUiStore.getState().commandOpen).toBe(true);
    setCommandOpen(false);
    expect(useUiStore.getState().commandOpen).toBe(false);
  });
});

describe("CommandPalette routing logic", () => {
  // Mirrors resultPath() from CommandPalette.tsx
  function resultPath(result: { type: string; id: string }): string {
    switch (result.type) {
      case "job":
        return `/jobs/${result.id}`;
      case "workflow":
        return `/workflows/${result.id}`;
      case "run":
        return `/workflows`;
      case "pack":
        return `/packs`;
      default:
        return "/";
    }
  }

  it("routes jobs to /jobs/:id", () => {
    expect(resultPath({ type: "job", id: "j-123" })).toBe("/jobs/j-123");
  });

  it("routes workflows to /workflows/:id", () => {
    expect(resultPath({ type: "workflow", id: "wf-1" })).toBe("/workflows/wf-1");
  });

  it("routes runs to /workflows (merged tab)", () => {
    expect(resultPath({ type: "run", id: "r-1" })).toBe("/workflows");
  });

  it("routes packs to /packs (list page)", () => {
    expect(resultPath({ type: "pack", id: "p-1" })).toBe("/packs");
  });

  it("grouping preserves type order", () => {
    const TYPE_ORDER = ["job", "workflow", "run", "pack"];
    const results = [
      { type: "pack", id: "p1" },
      { type: "job", id: "j1" },
      { type: "run", id: "r1" },
      { type: "workflow", id: "w1" },
    ];
    const grouped = TYPE_ORDER
      .map((type) => ({
        type,
        items: results.filter((r) => r.type === type),
      }))
      .filter((g) => g.items.length > 0);

    expect(grouped.map((g) => g.type)).toEqual(["job", "workflow", "run", "pack"]);
  });
});
