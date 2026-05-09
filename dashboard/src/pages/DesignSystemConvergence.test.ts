import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import jobDetailSource from "./JobDetailPage.tsx?raw";
import schemaDetailSource from "./SchemaDetailPage.tsx?raw";
import schemasPageSource from "./SchemasPage.tsx?raw";
import homePageSource from "./HomePage.tsx?raw";
import agentDetailSource from "./AgentDetailPage.tsx?raw";
import bundleDetailSource from "./govern/BundleDetailPage.tsx?raw";
import appShellSource from "../components/layout/AppShell.tsx?raw";
import settingsHubSource from "./SettingsHubPage.tsx?raw";
import agentsPageSource from "./AgentsPage.tsx?raw";
import packDetailSource from "./PackDetailPage.tsx?raw";
import evalsPageSource from "./EvalsPage.tsx?raw";
import evalDatasetDetailSource from "./EvalDatasetDetailPage.tsx?raw";
import evalRunDetailSource from "./EvalRunDetailPage.tsx?raw";
import runDetailSource from "./RunDetailPage.tsx?raw";
import packsPageSource from "./PacksPage.tsx?raw";
import delegationsPageSource from "./DelegationsPage.tsx?raw";
import approvalsPageSource from "./ApprovalsPage.tsx?raw";
import bundleDetailGovernSource from "./govern/BundleDetailPage.tsx?raw";
import outputRulesPageSource from "./govern/OutputRulesPage.tsx?raw";
import replayPageSource from "./govern/ReplayPage.tsx?raw";
import inputRulesPageSource from "./govern/InputRulesPage.tsx?raw";
import policyAnalyticsPageSource from "./govern/PolicyAnalyticsPage.tsx?raw";
import quarantinePageSource from "./govern/QuarantinePage.tsx?raw";
import approvalDetailPageSource from "./approvals/ApprovalDetailPage.tsx?raw";
import tenantDetailPageSource from "./govern/TenantDetailPage.tsx?raw";
import simulatorPageSource from "./govern/SimulatorPage.tsx?raw";
import bundlesPageSource from "./govern/BundlesPage.tsx?raw";
import tenantsPageSource from "./govern/TenantsPage.tsx?raw";
import velocityRulesPageSource from "./govern/VelocityRulesPage.tsx?raw";
import workflowsPageSource from "./WorkflowsPage.tsx?raw";
import settingsSSOPageSource from "./settings/SettingsSSOPage.tsx?raw";
import edgeSessionsPageSource from "./EdgeSessionsPage.tsx?raw";
import edgeSessionDetailPageSource from "./EdgeSessionDetailPage.tsx?raw";
import buttonSource from "../components/ui/Button.tsx?raw";
import cardSource from "../components/ui/Card.tsx?raw";

const hasInstrumentCard = (src: string) =>
  /instrument-card/.test(src) || /<InstrumentCard\b/.test(src);
const hasMotion = (src: string) =>
  /from "framer-motion"/.test(src) && /<motion\./.test(src);

const here = dirname(fileURLToPath(import.meta.url));
const indexCss = readFileSync(resolve(here, "../styles/index.css"), "utf8");

