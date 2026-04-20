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

## Out of scope (other tasks own these)

- Trust-score computation (verified publisher, test coverage, usage) — separate registry task.
- Signature enforcement in `cordumctl pack install` — separate gating task.
- Bulk-signing the 28 production packs — ops work requiring real publisher keys per pack.
- Server-side pack signature verification in the gateway install path — separate gateway task consuming `signing.VerifyPack`.
