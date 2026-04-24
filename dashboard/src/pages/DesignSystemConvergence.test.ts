import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import jobDetailSource from "./JobDetailPage.tsx?raw";
import schemaDetailSource from "./SchemaDetailPage.tsx?raw";
import schemasPageSource from "./SchemasPage.tsx?raw";
import homePageSource from "./HomePage.tsx?raw";
import agentDetailSource from "./AgentDetailPage.tsx?raw";
import runDetailSource from "./RunDetailPage.tsx?raw";
import bundleDetailSource from "./govern/BundleDetailPage.tsx?raw";
import appShellSource from "../components/layout/AppShell.tsx?raw";
import settingsHubSource from "./SettingsHubPage.tsx?raw";
import jobsPageSource from "./JobsPage.tsx?raw";
import auditLogPageSource from "./AuditLogPage.tsx?raw";
import agentsPageSource from "./AgentsPage.tsx?raw";

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

  it("DoD-3 — RunDetailPage uses 12-column Bento Grid", () => {
    expect(runDetailSource).toMatch(/grid-cols-12/);
  });

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
});
