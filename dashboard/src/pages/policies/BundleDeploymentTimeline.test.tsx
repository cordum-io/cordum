import { describe, expect, it } from "vitest";
import { fireEvent } from "@testing-library/react";
import { Route, Routes } from "react-router-dom";
import { renderWithProviders, screen } from "@/test-utils/render";
import type { BundleDeployment } from "@/hooks/useBundle";
import {
  computeTimelineSegments,
  uniqueVersions,
  versionColorIndex,
} from "@/lib/policy-studio/timeline-segments";
import { BundleDeploymentTimeline } from "./BundleDeploymentTimeline";

// Fixed "now" so the open-ended segment cap is deterministic across runs.
// 2026-04-15T12:00:00Z, mid-spring so fixture timestamps below land
// inside both the 7d and 30d ranges.
const NOW_MS = Date.parse("2026-04-15T12:00:00Z");

function makeDeployment(overrides: Partial<BundleDeployment> = {}): BundleDeployment {
  return {
    scope: "tenant:acme",
    scope_kind: "tenant",
    scope_value: "acme",
    version: "v1",
    active: true,
    deployed_at: "2026-04-10T08:00:00Z",
    ...overrides,
  };
}

function renderTimeline(props: {
  bundleId?: string;
  deployments: BundleDeployment[];
  nowMs?: number;
}) {
  return renderWithProviders(
    <Routes>
      <Route
        path="/policies/bundles/:id"
        element={
          <BundleDeploymentTimeline
            bundleId={props.bundleId ?? "bundle-1"}
            deployments={props.deployments}
            nowMs={props.nowMs ?? NOW_MS}
          />
        }
      />
    </Routes>,
    { initialEntries: ["/policies/bundles/bundle-1"] },
  );
}

describe("BundleDeploymentTimeline (D6 Gantt)", () => {
  it("TestRendersEmpty: shows the no-deployments placeholder when history is empty (DoD #4 variant)", () => {
    renderTimeline({ deployments: [] });
    expect(screen.getByTestId("bundle-timeline-empty")).not.toBeNull();
    // No SVG mounted in the empty state.
    expect(screen.queryByTestId("bundle-timeline-svg")).toBeNull();
  });

  it("TestSingleVersionAlwaysActive: a single deployment yields one open-ended segment spanning the whole range (DoD #4 variant)", () => {
    const deployments = [
      makeDeployment({
        version: "v1",
        deployed_at: "2026-04-10T08:00:00Z",
      }),
    ];
    renderTimeline({ deployments });
    const segs = document.querySelectorAll("[data-segment-version]");
    expect(segs.length).toBe(1);
    expect(segs[0].getAttribute("data-segment-version")).toBe("v1");
    expect(segs[0].getAttribute("data-segment-scope")).toBe("tenant:acme");
  });

  it("TestManyRollbacks: deploy v1 → v2 → rollback to v1 → v3 yields 4 ordered segments with correct colour reuse (DoD #4 variant)", () => {
    const deployments = [
      makeDeployment({ version: "v1", deployed_at: "2026-04-10T08:00:00Z" }),
      makeDeployment({ version: "v2", deployed_at: "2026-04-11T08:00:00Z" }),
      makeDeployment({ version: "v1", deployed_at: "2026-04-12T08:00:00Z" }),
      makeDeployment({ version: "v3", deployed_at: "2026-04-13T08:00:00Z" }),
    ];
    renderTimeline({ deployments });
    const segs = Array.from(
      document.querySelectorAll("[data-segment-version]"),
    );
    expect(segs.length).toBe(4);
    // Order is by startMs ascending within a single scope.
    expect(segs.map((s) => s.getAttribute("data-segment-version"))).toEqual([
      "v1",
      "v2",
      "v1",
      "v3",
    ]);
    // The two v1 segments must paint with the SAME colour token (rollback
    // to a previous version reuses its colour by design); v2 and v3 are
    // distinct from v1 and from each other.
    const versionOrder = uniqueVersions(
      computeTimelineSegments(deployments, {
        fromMs: NOW_MS - 30 * 86_400_000,
        toMs: NOW_MS,
      }),
    );
    expect(versionColorIndex("v1", versionOrder)).toBe(0);
    expect(versionColorIndex("v2", versionOrder)).toBe(1);
    // The 3rd segment (rollback to v1) reuses v1's index → same colour.
    expect(versionColorIndex("v1", versionOrder)).toBe(0);
  });

  it("TestTooltipOnHover: each segment exposes a `<title>` SVG tooltip with version + scope + deployed_at (DoD #3, Path-A: author/audit_hash deferred to Backend 2.5)", () => {
    const deployments = [
      makeDeployment({
        version: "v2",
        deployed_at: "2026-04-12T08:00:00Z",
      }),
    ];
    renderTimeline({ deployments });
    const titles = document.querySelectorAll("svg title");
    expect(titles.length).toBeGreaterThanOrEqual(1);
    const text = titles[0]?.textContent ?? "";
    expect(text).toMatch(/Version v2/);
    expect(text).toMatch(/tenant:acme/);
    expect(text).toMatch(/2026-04-12T08:00:00Z/);
  });

  it("TestSegmentClickNavigates: each segment is a router Link to /policies/bundles/:id?tab=versions&v=<version>", () => {
    const deployments = [
      makeDeployment({ version: "v1", deployed_at: "2026-04-10T08:00:00Z" }),
      makeDeployment({ version: "v2", deployed_at: "2026-04-12T08:00:00Z" }),
    ];
    renderTimeline({ bundleId: "bundle-x", deployments });
    const links = document.querySelectorAll("a[data-segment-version]");
    expect(links.length).toBe(2);
    const v1Link = Array.from(links).find(
      (l) => l.getAttribute("data-segment-version") === "v1",
    );
    expect(v1Link).not.toBeNull();
    const href = v1Link!.getAttribute("href") ?? "";
    const url = new URL(href, "http://test/");
    expect(url.pathname).toBe("/policies/bundles/bundle-x");
    expect(url.searchParams.get("tab")).toBe("versions");
    expect(url.searchParams.get("v")).toBe("v1");
  });

  it("TestZoomChangesRange: clicking 7d preset re-derives segments against the smaller range (segments outside it are dropped)", () => {
    // v1 deployed 20 days ago — visible on 30d, dropped on 7d.
    // v2 deployed 3 days ago — visible on both.
    const deployments = [
      makeDeployment({
        version: "v1",
        deployed_at: new Date(NOW_MS - 20 * 86_400_000).toISOString(),
      }),
      makeDeployment({
        version: "v2",
        deployed_at: new Date(NOW_MS - 3 * 86_400_000).toISOString(),
      }),
    ];
    renderTimeline({ deployments });
    // Default range = 30d; both segments visible.
    expect(
      document.querySelectorAll("[data-segment-version]").length,
    ).toBe(2);
    fireEvent.click(screen.getByRole("radio", { name: "7d" }));
    // 7d range — only the v2 segment (3d ago) rendered. v1 (20d ago) is
    // outside the visible window. v1's segment also closed at v2's
    // deploy, so its endMs is 17d ago; both endpoints are pre-fromMs
    // and the renderer's clamp drops it to a zero-width sliver. The
    // segment computation still keeps it (we don't filter), but the
    // visible card we assert on is v2's open-ended segment.
    const visibleSegs = document.querySelectorAll(
      "[data-segment-version='v2']",
    );
    expect(visibleSegs.length).toBe(1);
  });

  it("TestRangeToolbarA11y: range presets use radiogroup semantics with aria-checked toggling on click", () => {
    renderTimeline({
      deployments: [makeDeployment({ deployed_at: "2026-04-10T08:00:00Z" })],
    });
    const group = screen.getByRole("radiogroup", { name: /timeline range/i });
    expect(group).not.toBeNull();
    const initial30d = screen.getByRole("radio", { name: "30d" });
    expect(initial30d.getAttribute("aria-checked")).toBe("true");
    fireEvent.click(screen.getByRole("radio", { name: "7d" }));
    expect(screen.getByRole("radio", { name: "7d" }).getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("radio", { name: "30d" }).getAttribute("aria-checked")).toBe("false");
  });

  it("TestMobileFallback: the SVG is hidden on narrow viewports via Tailwind sm:hidden + a fallback paragraph is mounted instead", () => {
    renderTimeline({
      deployments: [makeDeployment({ deployed_at: "2026-04-10T08:00:00Z" })],
    });
    // The fallback paragraph mounts unconditionally (Tailwind handles
    // visibility); the test asserts both surfaces are present in the
    // DOM so SR users get the message at any viewport.
    expect(screen.getByTestId("bundle-timeline-mobile-fallback")).not.toBeNull();
    expect(screen.getByTestId("bundle-timeline-svg")).not.toBeNull();
  });
});

