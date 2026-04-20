# Policy Bundle Signing (Ed25519)

Cordum signs policy bundles so the safety kernel can prove that a bundle
came from your authorised control plane and has not been altered in
Redis or on disk. This document walks through the threat model, key
generation, environment variables, rollout, rotation, incident response,
and the `cordumctl policy sign` workflow.

## 1. Threat model

Policy bundles govern what agents may do (topic allow/deny, output
rules, MCP scopes, etc.). Every request flows through the kernel, so a
bundle the kernel trusts becomes the effective policy for the cluster.

Signing closes the following attack surfaces:

| Threat                                           | Without signing         | With `enforce` + signing        |
| ------------------------------------------------ | ----------------------- | ------------------------------- |
| Attacker with Redis write access rewrites bundle | Kernel loads tampered   | Kernel rejects (sig mismatch)   |
| Insider bypasses gateway UI and writes directly  | No record / no gate     | Kernel rejects (unsigned)       |
| Backup/restore replays an old bundle             | Applied silently        | Operator sees untrusted key     |
| Stolen signing key                               | N/A                     | Rotate key_id, re-sign, retire  |

The gateway holds the private signing key. Clients and agents never see
it. The kernel holds only public keys. Both sides log key ids but never
key material.

## 2. Generate a signing key pair

Any standard ed25519 generator works. With OpenSSL:

```bash
# Private key (gateway-only secret)
openssl genpkey -algorithm ED25519 -out cordum-policy.key.pem

# Public key (deployed to every kernel replica)
openssl pkey -in cordum-policy.key.pem -pubout -out cordum-policy.pub.pem
```

For environment-variable deployments, convert to base64:

```bash
# Private key as base64 (PKCS#8 bytes)
openssl pkey -in cordum-policy.key.pem -outform DER | base64 -w0

# Public key as base64 (SubjectPublicKeyInfo bytes)
openssl pkey -in cordum-policy.key.pem -pubout -outform DER | base64 -w0
```

Choose a **key id** — any short, stable string such as `prod-2026-04`.
The kernel uses this id to look up the trusted public key.

## 3. Environment variables

### Gateway (`cordum-api-gateway`)

| Variable                          | Required | Description                                                  |
| --------------------------------- | -------- | ------------------------------------------------------------ |
| `CORDUM_POLICY_SIGNING_KEY`       | Yes\*    | Ed25519 private key (PEM or base64-encoded DER).             |
| `CORDUM_POLICY_SIGNING_KEY_PATH`  | —        | Alternative: path to a file containing the same formats.     |
| `CORDUM_POLICY_SIGNING_KEY_ID`    | No       | Key id embedded in every signature. Default: `default`.      |
| `CORDUM_POLICY_STRICT`            | No       | `off` / `warn` (default) / `enforce`.                        |

\* Required when `CORDUM_POLICY_STRICT` is `warn` or `enforce`. In `off`
mode the gateway will save bundles without signing them.

### Safety kernel (`cordum-safety-kernel`)

| Variable                               | Required | Description                                                      |
| -------------------------------------- | -------- | ---------------------------------------------------------------- |
| `CORDUM_POLICY_PUBLIC_KEY_<ID>`        | Yes\*    | Trusted public key for `<ID>`. Repeat for multiple keys.         |
| `SAFETY_POLICY_PUBLIC_KEY`             | —        | Legacy single-key fallback. Honoured during migration only.      |
| `SAFETY_POLICY_PUBLIC_KEY_ID`          | —        | Key id for the legacy key. Default: `default`.                   |
| `CORDUM_POLICY_STRICT`                 | No       | `off` / `warn` (default) / `enforce`.                            |
| `SAFETY_POLICY_SIGNATURE_REQUIRED`     | —        | Legacy boolean. `true` upgrades file-based path to `enforce`.    |

\* Required in `enforce` mode; the kernel refuses to start without at
least one trusted key.

### File-based policies (`SAFETY_POLICY_PATH`)

When the kernel loads a policy from disk, it looks for a sibling
signature file next to the policy. The preferred format is JSON produced
by `cordumctl policy sign`; raw base64/hex signatures are accepted for
backward compatibility.

| Variable                            | Description                                                         |
| ----------------------------------- | ------------------------------------------------------------------- |
| `SAFETY_POLICY_SIGNATURE`           | Inline signature (JSON `Signature` or legacy base64/hex).            |
| `SAFETY_POLICY_SIGNATURE_PATH`      | Path to a signature file.                                           |
| _default_                           | Look for `${SAFETY_POLICY_PATH}.sig`.                               |

## 4. Rollout — `off` → `warn` → `enforce`

Signing is opt-in through three modes. Move left-to-right to avoid
outages.

### Phase 1: `off` (install / dev)

Default behaviour matches pre-signing releases.

```env
CORDUM_POLICY_STRICT=off
```

No keys required on either service. Bundles save and load exactly as
before.

### Phase 2: `warn` (production canary)

Generate a key pair, deploy the private key to the gateway and the
public key to every kernel replica, then switch both to `warn`:

