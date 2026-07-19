# Authenticated handshake interoperability gate

This build-tagged gate runs the public CAP Go, Python, and Node worker-trust APIs
against Cordum's production handshake resolver and session issuer. It is local/CI
evidence, not a certification or an external interoperability claim.

## Trust boundaries

- Redis must be a reachable real server supplied through
  `CAP_HANDSHAKE_REDIS_URL`; absence or connection failure is fatal, never a skip.
- NATS is an embedded loopback server from the pinned Go dependency
  `github.com/nats-io/nats-server/v2 v2.12.6`; the gate does not connect to a
  default or external broker.
- Identity enrollment uses `configsvc.Service`, `workercredentials.Service`,
  `AgentIdentityStore`, and `NewCredentialHandshakeTrustResolver` over Redis.
- Go is installed from a temporary local module proxy with `GOWORK=off`; Python
  runs from a wheel in a clean venv; Node runs from an npm-packed tarball in a
  clean consumer. Python and Node sources are exported from tracked CAP `HEAD`.
  Both runners require that full CAP `HEAD` resolve the revision suffix pinned
  in Cordum's `go.mod`; CI additionally supplies the same full SHA through
  `CAP_HANDSHAKE_CAP_SHA`.
- Client processes receive an allowlisted OS environment plus only their
  explicit handshake inputs. Redis/CI credentials are not inherited, Python
  user-site imports are disabled, and `PYTHONPATH`, `PYTHONHOME`, and
  `NODE_PATH` are cleared.
- Per-run worker, agent, tenant, key, and Redis IDs are random. Cleanup deletes
  exact owned challenge/request/nonce/session/revocation keys and restores the
  pre-run worker-config bytes. It never flushes or deletes unrelated state.

## Run

Start an isolated Redis container (or provide an equivalent dedicated URL):

```sh
docker run --rm -p 63288:6379 \
  redis:7.4.2-alpine@sha256:02419de7eddf55aa5bcf49efb74e88fa8d931b4d77c07eff8a6b2144472b6952
```

Linux/CI:

```sh
CAP_HANDSHAKE_REDIS_URL=redis://127.0.0.1:63288/0 \
  ./tests/handshakeinterop/run.sh /path/to/cap
```

Windows PowerShell:

```powershell
$env:CAP_HANDSHAKE_REDIS_URL = 'redis://127.0.0.1:63288/0'
./tests/handshakeinterop/run.ps1 -CapRoot D:/path/to/cap
```

The gate proves ISSUE, RENEW, rotation/supersession, required negatives in every
installed SDK, legacy-subject rejection, deterministic cross-replica challenge
consumption, atomic concurrent replay fencing, and reconnect after broker
restart. Negative subtests assert both unchanged Redis authority bytes and
`redis_authority_delta=0`; the concurrent pair asserts one accept, one replay,
and one newly persisted active-session record whose JTI matches the accepted
bound token (`redis_mint_delta=1`). Subscriber startup and reconnect use live
request-path readiness probes rather than fixed sleeps.

The complete 38-vector declarative mutation manifest is executable separately:

```sh
cd /path/to/cap
go test -count=1 ./test/interop/handshake
```

Client JSON contains only language, case, status, and boolean proof fields.
Tokens, private keys, signatures, and raw packets are never printed.
