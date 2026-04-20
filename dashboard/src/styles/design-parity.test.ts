/**
 * Design Parity Guard Tests
 *
 * These tests scan parity-scoped component files for visual drift from the
 * Control Surface design language. They catch ad-hoc colors, non-tokenized
 * styles, and forbidden patterns before they reach production.
 *
 * Accepted deviations are documented in a baseline allowlist — the guard
 * ensures no NEW files introduce drift beyond what's already accepted.
 */
import { describe, expect, it } from "vitest";
import * as fs from "node:fs";
import * as path from "node:path";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const SRC_DIR = path.resolve(__dirname, "..");

/** Recursively collect .tsx files under a directory */
function collectTsx(dir: string): string[] {
  const results: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory() && entry.name !== "node_modules" && entry.name !== "__mocks__") {
      results.push(...collectTsx(full));
    } else if (entry.isFile() && entry.name.endsWith(".tsx") && !entry.name.endsWith(".test.tsx")) {
      results.push(full);
    }
  }
  return results;
}

/** Normalize path separators for cross-platform consistency */
function norm(p: string): string {
  return p.replace(/\\/g, "/");
}

/** Parity-scoped directories — components and pages that must follow the design system */
const PARITY_DIRS = [
  path.join(SRC_DIR, "components"),
  path.join(SRC_DIR, "pages"),
];

const parityFiles = PARITY_DIRS.flatMap((d) => (fs.existsSync(d) ? collectTsx(d) : []));

// ---------------------------------------------------------------------------
// Patterns
// ---------------------------------------------------------------------------

/**
 * Matches ad-hoc hex colors in JSX props (className, style, color, fill, stroke).
 * Forbids: inline hex like `#ffffff`, `bg-[#ff0000]` in component JSX.
 */
const RAW_HEX_IN_JSX = /(?:className|style|color|background|fill|stroke).*?#[0-9a-fA-F]{3,8}/;

/**
 * Matches raw `rgba(` or `rgb(` values in JSX className/style props.
 * These should come from CSS variables or Tailwind tokens instead.
 */
