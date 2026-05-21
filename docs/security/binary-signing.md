# Cordum Edge — binary signing and integrity trust boundary

This document is the threat model and operator reference for EDGE-151
release-time integrity controls on the desktop binaries `cordum-hook`,
`cordum-agentd`, and `cordum-claude`. It is companion reading to
`docs/edge.md`, `docs/edge-claude-code.md`, and the cordum-hook /
cordum-agentd code paths under `cmd/`.

## 1. Overview — what this closes

`docs/edge/cordumctl-edge-claude.md` previously stated under "Trust
boundary":

> No binary integrity guarantee: the wrapper trusts the `claude`,
> `cordum-hook`, and `cordum-agentd` binaries it resolves. Signing and
> [verification was not yet wired up].

That gap is what EDGE-151 closes. With this control in place a local
attacker (a user-mode process on a developer or operator workstation, or
an unmanaged endpoint) cannot silently substitute a tampered
`cordum-hook` or `cordum-agentd` binary for the production-signed one
without being detected by the pre-activation gate in
`tools/scripts/install.{sh,ps1}`, or — when the wrapper or admin tooling
is invoked — by the `tools/sign` verifier package which re-validates the
manifest signature and per-binary SHA-256 before any execution path
trusts the on-disk binary.

The control is **release-time** integrity. It does not turn `cordum-hook`
itself into an enclave; the hook's own fail-closed policy enforcement is
unchanged by EDGE-151. What changes is that a process that exec()s
`cordum-hook` (the wrapper, `cordumctl edge claude`, `run-claude-edge.ps1`)
now has a reliable answer to "is this binary the one we signed for
release?" — answered before any code in the binary runs.

## 2. Trust boundary

### Prevents

| # | Threat | Mechanism |
|---|--------|-----------|
| a | **In-transit tampering** of a downloaded release artefact. | Detached GPG signature over `SHA256SUMS` manifest; per-binary SHA-256 inside the manifest. Any byte-level change in transit fails one or the other. |
| b | **Replacement by a non-root local process** of `cordum-hook` / `cordum-agentd` between install and execution. | Pre-activation gate writes binaries via atomic same-fs `mv` then recomputes the SHA-256 *after* the move, defeating a sig-then-swap race. The `tools/sign` verifier additionally re-checks at execution time when consumed by `cordumctl` / wrapper tooling. |
| c | **Accidental corruption** (disk error, partial download, package-manager mid-update). | Same SHA-256 check; failure is loud (`BINARY-VERIFY-FAIL: hash mismatch <name>`), exit non-zero, no activation. |

### Does NOT prevent

| # | Out-of-scope threat | Why |
|---|---------------------|-----|
| d | A **root / local-administrator** process that substitutes the binary AND swaps the bundled `tools/keys/cordum-release.pub.asc` file AND adjusts `CORDUM_PROD_FINGERPRINT_PIN` in `install.sh`'s header **simultaneously** before any subsequent install or wrapper run. | Defeated only by OS-level integrity (Secure Boot, code-integrity policies, MDM) or by the `-ldflags`-baked fingerprint in already-installed binaries — see (4) below. EDGE-151 reduces the attack surface but does not pretend to defeat full-root substitution. |
| e | **GitHub Actions secret compromise** (`GPG_RELEASE_KEY_PRIVATE` exfiltration). | The attacker can sign arbitrary manifests with the legitimate fingerprint. Mitigation is operational (secret scanning, rotation cadence in §5) not cryptographic. |
| f | **Apple Developer ID certificate leak**, **Authenticode `.pfx` leak**. | Same shape as (e) for Tier 2. Tier 1 stays an independent gate even when Tier 2 keys are leaked, because Tier 1 is gated by a separate GPG key. |
| g | **Downgrade attack**: serving an older, validly-signed binary that has a known CVE. | **ADDRESSED** by EDGE-151-DOWNGRADE (task-3166dda6). Install scripts enforce a persisted monotonic version-floor: a release manifest embedding `# version: vN.N.N` below the floor is refused with `BINARY-VERIFY-FAIL: downgrade attempt`. Legitimate rollbacks during incident response require an explicit `--rollback-operator-override` flag + `--rollback-reason <text>` and emit a `binary-floor-rollback` audit event. CI release-tag publishing additionally refuses any tag whose semver is not strictly greater than the most-recent prior tag (`version-monotonicity` job in `.github/workflows/release.yml`). Floor file tampering — an attacker with write access to `$HOME/.cordum/binary-version-floor.json` can clear the gate — remains residual; the floor file inherits the same trust principal as the installed binaries and is defense-in-depth on top of signature verification, not a replacement. See §8A for the audit-event schema. |
| h | **Build-environment supply-chain compromise** (malicious dependency, compromised runner image). | Out of scope; mitigated by GitHub's runner attestation, Renovate/Dependabot review, and the no-private-keys-leaked CI gate in `binaries-pr-validation.yml`. |

