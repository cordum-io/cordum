import { describe, expect, it } from "vitest";
import type { EdgeAssuranceState } from "@/api/types";
import { render, screen } from "@/test-utils/render";
import { ASSURANCE_STATES, AssuranceStateBadge } from "./AssuranceStateBadge";

function badgeFor(state?: EdgeAssuranceState) {
  render(<AssuranceStateBadge state={state} />);
  return screen.getByTestId("edge-assurance-badge");
}

describe("AssuranceStateBadge", () => {
  it.each([
    ["executor-enforced", "Executor-enforced"],
    ["hook-mediated", "Hook-mediated"],
    ["compatibility", "Compatibility"],
    ["unsupported", "Unsupported"],
    ["stale", "Stale"],
    [undefined, "Not reported"],
  ] as const)("renders %s as %s", (state, label) => {
    const badge = badgeFor(state);
    expect(badge.textContent).toBe(label);
    expect(badge.getAttribute("data-assurance-state")).toBe(state ?? "not-reported");
    expect(badge.getAttribute("title")).toBe("Assurance state");
  });

  it.each([...ASSURANCE_STATES.filter((s) => s !== "executor-enforced"), undefined])(
    "never says enforced for %s",
    (state) => {
      expect(badgeFor(state).textContent).not.toMatch(/enforced/i);
    },
  );

  it("lists the five states in display order for the filter", () => {
    expect(ASSURANCE_STATES).toEqual(["executor-enforced", "hook-mediated", "compatibility", "unsupported", "stale"]);
  });
});