```env
# Gateway
CORDUM_POLICY_STRICT=warn
CORDUM_POLICY_SIGNING_KEY=<base64>
CORDUM_POLICY_SIGNING_KEY_ID=prod-2026-04

# Kernel
CORDUM_POLICY_STRICT=warn
CORDUM_POLICY_PUBLIC_KEY_PROD-2026-04=<base64>
```

New bundles are signed on save. The kernel logs every unsigned or
failing bundle under `audit_event=policy_signature_missing` /
`policy_signature_rejected` — grep your logs to confirm every in-flight
bundle eventually carries a signature before moving on.

Once the kernel is emitting zero `policy_signature_*` warnings for 24 h,
advance to enforce.

### Phase 3: `enforce`

```env
CORDUM_POLICY_STRICT=enforce
```

The gateway refuses to save unsigned bundles (`503 Service Unavailable`
with a hint referencing `CORDUM_POLICY_SIGNING_KEY`). The kernel refuses
to load unsigned, tampered, or untrusted-key bundles — the previous
known-good policy stays active until a valid replacement arrives.

Boot logs confirm the active mode:

```
INFO policy signing: enabled component=gateway mode=enforce key_id=prod-2026-04
INFO policy signing: enabled component=kernel mode=enforce trusted_keys=prod-2026-04
```

## 5. Key rotation

Rotation is done additively — never swap keys in place.

1. Generate `prod-2026-10`.
2. Deploy its public half to every kernel replica alongside the old key:
   ```env
   CORDUM_POLICY_PUBLIC_KEY_PROD-2026-04=<old-base64>
   CORDUM_POLICY_PUBLIC_KEY_PROD-2026-10=<new-base64>
   ```
3. Roll the gateway with the new **private** key and `CORDUM_POLICY_SIGNING_KEY_ID=prod-2026-10`.
4. Re-save every custom bundle via the dashboard or API so each picks
   up a signature under the new id. Alternatively, for file-based
   policies, run `cordumctl policy sign`.
5. Once all bundles verify under `prod-2026-10`, remove
   `CORDUM_POLICY_PUBLIC_KEY_PROD-2026-04` from the kernel config.

## 6. Incident response — kernel refused a bundle

In `enforce` mode a rejected bundle leaves the previous known-good
policy active; the kernel does **not** fail open. You will see one of:

| `audit_event`                     | `reason`             | What happened                                                      |
| --------------------------------- | -------------------- | ------------------------------------------------------------------ |
| `policy_signature_rejected`       | `unsigned`           | Bundle has no `_signature` map (saved before signing was enabled). |
| `policy_signature_rejected`       | `malformed`          | `_signature` present but unreadable.                               |
| `policy_signature_rejected`       | `untrusted_key`      | Signer’s key id is not in the kernel’s trust store.                |
| `policy_signature_rejected`       | `invalid_signature`  | Content bytes differ from what was signed (possible tampering).    |
| `policy_signature_rejected`       | `no_trust_store`     | Kernel in enforce mode with no trusted keys (mis-deploy).          |

Response checklist:

1. `curl /api/v1/policy/bundles` on the gateway and locate the bundle by
   id (logged alongside the rejection).
2. Inspect the Redis record and compare the stored `content` string to
   the last known-good snapshot via
   `cordumctl policy sign --in snapshot.yaml` and diff.
3. If the snapshot diverges unexpectedly, treat it as an intrusion:
   rotate the signing key and re-sign every bundle from your source of
   truth.
4. If the divergence is legitimate (operator edit), re-save through the
   gateway so it picks up a fresh signature.

## 7. `cordumctl policy sign` / `verify`

For file-based deployments (`SAFETY_POLICY_PATH=safety.yaml`) use the
CLI to sign the policy out-of-band:

```bash
# Sign (private key in CORDUM_POLICY_SIGNING_KEY by default)
CORDUM_POLICY_SIGNING_KEY=$(cat cordum-policy.key.pem) \
CORDUM_POLICY_SIGNING_KEY_ID=prod-2026-04 \
cordumctl policy sign --in safety.yaml
# writes safety.yaml.sig

# Verify (reads trusted public keys from CORDUM_POLICY_PUBLIC_KEY_<ID>)
CORDUM_POLICY_PUBLIC_KEY_PROD-2026-04=$(cat cordum-policy.pub.b64) \
cordumctl policy verify --in safety.yaml
```

Exit codes:

| Code | Meaning                                                          |
| ---- | ---------------------------------------------------------------- |
| 0    | Signature verified                                                |
| 1    | Signature invalid (wrong key, tampered payload, untrusted key_id) |
| 2    | Configuration error (missing flags, unreadable files, bad key)    |

Use `--key-env` / `--public-key-env` to point the CLI at non-default env
var names for multi-environment setups.

## 8. Operational notes

* The gateway signs the exact bytes stored in Redis — not a YAML
  re-serialisation. Never edit bundle content directly in Redis; always
  go through the gateway or re-sign offline.
* Signatures are kept on a sibling `_signature` key in the bundle map,
  outside the YAML payload, so `kernel.go:extractPolicyFragment` does
  not see them as policy fields.
* In `warn` mode the kernel still loads failing bundles; use this
  period only to observe which bundles are unsigned.
* Snapshots persist signatures — a rollback to a snapshot restores its
  original signature verbatim. Rotate keys by re-saving rather than by
  editing snapshots.
