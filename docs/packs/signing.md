# Pack signing

Cordum packs ship with an Ed25519 signature that binds `pack.yaml` and
every file it references (schemas, workflows, overlays) to a known
publisher. Operators use the signature to catch supply-chain tampering
— a malicious edit to a workflow or schema invalidates the signature
even if `pack.yaml` itself is unchanged.

Pack signing is implemented by `core/packs/signing` (library) and
`cordumctl pack {keygen,sign,verify-signature,export-key}` (CLI).

## Threat model

| Threat | Defence |
|--------|---------|
| Tampered workflow silently executes a new tool | Every referenced file is hashed; signature fails verification |
| Attacker replaces `pack.yaml` to add a referenced file post-sign | `VerifyPack` detects files referenced on disk but absent from the signed manifest |
| Stolen publisher signing key | Publisher rotates KID; registry advertises both old+new for a grace window; operators refresh trusted-keys |
| Cross-context signature replay (publisher key used to sign a delegation token or a license) | Domain separation — the signed preimage is `cordum.pack.v1\n<canonical-json>`; the delegation and licensing domains use different strings |
| Symlinked `/etc/passwd` signed as a schema | Walker rejects symlinks at sign time (`ErrSymlinkRejected`) |
| `../secret.key` referenced by a handcrafted `pack.yaml` | Walker rejects paths that resolve outside the pack root (`ErrEscapesRoot`) |

## What gets signed

The canonical manifest covers:

- `pack.yaml` itself (kind: `manifest`)
- Every file under `resources.schemas[].path` (kind: `schema`)
- Every file under `resources.workflows[].path` (kind: `workflow`)
- Every file under `overlays.config[].path` and `overlays.policy[].path` (kind: `overlay`)

Not signed: `README.md`, worker binaries, `go.mod`/`go.sum`, the
contents of `deploy/`, and any file not referenced from `pack.yaml`.
These are operator trust-on-first-use surfaces.

Paths are stored as forward-slash POSIX strings regardless of the
host OS, so a Windows-signed pack verifies on Linux and vice versa.

## Reference workflow

```sh
# 1. Generate a fresh Ed25519 signing key. Writes to
#    ~/.cordum/pack-signing.key at 0600; prints kid + public_key_b64.
cordumctl pack keygen

# 2. Sign the pack. Writes pack.yaml.sig next to pack.yaml.
cordumctl pack sign ./my-pack

# 3. Publish the public key. Send {kid, algorithm, public_key_b64}
#    to the registry.
cordumctl pack export-key

# 4. Verify a pack's signature against a trusted keyring.
cordumctl pack verify-signature ./my-pack --trusted-keys=/etc/cordum/trusted-pack-keys
```

`cordumctl pack verify-signature` (not `pack verify`, which is the
legacy policy-simulation check) runs `signing.VerifyPack`:
re-walks the pack, rebuilds the canonical manifest, asserts every
hash matches, and runs `ed25519.Verify` over the domain-separated
preimage.

## Envelope format

The signature file `pack.yaml.sig` is written in one of two
interchangeable on-disk formats. Both deserialise to the same
`signing.SignedManifest` Go type.

### YAML (default, human-diffable)

```yaml
apiVersion: cordum.io/v1alpha1
kind: PackSignature
metadata:
  pack_id: hello-pack
  pack_version: 0.1.0
  signed_at: 2026-04-20T12:00:00Z
signature:
  key_id: pack-ab12cd34
  algorithm: ed25519
  value: <base64>
  domain: cordum.pack.v1
manifest:
  version: 1
  pack_id: hello-pack
  pack_version: 0.1.0
  signed_at: 2026-04-20T12:00:00Z
  algorithm: ed25519
  files:
    - path: pack.yaml
      sha256: <hex>
      size_bytes: 812
      kind: manifest
    - path: schemas/HelloInput.json
      sha256: <hex>
      size_bytes: 121
      kind: schema
```

### JSON (tooling-friendly)

Write with `cordumctl pack sign --json --out pack.yaml.sig.json`. The
body is identical; only the serialisation changes.

## Domain separation

The signing preimage is
```
cordum.pack.v1\n<compact-json(manifest-with-sorted-files)>
```

Other Cordum signature domains use distinct strings:

- Delegation tokens: JWT `iss=cordum` + the JWT header/payload binding (distinct structure).
- Licensing: license signatures use their own domain-scoped preimage.
- MCP outbound signer: separate domain under `core/mcp/outbound/`.

