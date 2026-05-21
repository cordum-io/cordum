# `tools/keys/` — production release pubkey bundle

This directory **will** hold the public half of the Cordum production
release-signing key. `tools/scripts/install.sh` imports the pubkey from
`tools/keys/cordum-release.pub.asc` and refuses to verify any release
whose manifest signer fingerprint differs from the value pinned in
`install.sh` (variable `CORDUM_PROD_FINGERPRINT_PIN`) or — at build time
for the cordum-hook/agentd/claude binaries — in
`github.com/cordum/cordum/tools/sign.PinnedReleaseFingerprint`.

## Status

> **Pubkey not yet provisioned.** EDGE-151 ships the verifier package
> (`tools/sign/`), CI signing workflow
> (`.github/workflows/release.yml`), and pre-activation gate
> (`tools/scripts/install.sh --release-dir ...`) without the actual
> production key material. Until Yaron provisions the
> `GPG_RELEASE_KEY_PRIVATE` GitHub secret (see
> `docs/security/binary-signing.md`) and commits the matching public
> key here, the install path will accept only
> `--dev-allow-unsigned` with a TEST-ONLY key under `tools/test-keys/`.

## Provisioning procedure (when the production key is generated)

1. Generate the keypair on an offline workstation (Yubikey-backed
   preferred). Use a sign-only ed25519 or RSA-3072 key with no
   expiration on the primary signing UID.
2. Export the **public** half ASCII-armored:
   ```sh
   gpg --batch --armor --export <fingerprint> > cordum-release.pub.asc
   ```
3. Record the 40-hex SHA-1 fingerprint:
   ```sh
   gpg --with-colons --list-keys <fingerprint> \
     | awk -F: '$1=="fpr" {print $10; exit}'
   ```
4. Update `CORDUM_PROD_FINGERPRINT_PIN` in `tools/scripts/install.sh`
   (search for the EDGE-151 header) **and** set the GitHub Actions
   secret `RELEASE_FINGERPRINT` to the same value. They MUST match —
   the workflow bakes the value into binaries via `-ldflags`, and
   install.sh re-checks it at install time, so a single source of
   truth is required.
5. Commit `tools/keys/cordum-release.pub.asc` to the repository.
6. Install the **private** half into the GitHub Actions secret
   `GPG_RELEASE_KEY_PRIVATE` and its passphrase into
   `GPG_RELEASE_KEY_PASSPHRASE`. Never commit the private key.

See `docs/security/binary-signing.md` for the full threat model
including key rotation, revocation, and the residual local-admin
limit (root processes that swap both binary and pubkey-file together
remain out of scope by design).
