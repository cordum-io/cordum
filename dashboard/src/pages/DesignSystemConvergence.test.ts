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
import jobsPageSource from "./JobsPage.tsx?raw";
import auditLogPageSource from "./AuditLogPage.tsx?raw";
import agentsPageSource from "./AgentsPage.tsx?raw";
import packDetailSource from "./PackDetailPage.tsx?raw";
import mcpPageSource from "./MCPPage.tsx?raw";
import evalsPageSource from "./EvalsPage.tsx?raw";
import evalDatasetDetailSource from "./EvalDatasetDetailPage.tsx?raw";
import evalRunDetailSource from "./EvalRunDetailPage.tsx?raw";
import runDetailSource from "./RunDetailPage.tsx?raw";
import packsPageSource from "./PacksPage.tsx?raw";
import delegationsPageSource from "./DelegationsPage.tsx?raw";
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
    const hasPerRowMotion = (src: string) =>
      /motion\.(tr|li|article)\b/.test(src) ||
      /<AnimatePresence[\s\S]*?<motion\./.test(src);
    expect(hasPerRowMotion(jobsPageSource)).toBe(true);
    expect(hasPerRowMotion(auditLogPageSource)).toBe(true);
    expect(hasPerRowMotion(agentsPageSource)).toBe(true);
  });

  it("DoD-1 — PackDetailPage renders instrument-card primitive", () => {
    expect(hasInstrumentCard(packDetailSource)).toBe(true);
  });

  it("DoD-2 — PackDetailPage adopts framer-motion", () => {
    expect(hasMotion(packDetailSource)).toBe(true);
  });

  it("DoD-1 — MCPPage renders instrument-card primitive", () => {
    expect(hasInstrumentCard(mcpPageSource)).toBe(true);
  });

  it("DoD-2 — MCPPage adopts framer-motion", () => {
    expect(hasMotion(mcpPageSource)).toBe(true);
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