describe("design-system convergence regressions", () => {
  it("keeps the schema surfaces off raw form controls", () => {
    expect(schemaDetailSource).not.toMatch(/<input\b/);
    expect(schemaDetailSource).not.toMatch(/<select\b/);
    expect(schemaDetailSource).not.toMatch(/type=\"checkbox\"/);
    expect(schemasPageSource).not.toMatch(/<input\b/);
  });

  it("keeps job detail status styling on shared tokens instead of page-local CSS vars", () => {
    expect(jobDetailSource).not.toMatch(/var\(--color-/);
  });

  it("keeps approvals page status styling on shared tokens instead of page-local CSS vars", () => {
    expect(approvalsPageSource).not.toMatch(/var\(--color-/);
  });

  it("keeps govern bundle detail page styling on shared tokens instead of page-local CSS vars", () => {
    expect(bundleDetailGovernSource).not.toMatch(/var\(--color-/);
  });

  it("keeps govern output rules page styling on shared tokens instead of page-local CSS vars", () => {
    expect(outputRulesPageSource).not.toMatch(/var\(--color-/);
  });

  it("keeps govern replay page styling on shared tokens instead of page-local CSS vars", () => {
    expect(replayPageSource).not.toMatch(/var\(--color-/);
  });

  it("keeps govern input rules page styling on shared tokens instead of page-local CSS vars", () => {
    expect(inputRulesPageSource).not.toMatch(/var\(--color-/);
  });

  it("keeps govern policy analytics page styling on shared tokens instead of page-local CSS vars", () => {
    expect(policyAnalyticsPageSource).not.toMatch(/var\(--color-/);
  });

  it("keeps govern quarantine page styling on shared tokens instead of page-local CSS vars", () => {
    expect(quarantinePageSource).not.toMatch(/var\(--color-/);
  });

  // Raw-control convergence regressions — the v2.5 drift sweep DoD #2 requires
  // each newly converged page to use the canonical Input / Select / Textarea /
  // Checkbox primitives instead of raw native controls. The regex is anchored
  // on a word boundary so JSX tags like `<input ` or `<select\n` match while
  // the literal words "input" / "select" / "textarea" inside identifiers
  // (component names, prop names, comments) do NOT trigger.
  const RAW_CONTROL_RE = /<(input|select|textarea)\b/;

  it("v2.5 drift sweep — ReplayPage uses primitives, no raw native controls", () => {
    expect(replayPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep — InputRulesPage uses primitives, no raw native controls", () => {
    expect(inputRulesPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep — OutputRulesPage uses primitives, no raw native controls", () => {
    expect(outputRulesPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep — PolicyAnalyticsPage uses primitives, no raw native controls", () => {
    expect(policyAnalyticsPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep — QuarantinePage uses primitives, no raw native controls", () => {
    expect(quarantinePageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep reopen #2 — ApprovalDetailPage uses primitives, no raw native controls", () => {
    expect(approvalDetailPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep reopen #2 — TenantDetailPage uses primitives, no raw native controls", () => {
    expect(tenantDetailPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep reopen #2 — SimulatorPage uses primitives, no raw native controls", () => {
    expect(simulatorPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep reopen #2 — BundlesPage uses primitives, no raw native controls", () => {
    expect(bundlesPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep reopen #2 — TenantsPage uses primitives, no raw native controls", () => {
    expect(tenantsPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep reopen #2 — VelocityRulesPage uses primitives, no raw native controls", () => {
    expect(velocityRulesPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep reopen #2 — WorkflowsPage uses primitives, no raw native controls", () => {
    expect(workflowsPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep reopen #2 — SettingsSSOPage uses primitives, no raw native controls", () => {
    expect(settingsSSOPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep reopen #2 — EdgeSessionsPage uses primitives, no raw native controls", () => {
    expect(edgeSessionsPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  it("v2.5 drift sweep reopen #2 — EdgeSessionDetailPage uses primitives, no raw native controls", () => {
    expect(edgeSessionDetailPageSource).not.toMatch(RAW_CONTROL_RE);
  });

  // Comprehensive sweep gate — task-82593815 (drift sweep follow-up #2) closure.
  // Replaces the open-ended per-page enumeration with a forward-compatible
  // assertion: every page under src/pages/**/*.tsx must use the canonical
  // Input / Select / Textarea primitives instead of raw native controls.
  // Documented carve-outs stay raw and are listed below with a comment.
  //
  // When adding a new page that legitimately needs a native control, add the
  // file basename to the carve-out set AND document the rationale in
  // dashboard/docs/design-system-audit.md.
  const allPageSources = import.meta.glob<string>(
    ["./**/*.tsx", "!./**/*.test.tsx"],
    {
      query: "?raw",
      import: "default",
      eager: true,
    },
  );

  // Carve-outs (documented in mem-df8a90aa + dashboard/docs/design-system-audit.md):
  // - LoginPage: native HTML form required for browser autofill / password
  //   manager interop on the auth surface.
  // - RunDetailPage: workflow-run console exempted from primitive sweep
  //   (see "DoD-3 (12-col Bento Grid) — exemptions" register).
  const RAW_CONTROL_CARVE_OUTS = new Set([
    "./LoginPage.tsx",
    "./RunDetailPage.tsx",
  ]);

  it("v2.5 drift sweep — comprehensive sweep: all pages except documented carve-outs use primitives", () => {
    const offenders: Array<{ path: string; match: string }> = [];
    for (const [path, source] of Object.entries(allPageSources)) {
      // Skip test files — vitest globs catch *.test.tsx too.
      if (/\.test\.tsx$/.test(path)) continue;
      if (RAW_CONTROL_CARVE_OUTS.has(path)) continue;
      const match = RAW_CONTROL_RE.exec(source);
      if (match) offenders.push({ path, match: match[0] });
    }
    expect(
      offenders,
      `Pages with raw native controls (not in carve-out set): ${offenders
        .map((o) => `${o.path} (${o.match})`)
        .join(", ")}`,
    ).toEqual([]);
  });

  it("v2.5 drift sweep — carve-out pages still hold raw controls (regression detector for misclassified migration)", () => {
    // If LoginPage or RunDetailPage are accidentally migrated and the carve-out
    // is not removed from the set above, this test fails — forcing a coordinated
    // doc + test update rather than silent drift.
    for (const carveOut of RAW_CONTROL_CARVE_OUTS) {
      const source = allPageSources[carveOut];
      expect(source, `Carve-out ${carveOut} should exist`).toBeTruthy();
      expect(
        source,
        `Carve-out ${carveOut} should still contain a raw control (else remove from carve-out set + update audit doc)`,
      ).toMatch(RAW_CONTROL_RE);
    }
  });
});

describe("premium overhaul DoD gates", () => {
  it("DoD-2 — HomePage renders motion primitives (framer-motion adoption)", () => {
    expect(homePageSource).toMatch(/from "framer-motion"/);
    expect(homePageSource).toMatch(/<motion\./);
  });

  it("DoD-3 — AgentDetailPage uses 12-column Bento Grid", () => {
    expect(agentDetailSource).toMatch(/grid-cols-12/);
  });

  it("DoD-3 — JobDetailPage uses 12-column Bento Grid", () => {
    expect(jobDetailSource).toMatch(/grid-cols-12/);
  });

  // DoD-3 skipped for RunDetailPage — exempted as workflow-run console (see dashboard/docs/design-system-audit.md § 'DoD-3 (12-col Bento Grid) — exemptions', task-c154ff08, 2026-04-24). BundleDetailPage is NOT exempted.

  it("DoD-3 — BundleDetailPage uses 12-column Bento Grid", () => {
    expect(bundleDetailSource).toMatch(/grid-cols-12/);
  });

  it("DoD-2 — BundleDetailPage adopts framer-motion", () => {
    expect(bundleDetailSource).toMatch(/from "framer-motion"/);
    expect(bundleDetailSource).toMatch(/<motion\./);
  });

  it("DoD-1 — AppShell applies glass-sidebar and glass-header utilities", () => {
    expect(appShellSource).toMatch(/glass-sidebar/);
    expect(appShellSource).toMatch(/glass-header/);
  });

  it("DoD-1 — Settings hub uses instrument-card primitive", () => {
    expect(settingsHubSource).toMatch(/instrument-card/);
  });

  it("DoD-1 — design tokens shadow-soft, --radius 0.75rem, duration-soft exist for light and dark", () => {
    expect(indexCss).toMatch(/--shadow-soft:\s*0 4px 14px/);
    expect(indexCss).toMatch(/--radius:\s*0\.75rem/);
    expect(indexCss).toMatch(/--duration-soft:\s*250ms/);
    const darkBlock = indexCss.split(/\.dark\s*\{/)[1] ?? "";
    expect(darkBlock).toMatch(/--shadow-soft:/);
    expect(darkBlock).toMatch(/--duration-soft:/);
  });

  it("DoD-2 — core data tables stagger rows (Level 3 claim)", () => {
    // JobsPage migrated to primitives/DataTable in Phase 3 wk4 (task-2c3c8a04);
    // AuditLogPage migrated in commit fe057848 (filter URL state via nuqs +
    // DataTable swap). Per-row `motion.tr` is incompatible with DataTable's
    // virtualizer which mounts/unmounts rows on scroll, so per-row stagger
    // no longer applies on those surfaces — the DataTable primitive owns its
    // own row-render contract. AgentsPage remains on the hand-rolled-table
    // contract until it migrates too.
    const hasPerRowMotion = (src: string) =>
      /motion\.(tr|li|article)\b/.test(src) ||
      /<AnimatePresence[\s\S]*?<motion\./.test(src);
    expect(hasPerRowMotion(agentsPageSource)).toBe(true);
  });

  it("DoD-1 — PackDetailPage renders instrument-card primitive", () => {
    expect(hasInstrumentCard(packDetailSource)).toBe(true);
  });

  it("DoD-2 — PackDetailPage adopts framer-motion", () => {
    expect(hasMotion(packDetailSource)).toBe(true);
  });

  it("DoD-1 — EvalsPage renders instrument-card primitive", () => {
    expect(hasInstrumentCard(evalsPageSource)).toBe(true);
  });

  it("DoD-2 — EvalsPage adopts framer-motion", () => {
    expect(hasMotion(evalsPageSource)).toBe(true);
  });

  it("DoD-1 — EvalDatasetDetailPage renders instrument-card primitive", () => {
    expect(hasInstrumentCard(evalDatasetDetailSource)).toBe(true);
  });

  it("DoD-2 — EvalDatasetDetailPage adopts framer-motion", () => {
    expect(hasMotion(evalDatasetDetailSource)).toBe(true);
  });

  it("DoD-1 — EvalRunDetailPage renders instrument-card primitive", () => {
    expect(hasInstrumentCard(evalRunDetailSource)).toBe(true);
  });

  it("DoD-2 — EvalRunDetailPage adopts framer-motion", () => {
    expect(hasMotion(evalRunDetailSource)).toBe(true);
  });

  it("DoD-1 — PacksPage renders instrument-card primitive", () => {
    expect(hasInstrumentCard(packsPageSource)).toBe(true);
  });

  it("DoD-2 — PacksPage adopts framer-motion", () => {
    expect(hasMotion(packsPageSource)).toBe(true);
  });

  it("DoD-1 — DelegationsPage renders instrument-card primitive", () => {
    expect(hasInstrumentCard(delegationsPageSource)).toBe(true);
  });

  it("DoD-2 — DelegationsPage adopts framer-motion", () => {
    expect(hasMotion(delegationsPageSource)).toBe(true);
  });

  it("DoD-2 — Button consumes --duration-soft token (Soft UI 250ms)", () => {
    expect(buttonSource).toMatch(/duration-\[var\(--duration-soft\)\]/);
    expect(buttonSource).not.toMatch(/duration-300/);
  });

  it("DoD-2 — Card consumes --duration-soft token (Soft UI 250ms)", () => {
    expect(cardSource).toMatch(/duration-\[var\(--duration-soft\)\]/);
    expect(cardSource).not.toMatch(/duration-300/);
  });
});

describe("DoD-5 mobile responsive (task-671f49cd)", () => {
  it("RunDetailPage declares mobile-first pane layout via flex-col md:flex-row", () => {
    expect(runDetailSource).toMatch(/flex-col\s+md:flex-row/);
  });
  it("RunDetailPage hides non-active panes via hidden md:flex|block at <md", () => {
    expect(runDetailSource).toMatch(/hidden\s+md:(flex|block)/);
  });
  it("RunDetailPage enforces 44px tap target at <md (WCAG 2.5.5)", () => {
    expect(runDetailSource).toMatch(/min-w-\[44px\]\s+min-h-\[44px\]/);
  });
  it("RunDetailPage guards mobile pane transitions with useReducedMotion", () => {
    expect(runDetailSource).toMatch(/useReducedMotion/);
  });
});
