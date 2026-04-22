---
title: Policy Bundle Signing
sidebar_position: 16
---

# Policy Bundle Signing (Ed25519)

Cordum signs policy bundles so the safety kernel can prove that a bundle
came from your authorised control plane and has not been altered in
Redis or on disk. This page is a mirror of the canonical doc in the
repo (`docs/deployment/policy-signing.md`) — see that file for the
definitive version.

## Threat model

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

## Generating a key pair

```bash
# Private key (gateway-only secret)
openssl genpkey -algorithm ED25519 -out cordum-policy.key.pem

# Public key (deployed to every kernel replica)
openssl pkey -in cordum-policy.key.pem -pubout -out cordum-policy.pub.pem

# Base64 encodings for env vars
openssl pkey -in cordum-policy.key.pem -outform DER | base64 -w0
openssl pkey -in cordum-policy.key.pem -pubout -outform DER | base64 -w0
```

Choose a **key id** (e.g. `prod-2026-04`) that the kernel uses to look
up the trusted public key.

## Environment variables

### Gateway

| Variable                          | Description                                              |
| --------------------------------- | -------------------------------------------------------- |
| `CORDUM_POLICY_SIGNING_KEY`       | Ed25519 private key (PEM or base64 DER).                 |
| `CORDUM_POLICY_SIGNING_KEY_PATH`  | Alternative: path to the key file.                       |
| `CORDUM_POLICY_SIGNING_KEY_ID`    | Key id embedded in signatures. Default: `default`.       |
| `CORDUM_POLICY_STRICT`            | `off` / `warn` (default) / `enforce`.                    |

### Kernel

| Variable                          | Description                                              |
| --------------------------------- | -------------------------------------------------------- |
| `CORDUM_POLICY_PUBLIC_KEY_<ID>`   | Trusted public key for `<ID>`. Repeat for multiple keys. |
| `SAFETY_POLICY_PUBLIC_KEY`        | Legacy single-key fallback (migration only).             |
| `CORDUM_POLICY_STRICT`            | `off` / `warn` (default) / `enforce`.                    |

## Staged rollout

1. `off` — install default. No keys required.
2. `warn` — deploy key pair, switch both services to warn, observe logs
   for 24 h with zero `policy_signature_rejected` events.
3. `enforce` — kernel refuses unsigned/invalid bundles, gateway refuses
   saves without a signing key.

Each boot emits an INFO line with the active mode and key_id:

```
INFO policy signing: enabled component=gateway mode=enforce key_id=prod-2026-04
INFO policy signing: enabled component=kernel mode=enforce trusted_keys=prod-2026-04
```

## Key rotation

Rotate additively — never swap keys in place.

1. Generate `prod-2026-10`.
2. Deploy its public half alongside the old key on every kernel.
3. Roll the gateway with the new private key.
4. Re-save every bundle so each picks up a fresh signature.
5. Remove the retired public key from the kernel config.

## Incident response

If the kernel refuses a bundle in enforce mode, the previous known-good
policy stays active. Look for `audit_event=policy_signature_rejected`
in kernel logs and the `reason` field:

| `reason`             | What happened                                                   |
| -------------------- | --------------------------------------------------------------- |
| `unsigned`           | Bundle has no `_signature` (saved before signing was enabled).  |
| `malformed`          | `_signature` present but unreadable.                            |
| `untrusted_key`      | Signer’s key id is not in the kernel’s trust store.             |
| `invalid_signature`  | Content bytes differ from what was signed (possible tampering). |
| `no_trust_store`     | Kernel in enforce mode with no trusted keys (mis-deploy).       |

## `cordumctl policy sign` / `verify`

For file-based deployments (`SAFETY_POLICY_PATH=safety.yaml`):

```bash
CORDUM_POLICY_SIGNING_KEY=$(cat cordum-policy.key.pem) \
CORDUM_POLICY_SIGNING_KEY_ID=prod-2026-04 \
cordumctl policy sign --in safety.yaml

CORDUM_POLICY_PUBLIC_KEY_PROD-2026-04=$(cat cordum-policy.pub.b64) \
cordumctl policy verify --in safety.yaml
```

Exit codes: `0` verified, `1` invalid, `2` config error.