If a publisher's signing key is ever reused in another domain (it
should not be), an attacker who obtains a pack signature cannot
replay it as a delegation token or a license — the preimage prefix
differs.

## Key rotation

1. Publisher generates a new keypair (`cordumctl pack keygen --out newkey.key --kid pack-v2`).
2. Publisher submits the new public key to the registry.
3. Registry advertises BOTH kids (`pack-v1` + `pack-v2`) for a
   documented TTL (e.g. 30 days).
4. Publisher signs new pack versions with the new kid.
5. After the TTL expires, the registry removes the old kid.

Operators that pin `--trusted-keys` refresh their local keyring
during the grace window so verification never breaks mid-rotation.

## Forward compatibility

The envelope's `apiVersion: cordum.io/v1alpha1` field is the
compatibility anchor. A future v2 envelope would set
`apiVersion: cordum.io/v1` (or `v1beta1`) so old + new envelopes can
coexist during a migration. The `manifest.version` integer exists
for the same reason on the signed body.

## Testing

Round-trip covered by:

- `core/packs/signing/sign_test.go` + `verify_test.go` — library-level.
- `core/packs/signing/envelope_test.go` — YAML ↔ JSON deserialisation equivalence.
- `cmd/cordumctl/pack_sign_test.go` — end-to-end sign → verify via the CLI, including tamper detection and JSON envelope round-trip.

```sh
go test -count=3 ./core/packs/signing/... ./cmd/cordumctl/...
go test -cover ./core/packs/signing/...
```

## Installation and Verification

Signature verification runs on BOTH sides of `cordumctl pack install`:
`cordumctl` checks locally for fast feedback, and the gateway
re-verifies server-side before persisting any install state. The
server-side check is the zero-trust rail — if `cordumctl` is patched
or a hand-rolled curl upload bypasses the client, the gateway's
verify still gates the install.

### Strict vs non-strict mode (decision tree)

| Mode | Unsigned pack | Tampered or bad signature | Typical deployment |
|------|---------------|---------------------------|--------------------|
| **Non-strict** (default) | Install proceeds with a loud `WARNING` on stderr | Install refused | Dev or pre-Phase-1-moat pilots where ecosystem signing is still rolling out |
| **Strict** | Install refused with `pack.unsigned` | Install refused | Production, compliance-gated tenants, and any deploy where supply-chain attestation is non-negotiable |

Strict mode flips two ways on each side:

- **Client (cordumctl):** `--strict` CLI flag, or `CORDUM_PACK_STRICT=true` env.
- **Gateway:** `CORDUM_GATEWAY_PACK_STRICT=true` env at boot, or
  `SET cfg:packs:strict_mode true` in Redis at runtime (propagates
  across gateway replicas within one second via the in-process
  cache). Use the Redis flip for incident response; env for
  steady-state.

### Trust-store setup

Publishers register their signing public key with every operator who
installs their pack. The operator drops the `<kid>.pub` file (produced
by `cordumctl pack export-key`) into a trusted-keys directory.

Client-side keyring, in precedence order:

1. `--trusted-keys=<dir>` CLI flag (per-invocation).
2. `CORDUM_PACK_TRUSTED_KEY_<KID>=<base64-pub>` env (per-shell bootstrap).
3. `$CORDUM_PACK_TRUSTED_KEYS_DIR` or `~/.cordum/trusted-keys/` (per-user default).
4. Embedded `cordum-verified.pub` shipped with the binary — the Cordum counter-signing key.

```sh
# Publisher exports their public key for operators.
cordumctl pack export-key --key $CORDUM_PACK_SIGNING_KEY > publisher.pub

# Operator drops it into their trust store.
mkdir -p ~/.cordum/trusted-keys
mv publisher.pub ~/.cordum/trusted-keys/acme.pub
chmod 0600 ~/.cordum/trusted-keys/acme.pub
```

0600 permissions are required on POSIX; less-restrictive modes trigger
`ErrTrustedKeyPermissions`. Windows ACLs are not Go-mode-checkable so
this rail warns rather than fails there — operators are responsible
for ACL hygiene on NTFS installs.

Server-side keyring lives in Redis under `packs:trusted_keys:<kid>`.
An admin endpoint for registering keys at the gateway lands in a
separate task; for now, deployers bootstrap via env
`CORDUM_GATEWAY_PACK_TRUSTED_KEY_<KID>=<base64>` on every gateway
replica.

Optional publisher metadata rides alongside each trusted key so the
installed-pack `verification.publisher_id` field carries a meaningful
name instead of falling back to the bare kid. Two registration paths:

