# Install troubleshooting

`cordumctl doctor` is the one-stop verifier for a Cordum install. It
exercises every interaction the platform actually needs — gateway TLS,
auth, NATS, Redis, workers, demo pack, policy bundles, per-service
readyz, cert expiry — and prints a fix hint next to each failure.

This page is the decision tree: which check fails → what's actually
broken → what to run. Keep it bookmarked; it doubles as the
post-mortem playbook for "my install doesn't work" in #help.

## Start here

```bash
cordumctl doctor
```

- All `ok` / `skip` → install is healthy. Done.
- Any `fail` → read the table below, scoped to the failing check id.
- Any `warn` → read the table, but don't block on it unless
  `--strict` is in play.
- Usage error (exit 2) → re-read the flags; `--fix` and `--json` are
  mutually exclusive.

## Check → cause → fix

### `gateway_reachable`

| Detail contains | Actual cause | Fix |
|-----------------|--------------|-----|
| `connection refused` | API gateway container not running | `docker compose up -d api-gateway`, then re-run doctor |
| `x509: certificate signed by unknown authority` | Host doesn't trust the self-signed CA | `export CORDUM_TLS_DIR=./certs` and re-run, or pass `--cacert ./certs/ca/ca.crt`; or install the CA in your OS trust store |
| `i/o timeout` | Gateway is up but overloaded or behind a firewall | `docker compose logs api-gateway`; check Docker network plumbing |
| Status code `5xx` | Gateway startup incomplete (dependencies not ready) | Wait 30s, re-run. If persists: `docker compose logs api-gateway` |

### `gateway_auth`

| Detail | Cause | Fix |
|--------|-------|-----|
| `no API key configured` | `CORDUM_API_KEY` not set | `export CORDUM_API_KEY=<key-from-.env>` (or pass `--api-key`) |
| `401 Unauthorized` | Key doesn't match gateway's `CORDUM_API_KEYS` | Re-check `.env`, then `docker compose up -d api-gateway` to pick up env changes |
| `/api/v1/status returned 5xx` | Gateway dependencies not loaded (Redis/NATS) | Fix the downstream check (`redis_ok`, `nats_connected`) first |

### `nats_connected`

| Detail | Cause | Fix |
|--------|-------|-----|
| `gateway reports NATS disconnected` | NATS container crashed or network partition | `docker compose logs nats`; verify `NATS_TOKEN` matches gateway's env |
| `skipped — /api/v1/status unavailable` | Earlier check failed; rerun after fixing `gateway_auth` | — |

### `redis_ok`

| Detail | Cause | Fix |
|--------|-------|-----|
| `gateway reports Redis not OK` | Redis rejects the gateway's auth or is OOM | `docker compose logs redis`; verify `REDIS_PASSWORD` matches both sides |
| `skipped` | Upstream check failed | Fix `gateway_auth` first |

### `workers_registered`

- `warn: no workers registered` → you haven't installed any packs. Run
  `cordumctl pack install ./demo/quickstart/pack` (or whichever pack
  you want to run first).
- If pack is installed but still zero → check the worker container
  logs: `docker compose logs <pack-worker>`. Worker tokens may have
  expired; see [worker-credentials.md](../deployment/worker-credentials.md).

### `build_info`

- `warn: build version "dev"` → running an unpinned image (`:dev`
  tag). Fine for local dev, not for a production deploy.
- Fix: pin via `CORDUM_VERSION=1.2.3 docker compose pull && up -d`.

### `service_*` (scheduler / safety-kernel / context-engine / workflow-engine / mcp / dashboard)

- `skip: host port N not exposed` → release compose bundles the
  services behind the gateway. The per-host port is not meant to be
  dialled directly. This is **expected** and safe to ignore.
- `fail: connection refused` with no status snapshot → use the
  service's fix hint (`docker compose logs <service>`). The gateway
  wasn't up either, so the doctor couldn't fall back to "port not
  exposed".
- `fail: 5xx` → the service is up but unhealthy; logs will say why.

### `demo_pack_installed`

- `warn: demo-quickstart pack is not installed` → on a fresh install
  the demo pack ships under `demo/quickstart/pack/`. Run:
  `cordumctl pack install ./demo/quickstart/pack`.
- You may see this in production deploys that don't want the demo pack
  — it's a warn by design, not a fail.

### `policy_bundle_loaded`

- `warn: no policy bundles loaded` → the demo pack seeds a default
  bundle, so `cordumctl pack install ./demo/quickstart/pack` fixes
  this too.