const RAW_RGBA_IN_JSX = /(?:className|style).*?rgba?\(\s*\d/;

/**
 * Matches `bg-white`, `bg-black` — non-tokenized Tailwind background classes.
 * `text-white` is allowed as it's a standard contrast pattern on colored backgrounds.
 * Opacity variants like `bg-white/70` are allowed (handled in CSS dark overrides).
 */
const RAW_BW_BG_CLASSES = /\b(?:bg-white|bg-black)(?!\/)\b/;

// ---------------------------------------------------------------------------
// Accepted Deviation Baselines
//
// These files contain known accepted deviations. The guard tests ensure no
// NEW files beyond this list introduce drift. When a file is cleaned up,
// remove it from the baseline.
// ---------------------------------------------------------------------------

/** Files with accepted ad-hoc hex colors (chart libraries, SVG visualizations) */
const HEX_BASELINE = new Set([
  "components/audit/AuditTimeline.tsx",
  "components/home/JobPipelineFunnel.tsx",
  "components/policy/PolicyAnalytics.tsx",
  "components/workflow/GanttTimeline.tsx",
  "components/workflow/RunVisualization.tsx",
  "components/workflows/SuccessSparkline.tsx",
  "pages/PoliciesOverviewPage.tsx",
]);

/** Files with accepted raw rgba() in className/style (inline alpha on semantic colors) */
const RGBA_BASELINE = new Set([
  "components/home/JobPipelineFunnel.tsx",
  "components/policy/PolicyReplay.tsx",
  "components/settings/ChangePasswordSection.tsx",
  "components/settings/UsersTab.tsx",
  "components/ui/Toast.tsx",
  "pages/LoginPage.tsx",
]);

/** Files with accepted bg-white/bg-black (toggle knobs, modals, dropdowns, cards) */
const BW_BG_BASELINE = new Set([
  "components/approvals/ApprovalCardV2.tsx",
  "components/approvals/ApprovalDetailPanel.tsx",
  "components/approvals/BulkActionBar.tsx",
  "components/audit/AuditExport.tsx",
  "components/audit/AuditFiltersBar.tsx",
  "components/audit/SavedFiltersDropdown.tsx",
  "components/CommandPalette.tsx",
  "components/ErrorBoundary.tsx",
  "components/jobs/ArtifactPanel.tsx",
  "components/jobs/JobFiltersBar.tsx",
  "components/jobs/UnifiedTimeline.tsx",
  "components/KeyboardShortcutsHelp.tsx",
  "components/layout/EnvironmentBanner.tsx",
  "components/policy/ImpactPreview.tsx",
  "components/policy/OutputRulesTab.tsx",
  "components/policy/PolicyAnalytics.tsx",
  "components/policy/PolicyLayout.tsx",
  "components/policy/PolicyTimeline.tsx",
  "components/policy/RuleCard.tsx",
  "components/policy/RulesTable.tsx",
  "components/policy/SecurityControls.tsx",
  "components/settings/LockInspector.tsx",
  "components/settings/MaintenanceModeSection.tsx",
  "components/ui/Button.tsx",
  "components/ui/ComboboxInput.tsx",
  "components/workflow/GanttTimeline.tsx",
  "components/workflow/nodes/BaseNode.tsx",
  "components/workflow/RunVisualization.tsx",
  "components/workflows/dag/RunOverlayNode.tsx",
  "components/workflows/RunNowModal.tsx",
  "pages/AuditLogPage.tsx",
  "pages/PoliciesOverviewPage.tsx",
  "pages/PoliciesRulesPage.tsx",
  "pages/settings/OutputSafetySettings.tsx",
  "pages/SettingsMcpPage.tsx",
  "pages/WorkflowDetailPage.tsx",
  "pages/WorkflowsPage.tsx",
]);

// ---------------------------------------------------------------------------
// Scan function
// ---------------------------------------------------------------------------

function scanFiles(
  pattern: RegExp,
  baseline: Set<string>,
): { file: string; violations: string[] }[] {
  const results: { file: string; violations: string[] }[] = [];
  for (const file of parityFiles) {
    const rel = norm(path.relative(SRC_DIR, file));
    if (baseline.has(rel)) continue;

    const content = fs.readFileSync(file, "utf-8");
    const lines = content.split("\n");
    const violations: string[] = [];
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      if (line.trimStart().startsWith("//") || line.trimStart().startsWith("*")) continue;
      if (line.includes("var(--")) continue;
      if (pattern.test(line)) {
        violations.push(`L${i + 1}: ${line.trim()}`);
      }
    }
    if (violations.length > 0) {
      results.push({ file: rel, violations });
    }
  }
  return results;
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("Design Parity Guard", () => {
  it("finds parity-scoped .tsx files to scan", () => {
    expect(parityFiles.length).toBeGreaterThan(0);
  });

  it("no NEW files introduce ad-hoc hex colors in JSX props", () => {
    const drift = scanFiles(RAW_HEX_IN_JSX, HEX_BASELINE);
    const msg = drift
      .map((d) => `${d.file}:\n${d.violations.join("\n")}`)
      .join("\n\n");
    expect(drift, `New ad-hoc hex color drift detected:\n${msg}`).toEqual([]);
  });

  it("no NEW files introduce raw rgba() in JSX props", () => {
    const drift = scanFiles(RAW_RGBA_IN_JSX, RGBA_BASELINE);
    const msg = drift
      .map((d) => `${d.file}:\n${d.violations.join("\n")}`)
      .join("\n\n");
    expect(drift, `New raw rgba() drift detected:\n${msg}`).toEqual([]);
  });

  it("no NEW files introduce non-tokenized bg-white/bg-black", () => {
    const drift = scanFiles(RAW_BW_BG_CLASSES, BW_BG_BASELINE);
    const msg = drift
      .map((d) => `${d.file}:\n${d.violations.join("\n")}`)
      .join("\n\n");
    expect(drift, `New bg-white/bg-black drift detected:\n${msg}`).toEqual([]);
  });

  it("baseline files still exist (no stale allowlist entries)", () => {
    const allBaseline = new Set([...HEX_BASELINE, ...RGBA_BASELINE, ...BW_BG_BASELINE]);
    const existingRels = new Set(parityFiles.map((f) => norm(path.relative(SRC_DIR, f))));
    const stale = [...allBaseline].filter((b) => !existingRels.has(b));
    expect(
      stale,
      `Stale baseline entries (files deleted but not removed from allowlist):\n${stale.join("\n")}`,
    ).toEqual([]);
  });
});