## 3. Two-tier scheme and per-platform trust marks

EDGE-151 ships two layered integrity tiers; both run on every release
when secrets allow, and Tier 1 alone is sufficient to satisfy DoD #1.

### Tier 1 — GPG-signed SHA256SUMS manifest (always-on)

* Producer: `.github/workflows/release.yml` job `sign-manifest` runs on
  every `v*` tag. It builds a deterministic `SHA256SUMS` manifest via the
  Go `tools/sign/cmd/manifest-cli` helper (sorted alphabetically by
  forward-slash relative path, lowercase hex), then GPG detach-signs it
  with `--pinentry-mode loopback` using `GPG_RELEASE_KEY_PRIVATE`.
* Output: `SHA256SUMS` + `SHA256SUMS.asc` published alongside the
  cross-compiled binaries via `gh release create`.
* Consumer: `tools/scripts/install.{sh,ps1}` and the pure-Go
  `tools/sign.Verifier`. Both reject `BINARY-VERIFY-FAIL: unsigned
  manifest` when `SHA256SUMS.asc` is missing, and `BINARY-VERIFY-FAIL:
  gpg signature invalid` when the signature does not verify against the
  trusted pubkey at `tools/keys/cordum-release.pub.asc`.
* Failure mode on forks / missing secret: workflow logs a `::warning::`
  banner and emits the manifest **without** a `.asc`; install path
  refuses such artefacts in production mode and accepts them only with
  `--dev-allow-unsigned` against a TEST-ONLY key.

### Tier 2 — OS-native code signing (canonical repo + secrets only)

* macOS (`darwin/amd64`, `darwin/arm64`): `codesign --options runtime
  --timestamp --deep --strict` with Apple Developer ID, followed by
  `xcrun notarytool submit --wait`. The notary record is checked online
  by Gatekeeper at run time; for bare binaries (non-bundle) `stapler`
  cannot embed the notarisation locally, so the verification is online.
* Windows (`windows/amd64`): `signtool sign /f cert.pfx /tr
  http://timestamp.digicert.com /td sha256 /fd sha256`. SmartScreen and
  Mark-of-the-Web honour the Authenticode signature; the Windows
  install.ps1 path also runs `Get-AuthenticodeSignature` as a
  defence-in-depth check.
* Linux: no equivalent OS-native trust mark; Tier 1 is the only layer
  and is sufficient. Users who run binaries with elevated privileges
  should additionally rely on package-manager integrity (apt, rpm) when
  distributing via that channel.
* Conditional gate: `if: github.repository == 'cordum-io/cordum' &&
  secrets.APPLE_DEVELOPER_ID != ''` (mirror for Windows). Tier 2 steps
  are `continue-on-error: true` so missing secrets in any fork result in
  a `success: skipped` job, not a workflow failure.

## 4. Pubkey pinning via `-ldflags`

`go build -ldflags '-X
github.com/cordum/cordum/tools/sign.PinnedReleaseFingerprint=<hex>'`
bakes the production fingerprint into the linker's `.rodata` for every
shipped binary. The same value is hardcoded into `install.sh`'s
`CORDUM_PROD_FINGERPRINT_PIN` and into the matching GitHub Actions
`RELEASE_FINGERPRINT` secret.

