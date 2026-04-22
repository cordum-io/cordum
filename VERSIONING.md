# Versioning & Release

The cordum repo publishes three independent artifacts. Each has its own version
stream, its own tag format, and its own release workflow. They are intentionally
decoupled: a core security fix does not force an SDK bump, and an SDK
ergonomics tweak does not force a core bump.

## Artifacts

| Artifact | Tag format | Version source of truth | Ships as | Workflow |
|----------|------------|-------------------------|----------|----------|
| Core platform (7 services + dashboard) | `v<major>.<minor>.<patch>` | Git tag + image tag | ghcr.io images | `.github/workflows/docker.yml` |
| Python SDK | `sdk-python-v<major>.<minor>.<patch>` | `sdk/python/src/cordum_sdk/__init__.py` `__version__` | PyPI `cordum-sdk` | `.github/workflows/sdk-python.yml` |
| TypeScript SDK | `sdk-ts-v<major>.<minor>.<patch>` | `sdk/typescript/package.json` `version` | npm `@cordum/sdk` | `.github/workflows/sdk-typescript.yml` |

Tag prefixes are load-bearing: each publish workflow only fires on its own
prefix. Pushing an unprefixed `v*` tag will build core images, not the SDKs.

## Semver

Strict [SemVer](https://semver.org/). Pre-1.0 (`0.y.z`) allows breaking changes
on minor bumps — but document them in the artifact's `CHANGELOG.md` and bump
`y`.

| Change | MAJOR | MINOR | PATCH |
|--------|-------|-------|-------|
| Remove endpoint / class / method / field | yes | | |
| Narrow parameter type, change error-code meaning | yes | | |
| Change wire format (cap protocol) | yes | | |
| Add endpoint / method / optional field | | yes | |
| Loosen parameter constraint | | yes | |
| Bug fix with no contract change | | | yes |
| Dependency bump (no user-visible API change) | | | yes |

## Gateway ↔ SDK compatibility

The Python and TypeScript SDKs are generated from
`docs/api/openapi/cordum-api.yaml`. Their versions are independent of core's
version — do not try to keep them in lockstep.

Each SDK README carries a compatibility matrix:

```
cordum-sdk 0.1.x  <->  cordum core >= 0.9.5  (spec >= 1.0)
```

When the spec adds a minor surface, ship `cordum-sdk 0.2.0`. When core removes
a field (breaking), that is when the SDK's compat minimum moves forward.

## Release procedure

Same shape for all three artifacts. Substitute the tag prefix.

```bash
# 1. On a branch: bump version + changelog
#    Python: edit sdk/python/src/cordum_sdk/__init__.py -> __version__ = "0.1.1"
#            edit sdk/python/CHANGELOG.md               -> ## 0.1.1 - YYYY-MM-DD
#    TS:     edit sdk/typescript/package.json           -> "version": "0.1.1"
#            edit sdk/typescript/CHANGELOG.md
#    Core:   edit CHANGELOG.md
#    commit, PR, merge to main

# 2. From main, tag and push:
git checkout main && git pull --ff-only
git tag sdk-python-v0.1.1                  # or sdk-ts-v0.1.1, or v0.9.10
git push origin sdk-python-v0.1.1

# 3. Workflow takes over:
#    SDKs: test matrix -> build -> OIDC Trusted Publisher upload
#    Core: matrix build -> ghcr.io push

# 4. Verify (SDK):
pip index versions cordum-sdk              # Python
npm view @cordum/sdk versions --json       # TypeScript
```

## Tag ↔ version consistency gate

Both SDK workflows verify that the tag version matches the in-tree version
file before building. Tag drift (pushing `sdk-python-v0.2.0` while
`__version__` still says `"0.1.0"`) fails the build and does not ship.

If you see `::error::tag=X but <file>=Y` in CI, the tag and file disagree. Fix
the file on a follow-up commit, delete and repush the tag. Never ship with a
mismatched internal version — PyPI/npm burn the external version forever.

## Trusted Publisher OIDC

Neither SDK uses a long-lived `PYPI_API_TOKEN` or `NPM_TOKEN`. Publication
flows through short-lived OIDC tokens exchanged at tag-push time:

- **PyPI**: configured on https://pypi.org/manage/account/publishing/
- **npm**: configured on the `@cordum/sdk` package settings on npmjs.com

Both publishers are pinned to owner `cordum-io`, repo `cordum`, the workflow
filename, and an environment (`pypi` / `npm`). Forks cannot publish — the
workflow's `github.repository == 'cordum-io/cordum'` guard rejects them before
OIDC is attempted.

See `sdk/python/RELEASING.md` and `sdk/typescript/RELEASING.md` for the
operator checklist per artifact.

## Cross-repo note

`cap` (Cordum Agent Protocol) lives in its own repo with its own version
stream (`v2.x.x`, Go module path `github.com/cordum-io/cap/v2`). When the wire
protocol changes majors, every `cap` SDK bumps in lockstep. This does not
trigger a `cordum-sdk` or `@cordum/sdk` bump — those consume the HTTP REST
control plane, not the bus protocol.

`cordum-packs` uses mixed prefixes (`v0.6.x` for the overall pack release,
`adapters-v0.2.x` for the agent-adapters sub-package). See its own
`VERSIONING.md` when it gets one.

## Historical tag anomalies

For reference, this non-conforming tag exists and should not be replicated:

- `V0.9.9.1` — wrong case, four version parts. Points at commit `9595637e`
  (fix #191). Left in place for release traceability. Do **not** retroactively
  add a `v0.9.9` sibling tag: `.github/workflows/docker.yml` triggers on any
  `v*` tag and would rebuild images at an old commit, potentially rolling the
  `:latest` tag backwards in ghcr and Docker Hub.
- Going forward, never ship a four-part version tag. If you need a sub-patch
  distinction (e.g. a cherry-pick into a release), use SemVer metadata —
  `v1.2.3+hotfix1` — and ensure the publish workflow's regex matches.
