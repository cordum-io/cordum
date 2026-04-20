# Backwards-Compat Shim Audit — 2026-04-20

Durable record of the two `*_compat.go` files in cordum core, their
scope, and the caller audit that drove the deletion.

## `core/licensing/compat.go` (186 lines)

**What it does:** bridges a legacy license envelope format to the
current `Claims` shape. Defines:

- `legacyClaims` struct (top-level `features` + `limits` maps),
- `isLegacyClaims([]byte) bool` (detects the legacy JSON shape),
- `migrateLegacyClaims(legacyClaims) Claims` (projects legacy fields
  onto the current `Claims` / `Rights` / `Entitlements` record),
- `ensureRights`, `ensureEntitlements`, `cloneInt`, `normalizeName`
  (helpers used by the migration path and — importantly — by
  `license.go` and `enforce.go` for current-format feature-name
  canonicalization).

**Caller audit (grep from `core/`, `sdk/`, `cmd/`):**

| Symbol | license.go | license_test.go | enforce.go | compat.go (self) | Other |
|--------|------------|-----------------|------------|-------------------|-------|
| `legacyClaims`, `isLegacyClaims`, `migrateLegacyClaims` | lines 304-309 | lines 359, 376 | — | declarations | 0 |
| `ensureRights`, `ensureEntitlements`, `cloneInt` | — | — | — | used internally | 0 |
| `normalizeName` | lines 114, 141 | — | line 100 | used internally | 0 |

Zero external cordum consumers, zero cordum-enterprise / cordum-packs /
cordum-tools / cordum-marketing consumers (cross-repo grep — see step 4).

**Action:** delete `compat.go`. Before deleting:

1. Move `normalizeName` into `license.go` (it is a canonical-format
   helper, not a legacy-only one).
2. Replace `license.go:304-309` legacy-parsing branch with a hard
   rejection via `ErrUnsupportedLegacyLicenseFormat` (new typed error).
3. Rewrite the two `license_test.go` fixtures that built legacy
   envelopes — one becomes a rejection assertion, the other is
   deleted (it only asserted the migration path).

## `core/controlplane/gateway/auth_compat.go` (145 lines)

**What it does:** type-alias + var-rebind file that was generated when
the auth logic moved from `gateway/` to `gateway/auth/` subpackage.
Every symbol maps 1:1 onto the auth subpackage:

- 26 `type X = auth.X` aliases (AuthSource, AuthContext, AuthProvider,
  UserStore, RBACStore, etc.)
- 40 `const X = auth.Y` constant re-exports (AuthSource* sentinels,
  Perm* permission strings including the three `PermEvalsDatasets*`
  added by task-f34c528f, saml*/oidc*/scim* path constants, session
  cookie name)
- 25 `var X = auth.Y` function / error re-exports (auth helpers like
  `authFromContext` → `auth.FromContext`, `newBasicAuthProvider` →
  `auth.NewBasicAuthProvider`, validation helpers like
  `ValidatePassword`, and 8 error sentinels).

**Scope of the migration (verified with `go build` after the file is
deleted — compiler surfaces every unresolved name):**

- ~20 gateway-package files reference these aliases.
- ~100–500 individual call sites (estimate varies with grep method;
  the authoritative count is whatever `go build` reports as
  undefined-identifier errors once `auth_compat.go` is removed).
- `core/protocol/capsdk/` is explicitly OUT OF SCOPE (unreleased-upstream
  mirror, epic rail #2).

**Action:** delete `auth_compat.go`. Rewrite every call site in
`core/controlplane/gateway/*.go` to:

1. Add the `auth "github.com/cordum/cordum/core/controlplane/gateway/auth"`
   import if not already present.
2. Rewrite each aliased symbol to its canonical form. The mapping is
   the entirety of `auth_compat.go` — use it as the literal cheatsheet.
   Examples: `AuthSource` → `auth.AuthSource`, `PermJobsRead` →
   `auth.PermJobsRead`, `authFromContext(ctx)` → `auth.FromContext(ctx)`,
   `samlMetadataPath` → `auth.SAMLMetadataPath`, `ErrUserNotFound` →
   `auth.ErrUserNotFound`.

**Name-change note** (`authFromContext` / `authFromRequest` /
`basicAuthProvider` / etc. have different names on the canonical side):

| Compat alias | Canonical |
|--------------|-----------|
| `authContextKey` | `auth.ContextKey` |
| `authFromContext` | `auth.FromContext` |
| `authFromRequest` | `auth.FromRequest` |
| `newBasicAuthProvider` | `auth.NewBasicAuthProvider` |
| `basicAuthProvider` | `auth.ExtractBasicAuth` |
| `normalizeAPIKey` | `auth.NormalizeAPIKey` |
| `apiKeyFromWebSocket` | `auth.APIKeyFromWebSocket` |
| `bearerToken` | `auth.BearerToken` |
| `headerValue` | `auth.HeaderValue` |
| `normalizeRole` | `auth.NormalizeRole` |
| `parseAPIKeys` | `auth.ParseAPIKeys` |
| `sessionTokenFromCookie` | `auth.SessionTokenFromCookie` |
| `setSessionCookie` | `auth.SetSessionCookie` |
| `clearSessionCookie` | `auth.ClearSessionCookie` |
| `seedDefaultAdminUser` | `auth.SeedDefaultAdminUser` |
| `bcryptCostFromEnv` | `auth.BcryptCostFromEnv` |
| `samlMetadataPath`, `samlLoginPath`, `samlACSPath` | `auth.SAMLMetadataPath`, `auth.SAMLLoginPath`, `auth.SAMLACSPath` |
| `oidcLoginPath`, `oidcCallbackPath` | `auth.OIDCLoginPath`, `auth.OIDCCallbackPath` |
| `scimBasePath`, `scimUsersPath`, `scimGroupsPath` | `auth.SCIMBasePath`, `auth.SCIMUsersPath`, `auth.SCIMGroupsPath` |
| `sessionCookieName` | `auth.SessionCookieName` |

All other aliases keep the same identifier (`AuthContext` → `auth.AuthContext`, etc.).

## Behavior change

Legacy-format license envelopes are now **hard-rejected** at startup
with `ErrUnsupportedLegacyLicenseFormat` rather than silently migrated.
Operators running a license generated in the old format must regenerate
via `cordum-tools license-generator` before merging this PR. This is
intentional per `feedback_no_backwards_compat.md` and recorded in the
release note.

## Cross-repo belt-and-suspenders grep (step 4)

Expected: zero hits outside cordum. Any hit blocks this task (a
follow-up migrates the external caller first). Documented in step 4.

## Completion status (2026-04-20)

Both compat shims covered by this task are now deleted:

- `core/licensing/compat.go` was removed. The non-legacy helpers that
  still serve the current parser/enforcement path now live in
  `core/licensing/helpers.go` (`normalizeJSON`, `normalizeName`, and
  the narrow `isLegacyLicenseEnvelope` detector). `parseLicense`
  rejects the old top-level `features` + `limits` shape with
  `ErrUnsupportedLegacyLicenseFormat` and emits the
  `slog.Error("legacy license format rejected", ...)` breadcrumb before
  returning.
- `core/controlplane/gateway/auth_compat.go` was removed. Gateway
  callers now import `core/controlplane/gateway/auth` directly and use
  the canonical `auth.*` symbols. The alias table above is retained as
  the durable rewrite audit / migration cheatsheet.

Cross-repo grep remains the belt-and-suspenders check: no external repo
(`cordum-enterprise`, `cordum-packs`, `cordum-tools`,
`cordum-marketing`) should reach into these package-internal shim
symbols.
