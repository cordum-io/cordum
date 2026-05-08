import { defineConfig } from "orval";

/**
 * orval — OpenAPI codegen for the Cordum HTTP API.
 *
 * Spec: cordum/docs/api/openapi/cordum-api.yaml (relative to dashboard/).
 * Output: ./src/api/generated/ (one folder per tag, schemas alongside).
 * Client: react-query (uses @tanstack/react-query already in dependencies).
 * Mutator: ./src/api/client.ts apiClient — generated hooks call our existing
 *   http layer instead of raw fetch, so auth headers, tenant routing, 30s
 *   timeout, error normalization, and 401-redirect logic remain centralized.
 *
 * Mode `tags-split` produces:
 *   src/api/generated/<tag>/<tag>.ts          — query/mutation hooks
 *   src/api/generated/<tag>/<tag>.schemas.ts  — typed request/response bodies
 *
 * Regenerate: pnpm run generate-api. CI gates drift via check-api-codegen.
 * Do not hand-edit generated files.
 */
export default defineConfig({
  cordum: {
    input: {
      target: "../docs/api/openapi/cordum-api.yaml",
    },
    output: {
      mode: "tags-split",
      target: "./src/api/generated/cordum.ts",
      schemas: "./src/api/generated/model",
      client: "react-query",
      prettier: true,
      clean: true,
      override: {
        mutator: {
          path: "./src/api/client.ts",
          name: "apiClient",
        },
      },
    },
  },
});