Why this matters: substituting `tools/keys/cordum-release.pub.asc` alone
is insufficient to bypass the gate. The pubkey file is data; the
fingerprint is schema. The `tools/sign.Verifier` cross-checks the
signer's fingerprint against the compiled-in `PinnedReleaseFingerprint`
in addition to membership in the trust set, so a single-file swap leaves
the verifier with a fingerprint mismatch (`ErrFingerprintMismatch`).
Defeating this requires either rebuilding the binary from source — which
flushes the attacker's pin into a value Cordum-the-organisation would
not sign for — or compromising the build environment (out-of-scope, (h)
above).

`PinnedReleaseFingerprint` is left empty in dev builds and in
`make release-local`; both paths use the TEST-ONLY fingerprint from
`tools/test-keys/TEST-ONLY-release.pub.asc` instead, and install.sh
refuses to honour TEST-ONLY material in production mode.

## 5. Key rotation

The release key has no expiration on its primary signing UID; rotation
is driven by calendar (annual) and event (suspected compromise, primary
custodian change).

Procedure:

1. **Generate the new keypair offline** on a Yubikey-backed
   workstation. Sign-only Ed25519 or RSA-3072. No expiration.
2. **Dual-sign overlap window** (two weeks). The release pipeline
   accepts both the old and new key by listing both fingerprints in the
   `trustedFingerprints` argument to `NewVerifier`. New artefacts are
   signed only by the new key during overlap; older versions remain
   verifiable.
3. **Cut over**: update `CORDUM_PROD_FINGERPRINT_PIN` in `install.sh`,
   update `RELEASE_FINGERPRINT` secret, re-commit
   `tools/keys/cordum-release.pub.asc` with the new public material,
   bump and tag a no-functional-change release whose binaries are built
   with the new pin via `-ldflags`. The old binaries continue to
   verify against the still-trusted old fingerprint.
4. **Retire** the old key by removing its fingerprint from the trust
   set in a follow-up release, and revoke (§6).

## 6. Revocation

OpenPGP CRLs are not consulted by the gate (it would couple availability
to keyserver infrastructure). Revocation is therefore **rotate-and-
republish**:

* Generate a new release key, publish a security advisory naming the
  withdrawn fingerprint, and follow §5 procedure compressed into a
  single emergency tag.
* The advisory is published under the same release channel
  (`cordum-io/cordum` Security tab) and via the email distribution list
  documented in `SECURITY.md`.

Operators verifying older artefacts after a revocation should consult
the advisory to decide whether to trust those signatures — the gate will
still treat them as cryptographically valid against the old key.

## 7. Fork and developer / dev mode

`--dev-allow-unsigned` is the only path that accepts a TEST-ONLY key.
Three guardrails:

* The pubkey path is hardcoded to
  `tools/test-keys/TEST-ONLY-release.pub.asc`; an attacker cannot
  redirect to an arbitrary file.
* The imported pubkey's UID must contain the literal string
  `TEST-ONLY`, defeating a "rename a production key into
  tools/test-keys/" bypass.
* The imported fingerprint is asserted **not equal** to
  `CORDUM_PROD_FINGERPRINT_PIN` (when set), so a cross-signed manifest
  cannot ride the dev path.

Forked CI runners exercise the gate via
`.github/workflows/binaries-pr-validation.yml` on `ubuntu-latest` only.
The synthetic-tampered and unsigned-manifest scenarios run against a
`make release-local` artefact signed by the committed TEST-ONLY key, so
forks without any production secret still validate the install path.

## 8. Audit event schema

The install path emits one structured line per outcome. The current
schema is loose (text lines from install.sh / install.ps1); see
`tools/scripts/install.sh` source for the exact strings.

When Cordum's audit pipeline is wired into the install path in a future
sibling task (currently tracked as `EDGE-151-AUDIT`), each outcome MUST
emit a `binary-verify` audit event with the following fields. **No
secrets, no full paths.**

