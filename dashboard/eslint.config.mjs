import tsParser from "@typescript-eslint/parser";
import jsxA11y from "eslint-plugin-jsx-a11y";

export default [
  {
    ignores: ["node_modules/", "dist/", "*.config.*", "src/api/generated/**"],
  },
  {
    files: ["src/**/*.{ts,tsx}"],
    plugins: {
      "jsx-a11y": jsxA11y,
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
      // Critical: every interactive element needs an accessible name.
      "jsx-a11y/alt-text": "warn",
      "jsx-a11y/aria-props": "error",
      "jsx-a11y/aria-role": "error",
      "jsx-a11y/aria-unsupported-elements": "error",
      "jsx-a11y/role-has-required-aria-props": "error",
      "jsx-a11y/role-supports-aria-props": "error",
      "jsx-a11y/tabindex-no-positive": "warn",

      // Relaxed: too many warnings for first pass. Enable incrementally.
      "jsx-a11y/anchor-is-valid": "off",
      "jsx-a11y/click-events-have-key-events": "off",
      "jsx-a11y/no-static-element-interactions": "off",
      "jsx-a11y/no-noninteractive-element-interactions": "off",
    },
  },
  // ---------------------------------------------------------------------------
  // No-console rule — production paths only.
  //
  // task-1acf9c07 Pass C: every console.log/warn/error/debug/info in
  // dashboard/src/ should go through the structured logger at
  // src/lib/logger.ts (component + message + fields → JSON in prod,
  // human-readable in dev). The rule fires on production paths and is
  // explicitly disabled for test infrastructure (test-utils, *.test.*,
  // *.stories.*) where direct console use is fine.
  // ---------------------------------------------------------------------------
  {
    files: ["src/**/*.{ts,tsx}"],
    ignores: [
      "src/test-utils/**",
      "src/**/*.test.{ts,tsx}",
      "src/**/__tests__/**",
      "src/**/*.stories.{ts,tsx}",
    ],
    rules: {
      "no-console": "error",
    },
  },
];
