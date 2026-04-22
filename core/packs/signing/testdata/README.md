# Pack signing test data

This directory holds keys and fixtures used by the pack-signing test
suite. **Every key here is a throwaway TEST key — NEVER trust these
keys in production.**

| File | Purpose |
|------|---------|
| `pack-signing-test.pub` | Ed25519 public key used by hello-pack's reference `pack.yaml.sig`. Clearly labeled as test-only. |

The matching private key is **NOT** checked in. It lives in the test
harness only (generated per-run with `crypto/rand`) so a real
publisher key cannot leak through this directory. Regenerate the
public key with `cordumctl pack keygen --out testdata.key` and copy
the `public_key_b64` line into the fixture.