```sh
# Redis (persistent, shared across replicas). JSON document keyed
# by kid; at minimum set publisher_id, optionally display_name +
# added_at.
redis-cli SET packs:publishers:acme \
  '{"publisher_id":"acme-corp","display_name":"Acme Corp","added_at":"2026-04-20T00:00:00Z"}'

# Environment (per-gateway bootstrap shorthand). publisher_id only;
# display_name falls back to publisher_id.
export CORDUM_GATEWAY_PACK_PUBLISHER_ACME=acme-corp
```

When a pack signed by kid `acme` installs, `verification.publisher_id`
reports `acme-corp`. When no publisher record exists, the field falls
back to the kid itself so the installed-pack metadata always carries
a non-empty publisher_id on a successful verify (required by the
`signed / publisher_id / verified_at` DoD).

### `--require-cordum-sig`

When an enterprise deploy wants a two-signature trust chain — the
publisher's signature *plus* a Cordum counter-signature as proof the
pack passed Cordum's review workflow — pass `--require-cordum-sig` on
install. The gate then looks for a sibling `pack.yaml.sig.cordum`
envelope next to `pack.yaml.sig`; if present, it must verify against
the embedded Cordum counter-signing key. If the cordumctl build ships
without the counter-signing key (open-source distribution before the
counter-signing workflow lands), the gate returns
`ErrCordumSigUnavailable` rather than silently accepting.

### Error-code reference

| Error code | HTTP | Operator remediation |
|------------|------|----------------------|
| `pack.unsigned` | 400 | Sign the pack with `cordumctl pack sign` or (client-only) drop out of strict mode with `--strict=false` / `CORDUM_PACK_STRICT=false`. |
| `pack.bad_signature` | 400 | Regenerate the signature with a fresh `cordumctl pack sign` run. A stale signature from a prior `pack.yaml` revision fails here. |
| `pack.tampered` | 400 | Some signed file changed on disk between sign and install — rebuild the bundle from the signed source, or re-sign the current tree. |
| `pack.unknown_kid` | 400 | The pack was signed with a kid not registered in the trust store. Add the publisher's `.pub` file to `~/.cordum/trusted-keys/` (or the gateway's `packs:trusted_keys:` Redis namespace). |
| `pack.missing_cordum_sig` | 400 | `--require-cordum-sig` is on but the pack has no `pack.yaml.sig.cordum` envelope (or the counter-signature kid doesn't match the embedded Cordum kid). Drop `--require-cordum-sig`, or wait for Cordum's review workflow to counter-sign the pack. |
| `pack.malformed` | 400 | Signature file exists but is neither valid YAML nor JSON, or the envelope schema is broken. Re-run `cordumctl pack sign`. |
| `pack.verify_unavailable` | 400 | Gateway could not load its trust keyring (Redis outage, env parse failure). Check gateway logs; retry once Redis is healthy. |

### Audit events

Every install emits exactly one audit event:

- `pack.install.verified` on success. Message body contains
  `signed=<bool> kid=<kid> cordum=<bool>`. A pre-existing install that
  didn't go through the verify gate reads as unsigned; the field is
  not back-filled.
- `pack.install.rejected` on failure. Message body contains the
  full typed error (`<error_code>: <message>`) so SIEM queries can
  pivot on the code field.

### Runtime strict-flag flip (hot failover)

When a compromised publisher key surfaces, ops can escalate the
fleet immediately without a redeploy:

```sh
# Gateway side: propagates across replicas within 1s.
redis-cli SET cfg:packs:strict_mode true

# cordumctl side: ship CORDUM_PACK_STRICT=true via config-management,
# or push a follow-up key revocation so --trusted-keys drops the
# compromised kid.
```

Strict mode + an empty keyring is a refusal, not an accept — the gate
returns `ErrEmptyKeyringInStrictMode` so a misconfigured "strict
before any keys loaded" state fails closed.

## Out of scope (other tasks own these)

- Trust-score computation (verified publisher, test coverage, usage) — separate registry task.
- Bulk-signing the 28 production packs — ops work requiring real publisher keys per pack.
- Public pack registry at `packs.cordum.io` — separate epic-01a33e79 task.
- Cordum counter-signing review workflow — separate epic-01a33e79 task that populates the embedded `cordum-verified.pub`.
- Dashboard "Verified by X" badge — consumes the `verification` field on `GET /api/v1/packs/installed/{id}` (added by this task); dashboard wiring is a separate task.
