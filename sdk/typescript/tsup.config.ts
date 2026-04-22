import { defineConfig } from "tsup";

export default defineConfig({
  entry: ["src/index.ts"],
  format: ["esm", "cjs"],
  dts: true,
  sourcemap: true,
  splitting: false,
  clean: true,
  target: "es2022",
  outExtension({ format }) {
    return {
      js: format === "esm" ? ".mjs" : ".js",
    };
  },
  onSuccess: "node -e \"const fs=require('node:fs');fs.writeFileSync('dist/package.json',JSON.stringify({type:'commonjs'},null,2)+'\\n')\"",
});
