// Strict a11y-only gate config used by `pnpm run lint:a11y`. Errors block CI;
// the broader `lint` script keeps a curated severity mix (warn for low-impact
// rules, error for ARIA correctness) so existing surfaces don't drown the
// pipeline. This narrower file gates the subset that MUST stay zero on a
// merged PR.
//
// Mirrors eslint.config.mjs's jsx-a11y plugin wiring so we run against the
// same source files; the only deltas are severities (everything to error)
// and the rule allow-list (we only escalate the rules that are non-flaky in
// jsdom + RTL-rendered output).

import jsxA11y from "eslint-plugin-jsx-a11y";
import tsParser from "@typescript-eslint/parser";

export default [
  {
    ignores: ["node_modules/", "dist/", "*.config.*", "src/api/generated/**"],
  },
  {
    files: ["src/**/*.{ts,tsx}"],
    plugins: {
      "jsx-a11y": jsxA11y,
    },
    // Disable unused-disable-directive warnings: this config doesn't load
    // every rule in eslint.config.mjs (e.g. no-console), so legitimate
    // disable comments elsewhere look "unused" to the narrower runner.
    linterOptions: {
      reportUnusedDisableDirectives: "off",
    },
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaFeatures: { jsx: true },
        ecmaVersion: "latest",
        sourceType: "module",
      },
    },
    rules: {
      "jsx-a11y/alt-text": "error",
      "jsx-a11y/aria-props": "error",
      "jsx-a11y/aria-role": "error",
      "jsx-a11y/aria-unsupported-elements": "error",
      "jsx-a11y/role-has-required-aria-props": "error",
      "jsx-a11y/role-supports-aria-props": "error",
      "jsx-a11y/anchor-has-content": "error",
      "jsx-a11y/heading-has-content": "error",
      "jsx-a11y/iframe-has-title": "error",
    },
  },
];