- `warn: bundles present but none enabled` → someone published a
  bundle but never activated it. `cordumctl policy activate <id>`
  after `cordumctl policy list`.
- `warn: enabled bundle(s) loaded but demo-quickstart policy rules
  absent` → the gateway has *some* active policy (e.g. `secops/core`)
  but NONE of the demo quickstart rules (`demo-quickstart-greet-allow`,
  `-delete-deny`, `-admin-approve`) are present. The ALLOW/DENY/APPROVE
  demo cannot render at all. Run
  `cordumctl pack install ./demo/quickstart/pack` to merge the demo
  policy fragment into the bundle. Production deploys that
  deliberately don't ship the demo can ignore this warn.
- `warn: enabled bundle(s) with partial demo policy — missing N of 3
  required rule(s): ...` → some demo rules survived but others are
  missing (common after a partial reinstall or a bundle edit that
  dropped rule ids). The quickstart demo will render incomplete
  verdicts — e.g. ALLOW works but DENY stops firing. Reinstall the
  demo pack (`cordumctl pack install ./demo/quickstart/pack`) — the
  pack install overwrites the full fragment so every
  `demo-quickstart-*` rule is merged atomically. doctor names the
  specific missing rule ids in the detail so you can verify the fix.

### `version_skew`

- `warn: minor version skew: cordumctl=X.Y.Z gateway=X.Y'.Z'` → your
  CLI is a minor version behind/ahead of the running stack.
  `docker compose pull && up -d` brings the stack up; reinstall
  cordumctl from the matching release.
- `fail: major version mismatch` → don't run mutating commands until
  the versions agree. Major-version skews break wire compat.

### `tls_cert_expiry`

- `warn: CA cert expires in Nd` (N < 7) → regenerate soon. Rotation
  is backwards-compatible if you stage it (see
  [deployment/certs.md](../deployment/certs.md)).
- `fail: CA cert expires in Xh` (X < 24) → regenerate now:
  `cordumctl generate-certs --force --days 365`, then restart all
  services so the new cert is picked up.
- `skip: certs/ca/ca.crt not present` → you're deployed with an
  external TLS terminator or plain HTTP in a private network. Doctor
  honours that and moves on.

## "Everything is fail"

If every check lands on fail, it's usually one of three things:

1. **Gateway is down.** Fix `gateway_reachable` first; the rest
   cascade-recover.
2. **Wrong URL.** `cordumctl doctor --gateway https://...` should
   point at the live gateway. `.env` ships the canonical value.
3. **Wrong TLS trust.** If the gateway uses the self-signed dev CA
   and your shell doesn't trust it, every HTTPS probe fails. Either
   `--insecure` (dev only) or `--cacert ./certs/ca/ca.crt`.

## Using `--fix` safely

`cordumctl doctor --fix` walks each fail in turn:

```
[FIX] NATS connected (nats_connected)
      detail: gateway reports NATS disconnected
      suggested: docker compose logs nats  (verify NATS_TOKEN + nats service health)
      run now? [y/N/a]
```

- `y` runs the suggested command and re-checks.
- `N` (default, also empty line) leaves the check as fail.
- `a` aborts the rest of the queue.

Fixes containing destructive substrings (`--force`, `down -v`,
`reset --hard`, `rm -rf`, `dropdb`, `DELETE FROM`) require a second
confirmation: the operator must type `yes` (not just `y`). This keeps
`generate-certs --force` from running without a second thought.

## CI health gate

```bash
cordumctl doctor --json | jq -e '.exitCode == 0'
```

The `--json` envelope is stable (see
[COMMANDS.md](../../COMMANDS.md#cordumctl-doctor--install-verification)).
Fail the build if any check returns `fail`, and include the decoded
envelope in the build log so the failing fix hint is visible.

## Running against docker compose in CI

The integration test at
`tools/scripts/doctor_integration_test.sh` drives the real compose
stack: baseline → stop NATS → assert recovery. Invoke via
`make doctor-test` from a job that has Docker running. Requires
`CORDUM_API_KEY` in the env; the script refuses to run without it.

## See also

- [COMMANDS.md](../../COMMANDS.md) — reference for every doctor flag
  and check.
- [docs/troubleshooting.md](../troubleshooting.md) — post-install
  runtime issues (flaky workers, job-timeouts, DLQ pile-up).
- [docs/deployment/certs.md](../deployment/certs.md) — cert rotation.
- [tools/scripts/quickstart.sh](../../tools/scripts/quickstart.sh) —
  doctor runs as the final verification step.
