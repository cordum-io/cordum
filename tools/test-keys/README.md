# `tools/test-keys/` — TEST-ONLY synthetic keys. **NOT FOR PRODUCTION.**

This directory holds OpenPGP keypairs used exclusively by the binary-integrity
test harness (`tools/sign/...`, `tools/scripts/install_test.{sh,ps1}`,
`.github/workflows/binaries-pr-validation.yml`). They have no relationship to
the production release-signing key whose fingerprint is pinned at build time
via `-ldflags -X cordum/tools/sign.PinnedReleaseFingerprint=...` and whose
public material lives under `tools/keys/cordum-release.pub.asc` (step-5).

## What is here

| File | Content | Sensitivity |
|------|---------|-------------|
| `TEST-ONLY-release.pub.asc` | Public half of the test signing key | Safe to commit |
| `TEST-ONLY-release.priv.asc` | Private half of the test signing key | **Public on purpose** — see below |
| `gen.sh` | Reproducible re-generation script (uses `gpg --batch`) | Source |

## Why a private key is committed

The private half is committed deliberately so that:

1. Local `tools/scripts/install_test.sh` runs without any external secret.
2. CI on forks (where production GPG secrets are not provisioned) can still
   exercise the synthetic-tampered + unsigned scenarios in
   `binaries-pr-validation.yml`.
3. Reviewers can re-derive every signed fixture from a single committed
   keypair instead of opaque binary blobs.

The `install.sh` pre-activation gate refuses any manifest signed by this
fingerprint **unless** `--dev-allow-unsigned` is set, and the matching
production gate (`PinnedReleaseFingerprint` baked in at release build) further
rejects this fingerprint as a release source. A lint guard in step-7 +
`golangci-lint` config keeps any TEST-ONLY material under this directory
and prevents leak into `tools/keys/` or the repo root.

## Regenerating

```bash
cd tools/test-keys
./gen.sh           # writes TEST-ONLY-release.{pub,priv}.asc, prints fingerprint
```

If the fingerprint changes, update the `TEST-ONLY-*` fingerprint in
`tools/sign/verifier_test.go` (auto-derived) and in any documentation that
quotes it. **Do not** copy this key under `tools/keys/`. **Do not** use it to
sign anything outside the test harness.