| Field | Type | Notes |
|-------|------|-------|
| `event` | string | `binary-verify-ok` or `binary-verify-fail` |
| `hash` | string | Lowercase SHA-256 hex of the binary in question. |
| `path` | string | Basename only — never the absolute path. |
| `sig_scheme` | string | `gpg`, `codesign`, `authenticode`, or `dev` (TEST-ONLY). |
| `fingerprint` | string | Signer fingerprint (40 hex). Empty for `dev` when pinning is bypassed. |
| `reason` | string | On fail, one of the `BINARY-VERIFY-FAIL` reason strings emitted by install.{sh,ps1}: `hash mismatch <name>`, `unsigned manifest`, `gpg signature invalid`, `codesign verify failed`, `release pubkey fingerprint <got> does not match pinned <want>`, `manifest path traversal`, `post-activation hash mismatch`. Empty on success. |
| `exit_code` | integer | 0 for `-ok`, non-zero for `-fail`. |

These fields are stable; downstream SIEM mappings should pin to them.

### Dashboard surface (EDGE-151-DASHBOARD)

The Cordum admin dashboard's **Edge → Binary integrity** panel renders
recent `binary-verify-{ok,fail}` events for the active tenant with
filters by event class, `sig_scheme`, and endpoint. Failed events
display a pinned-warning row with a deep link to the
[§9 operator runbook](#9-operator-runbook) below. The panel is backed
by the gateway endpoints:

* `POST /api/v1/edge/binary-integrity/events` — operator ingest. Body
  is `{"endpoint": "<host-label>", "events": [BinaryVerifyEvent, ...]}`
  with up to 1000 events per request. Requires `audit.export`
  permission and the admin role; the tenant is resolved from the
  request's `X-Tenant-ID` header per the Edge auth rail. The handler
  re-validates every event against the schema above (defense-in-depth
  against relays that re-shape fields) and persists each accepted
  event through the standard `audit.Chainer` so it shows up in
  `/api/v1/audit/events` queries and SIEM exports alongside every
  other audit event.
* `GET /api/v1/edge/binary-integrity/events` — list view used by the
  dashboard. Query params: `?event=ok|fail`, `?sig_scheme=gpg|codesign|authenticode|dev`,
  `?endpoint=<host-label>`, `?limit=<1..200>`, `?cursor=<stream-id>`.
  Requires `audit.read` permission and the admin role.

### Operator ingest workflow

The install path emits JSON-lines to **stderr**, not to a Cordum API
directly — operators decide when and how to upload them, so the
install can run offline (air-gapped fleet rollouts) without requiring
gateway connectivity at install time. The supported workflow is:

```sh
# 1. Capture install-script stderr per host:
bash tools/scripts/install.sh --release-dir ./release-bundle \
  2> /var/log/cordum/install-binary-verify-$(hostname).log

# 2. Filter to JSON-lines only (drop any human-readable warnings the
#    script also writes to stderr) and post in bulk:
jq -c 'select(.event == "binary-verify-ok" or .event == "binary-verify-fail")' \
  /var/log/cordum/install-binary-verify-*.log \
  | jq -s '{endpoint: "'"$(hostname)"'", events: .}' \
  | curl -fsSL \
      -H "X-Tenant-ID: $CORDUM_TENANT" \
      -H "Authorization: Bearer $CORDUM_API_KEY" \
      -H 'Content-Type: application/json' \
      --data-binary @- \
      "$CORDUM_GATEWAY_URL/api/v1/edge/binary-integrity/events"
```

Windows operators run the equivalent against
`tools/scripts/install.ps1`'s stderr; the JSON-line shape is identical.

The endpoint label is operator-chosen — a hostname, an asset tag, or
any string ≤ 256 chars that identifies the install target. The
dashboard filter uses this label verbatim, so a stable convention
across the fleet helps SOC triage.

The response shape:

```jsonc
{
  "accepted": 12,
  "rejected": 0,
  "errors": []  // per-event validation errors when rejected > 0
}
```

A `202 Accepted` status with `rejected > 0` indicates partial success —
re-upload only the failed indices after fixing the cause. A `400 Bad
Request` indicates the whole batch was rejected (zero accepted) and
the operator should re-validate the input.

## 8A. Version-floor enforcement (EDGE-151-DOWNGRADE)

EDGE-151's signature verification accepts any binary signed by the
trusted release key regardless of version. EDGE-151-DOWNGRADE layers a
monotonic version-floor on top of that gate so an attacker who replays a
validly-signed older release — for instance one known to have a public
CVE — cannot ride the signature path through the install.

### Manifest version embedding

Every signed release manifest produced after EDGE-151-DOWNGRADE carries a
`# version: vN.N.N` line as its first line. The line is covered by the
detached GPG signature over `SHA256SUMS`, so an attacker cannot remove or
edit it without invalidating the signature. The release CI runs the same
embed via `.github/workflows/release.yml`'s sign-manifest job:

```
# version: v1.5.0
0123abc...  cordum-hook-linux-amd64
fedc987...  cordum-agentd-linux-amd64
```

Existing legacy bundles without an embedded `# version:` line still
verify; they are only rejected when a floor is already persisted (i.e.
after a first successful EDGE-151-DOWNGRADE-aware install).

### Persisted floor file

`tools/scripts/install.{sh,ps1}` read and write a JSON state file:

```jsonc
// $HOME/.cordum/binary-version-floor.json
{
  "version":     "v1.5.0",
  "advanced_at": "2026-05-17T13:54:00Z",
  "sig_scheme":  "gpg",
  "fingerprint": "<40-hex>",
  "operator":    "<USER env>",
  "reason":      ""
}
```

Path defaults to `$HOME/.cordum/binary-version-floor.json` (or
`%USERPROFILE%\.cordum\binary-version-floor.json` on Windows); override
via the `CORDUM_BINARY_FLOOR_FILE` env var or `-FloorFile` parameter to
install.ps1. The file mirrors the agentd convention
(`~/.cordum/edge/sessions/` in `core/edge/agentd/config.go`'s
`defaultStateDir`); it carries only public material (a version, a 40-hex
fingerprint, and the env-resolved operator handle) — no private key.

Writes are atomic via write-tmp + `mv -f` (POSIX rename) on Linux/macOS
and `[System.IO.File]::Move(tmp, dst, $true)` on Windows.

### Operator override for legitimate rollback

When an incident requires rolling back to a previous signed release —
for instance to back out a regression discovered in production — pass:

```sh
bash tools/scripts/install.sh --release-dir ./v1.4.0-bundle \
  --install-dir /usr/local/bin \
  --rollback-operator-override \
  --rollback-reason 'incident INC-2026-05-17: cordum-agentd v1.5.0 deadlock'
```

```pwsh
pwsh -NoProfile -File tools\scripts\install.ps1 -ReleaseDir .\v1.4.0-bundle `
  -InstallDir 'C:\Program Files\Cordum' `
  -RollbackOperatorOverride -RollbackReason 'incident INC-2026-05-17 ...'
```

Both flags are required together — the override flag without a non-empty
reason is refused at argv-parse time with
`BINARY-VERIFY-FAIL: --rollback-operator-override requires --rollback-reason <text>`.
Reason strings are truncated at 256 chars before being written to the
audit event.

### Audit-event schema

The install path emits structured stderr JSON-lines for floor mutations,
following the existing §8 audit-event shape with three additive fields:

| Field | Type | Notes |
|-------|------|-------|
| `event` | string | `binary-floor-advance` or `binary-floor-rollback` |
| `from` | string | Previous floor (`""` when no floor was set). |
| `to` | string | New floor — always the verified candidate version. |
| `sig_scheme` | string | Same values as §8 (`gpg`, `codesign`, `authenticode`, `dev`). |
| `fingerprint` | string | Same 40-hex value as §8. |
| `operator` | string | `$USER`/`%USERNAME%` of the install-script invoker, or `unknown` when empty. |
| `reason` | string | Always populated on `binary-floor-rollback`; empty on `binary-floor-advance`. |
| `exit_code` | integer | Always `0` (the script aborted before emitting the event on any failure path). |

`binary-floor-rollback` is mandatory whenever the override path advances
the floor *downward*; there is no silent-rollback path through
install.{sh,ps1}. Operators piping install stderr into the existing
EDGE-151-DASHBOARD ingest workflow (§8 sample `curl`) will see floor
events alongside per-binary verify events with no schema changes
required upstream.

### CI release-tag monotonicity gate

`.github/workflows/release.yml`'s `version-monotonicity` job runs before
the build matrix on every `v*` tag push. It refuses any tag whose semver
is not strictly greater than the most recent prior `v*` tag, preventing
an accidental sibling-release downgrade at tag time. The job uses
`tools/sign/cmd/version-cli monotonic-or-fail` which shares
`tools/sign.SemverCompare` with the install path, so CI and install
endpoints always agree on ordering.

### Out-of-scope residuals

* **Floor-file tampering by a local attacker with write access to
  `$HOME/.cordum/`** — the floor inherits the same trust principal as
  the installed binaries; defense-in-depth, not a replacement for OS-
  level integrity (see (d) above).
* **CI gate bypass by a repo admin publishing outside the workflow** —
  a privileged user can override branch protection rules or push a
  release artefact directly via the GitHub API; addressed by repo
  governance, not by the install path.
* **Semver tag forms outside `vMAJOR.MINOR.PATCH[-PRE]`** — non-semver
  tags fail the version-cli parse and cause the monotonicity job to
  refuse the publish; intentional, false-positive failures on malformed
  tags are preferable to silent acceptance.
* **CVE-database integration** — automated lookups against an external
  vulnerability database to refuse a vulnerable-but-monotonic version
  are out of scope; tracked as a separate P2+ task.

## 9. Operator runbook

### Verify a release manually

```sh
# From a freshly downloaded release directory (SHA256SUMS, .asc, and binaries):
bash tools/scripts/install.sh --release-dir ./release-bundle
```

In production mode the install path will refuse if `tools/keys/cordum-
release.pub.asc` is missing locally; clone the repository and run from
within it so the bundled pubkey is available.

### Rotate the release fingerprint

1. Follow §5 procedure end-to-end on the offline workstation.
2. Update three places with the new fingerprint **in this order**:
   * GitHub Actions secret `RELEASE_FINGERPRINT` (UI: Settings →
     Secrets → Actions).
   * `tools/scripts/install.sh` header comment-block, line for
     `CORDUM_PROD_FINGERPRINT_PIN`.
   * Commit `tools/keys/cordum-release.pub.asc` with the new public
     material.
3. Tag a synthetic patch release (`v<major>.<minor>.<patch>+rotate-N`)
   to verify the dual-sign overlap. The first run of
   `binaries-pr-validation.yml` after the secret rotates is the
   verification gate.

### Investigate a `BINARY-VERIFY-FAIL: <reason>` report

| Reason text | Most likely cause | Operator action |
|-------------|-------------------|-----------------|
| `unsigned manifest` | Release was distributed without `.asc`. | Re-download from official channel; do not run binaries. |
| `gpg signature invalid` | Bit-rot, mid-transit corruption, or active substitution. | Compare against known-good `SHA256SUMS.asc` from a second mirror; if both differ, treat as active substitution and rotate the install. |
| `hash mismatch <name>` | A single binary was tampered with after manifest signing. | The other binaries in the bundle remain trusted; replace only the affected binary from a fresh download. |
| `release pubkey fingerprint <got> does not match pinned <want>` | Pubkey file was rotated locally but `CORDUM_PROD_FINGERPRINT_PIN` was not, or vice-versa. | Reconcile per §5 — both values MUST match. |
| `post-activation hash mismatch` | An attacker raced the activation step. | Treat the endpoint as compromised and re-image. |
