/**
 * Theme Token Guard Tests
 *
 * Validates that the CSS custom properties and Tailwind config maintain the
 * expected set of semantic design tokens from the Control Surface design language.
 */
import { describe, expect, it } from "vitest";
import * as fs from "node:fs";
import * as path from "node:path";

// ---------------------------------------------------------------------------
// Load source files
// ---------------------------------------------------------------------------

const STYLES_DIR = path.resolve(__dirname);
const CSS_PATH = path.join(STYLES_DIR, "index.css");
const TAILWIND_PATH = path.resolve(__dirname, "../../tailwind.config.cjs");

const cssContent = fs.readFileSync(CSS_PATH, "utf-8");
const tailwindContent = fs.readFileSync(TAILWIND_PATH, "utf-8");

// ---------------------------------------------------------------------------
// CSS Variable Extraction
// ---------------------------------------------------------------------------

/** Extract all CSS custom property names from a :root block */
function extractVars(css: string, selector: string): string[] {
  const regex = new RegExp(`${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{([^}]+)\\}`, "g");
  const vars: string[] = [];
  let match: RegExpExecArray | null;
  while ((match = regex.exec(css)) !== null) {
    const block = match[1];
    const propRegex = /--([\w-]+)\s*:/g;
    let propMatch: RegExpExecArray | null;
    while ((propMatch = propRegex.exec(block)) !== null) {
      vars.push(`--${propMatch[1]}`);
    }
  }
  return vars;
}

const lightVars = extractVars(cssContent, ":root");
const darkVars = extractVars(cssContent, ':root[data-theme="dark"]');

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("Theme Tokens: CSS Custom Properties", () => {
  const REQUIRED_TOKENS = [
    "--bg",
    "--surface",
    "--surface-2",
    "--text",
    "--muted",
    "--accent",
    "--accent-2",
    "--success",
    "--warning",
    "--danger",
    "--border",
    "--ring",
    "--shadow",
    "--surface-glass",
  ];

  describe("light theme (:root) defines all required tokens", () => {
    for (const token of REQUIRED_TOKENS) {
      it(`defines ${token}`, () => {
        expect(lightVars).toContain(token);
      });
    }
  });

  describe("dark theme (:root[data-theme=\"dark\"]) defines all required tokens", () => {
    for (const token of REQUIRED_TOKENS) {
      it(`defines ${token}`, () => {
        expect(darkVars).toContain(token);
      });
    }
  });

  it("light and dark themes define the same set of core tokens", () => {
    const coreLight = lightVars.filter((v) => REQUIRED_TOKENS.includes(v)).sort();
    const coreDark = darkVars.filter((v) => REQUIRED_TOKENS.includes(v)).sort();
    expect(coreLight).toEqual(coreDark);
  });
});

describe("Theme Tokens: Tailwind Config Mapping", () => {
  const EXPECTED_COLORS: Record<string, string> = {
    base: "var(--bg)",
    surface: "var(--surface)",
    surface2: "var(--surface-2)",
    ink: "var(--text)",
    muted: "var(--muted)",
    accent: "var(--accent)",
    accent2: "var(--accent-2)",
    success: "var(--success)",
    warning: "var(--warning)",
    danger: "var(--danger)",
    border: "var(--border)",
  };

  for (const [name, cssVar] of Object.entries(EXPECTED_COLORS)) {
    it(`maps Tailwind color "${name}" to ${cssVar}`, () => {
      // Check both the key and value exist in the config
      expect(tailwindContent).toContain(name);
      expect(tailwindContent).toContain(cssVar);
    });
  }

  const EXPECTED_FONTS: Record<string, string> = {
    sans: "IBM Plex Sans",
    display: "Space Grotesk",
    mono: "IBM Plex Mono",
  };

  for (const [key, family] of Object.entries(EXPECTED_FONTS)) {
    it(`maps Tailwind font "${key}" to ${family}`, () => {
      expect(tailwindContent).toContain(key);
      expect(tailwindContent).toContain(family);
    });
  }
});

describe("Theme Tokens: Dark Mode Configuration", () => {
  it("uses data-theme attribute for dark mode", () => {
    expect(tailwindContent).toContain('data-theme="dark"');
  });

  it("CSS has dark theme color-scheme set to dark", () => {
    // Find the dark theme block and check for color-scheme: dark
    const darkBlock = cssContent.match(/:root\[data-theme="dark"\]\s*\{([^}]+)\}/);
    expect(darkBlock).toBeTruthy();
    expect(darkBlock![1]).toContain("color-scheme: dark");
  });

  it("CSS has light theme color-scheme set to light", () => {
    const lightBlock = cssContent.match(/:root\s*\{([^}]+)\}/);
    expect(lightBlock).toBeTruthy();
    expect(lightBlock![1]).toContain("color-scheme: light");
  });
});

describe("Theme Tokens: Animation Definitions", () => {
  const REQUIRED_ANIMATIONS = [
    "rise-in",
    "slide-in-right",
    "fade-in",
    "scale-in",
    "pulse-scan",
  ];

  for (const anim of REQUIRED_ANIMATIONS) {
    it(`defines @keyframes ${anim}`, () => {
      expect(cssContent).toContain(`@keyframes ${anim}`);
    });
  }

  it("respects prefers-reduced-motion", () => {
    expect(cssContent).toContain("prefers-reduced-motion");
  });
});

describe("Theme Tokens: Surface Utility Classes", () => {
  const REQUIRED_CLASSES = [
    ".glass-panel",
    ".surface-card",
    ".attention-card",
    ".list-row",
  ];

  for (const cls of REQUIRED_CLASSES) {
    it(`defines ${cls} utility class`, () => {
      expect(cssContent).toContain(cls);
    });
  }

  it("glass-panel uses backdrop-filter blur", () => {
    const glassSection = cssContent.slice(
      cssContent.indexOf(".glass-panel"),
      cssContent.indexOf(".glass-panel") + 200,
    );
    expect(glassSection).toContain("backdrop-filter");
    expect(glassSection).toContain("blur");
  });

  it("surface-card uses var(--surface) background", () => {
    const surfaceSection = cssContent.slice(
      cssContent.indexOf(".surface-card"),
      cssContent.indexOf(".surface-card") + 200,
    );
    expect(surfaceSection).toContain("var(--surface)");
  });
});
