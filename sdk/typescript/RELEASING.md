# Releasing `@cordum/sdk`

The worker session does **not** create tags or publish packages. A human release operator performs the final tag push after merge.

## One-time npm Trusted Publisher setup

On npmjs.com, configure a Trusted Publisher for `@cordum/sdk` with:

- package: `@cordum/sdk`
- provider: GitHub Actions
- owner: `cordum-io`
- repository: `cordum`
- workflow filename: `sdk-typescript.yml`
- environment: optional (`npm` is a good default)

Keep the workflow filename exact — npm matches it case-sensitively.

> npm Trusted Publishing currently requires a GitHub-hosted runner and a modern npm CLI. The publish workflow uses Node 24 so `npm publish` can authenticate with OIDC and emit provenance without storing an `NPM_TOKEN`.

## 1. Update release metadata

1. Bump `package.json` `version`
2. Update `CHANGELOG.md`
3. Commit the release prep on `main`

## 2. Run the local verification gate

From `sdk/typescript/`:

```bash
npm ci
npm run typecheck
npm run lint
npm test
npm run build
npm run check:no-node-builtins
npm run size
npm run test:browser
npm pack --dry-run
```

All seven commands must be green before tagging.

## 3. Push the release tag

```bash
git tag sdk-ts-v0.1.0
git push origin sdk-ts-v0.1.0
```

The GitHub Actions workflow `.github/workflows/sdk-typescript.yml` will:

1. validate the SDK on Node 18 / 20 / 22 across Ubuntu and Windows
2. run the browser smoke harness in Chromium, Firefox, and WebKit
3. publish `@cordum/sdk` with npm Trusted Publishing (`npm publish --provenance --access public`)

## 4. Verify the release

After the publish job completes:

```bash
npm view @cordum/sdk version
npm view @cordum/sdk dist-tags --json
```

Confirm the new version appears, then verify the package page:

- https://www.npmjs.com/package/@cordum/sdk

## Notes

- Do **not** create or store `NPM_TOKEN` for this workflow.
- Trusted Publishing already generates npm provenance for public packages from public GitHub repositories.
- Tag creation stays human-only.