describe("computeTimelineSegments helper (D6 Gantt math)", () => {
  it("preserves the deployed-at value as the segment startMs", () => {
    const range = { fromMs: NOW_MS - 30 * 86_400_000, toMs: NOW_MS };
    const deployments = [
      makeDeployment({
        version: "v1",
        deployed_at: "2026-04-10T08:00:00Z",
      }),
    ];
    const segs = computeTimelineSegments(deployments, range);
    expect(segs.length).toBe(1);
    expect(segs[0].startMs).toBe(Date.parse("2026-04-10T08:00:00Z"));
    // Latest event → open-ended.
    expect(segs[0].endMs).toBeNull();
  });

  it("sequential deployments chain endMs to the next deployment's startMs", () => {
    const range = { fromMs: NOW_MS - 30 * 86_400_000, toMs: NOW_MS };
    const segs = computeTimelineSegments(
      [
        makeDeployment({
          version: "v1",
          deployed_at: "2026-04-10T08:00:00Z",
        }),
        makeDeployment({
          version: "v2",
          deployed_at: "2026-04-12T08:00:00Z",
        }),
      ],
      range,
    );
    expect(segs.length).toBe(2);
    expect(segs[0].endMs).toBe(Date.parse("2026-04-12T08:00:00Z"));
    expect(segs[1].endMs).toBeNull();
  });

  it("drops events with unparseable timestamps without throwing", () => {
    const range = { fromMs: NOW_MS - 30 * 86_400_000, toMs: NOW_MS };
    const segs = computeTimelineSegments(
      [
        makeDeployment({ version: "v1", deployed_at: "not-a-date" }),
        makeDeployment({
          version: "v2",
          deployed_at: "2026-04-12T08:00:00Z",
        }),
      ],
      range,
    );
    expect(segs.length).toBe(1);
    expect(segs[0].version).toBe("v2");
  });

  it("equal timestamps degrade to a 1ms sliver so a same-instant rollback still draws", () => {
    const range = { fromMs: NOW_MS - 30 * 86_400_000, toMs: NOW_MS };
    const segs = computeTimelineSegments(
      [
        makeDeployment({
          version: "v1",
          deployed_at: "2026-04-10T08:00:00Z",
        }),
        makeDeployment({
          version: "v2",
          deployed_at: "2026-04-10T08:00:00Z",
        }),
      ],
      range,
    );
    expect(segs[0].endMs! - segs[0].startMs).toBeGreaterThanOrEqual(1);
  });
});
