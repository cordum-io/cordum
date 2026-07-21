# CAP-PRODUCTION integration gate

This build-tagged gate proves Cordum's CAP-PRODUCTION scheduler and a worker
from the CAP Go module pinned in `go.mod` interoperate over real transport. It
is local/CI evidence, not an external certification claim.

## Trust and transport boundaries

- `CAP_PRODUCTION_REDIS_URL` must name a reachable external Redis server.
  Absence or connection failure is fatal; the suite contains no `t.Skip` path.
- NATS uses the pinned `github.com/nats-io/nats-server/v2 v2.14.3` dependency
  on loopback. The broker requires and verifies dynamically generated client
  certificates, so scheduler and worker traffic crosses real mutual TLS.
- The scheduler runs its production raw-admission, replay, session, safety,
  dispatch-fence, and durable-result boundaries. The CAP managed worker runs
  production validation, authenticated handshake, durable Redis replay, and
  signed result transport.
- Per-run identities, keys, certificates, sessions, message IDs, dispatch IDs,
  and Redis key prefixes are random. Cleanup removes only exact owned keys.

## Run

Start an isolated Redis container (or provide an equivalent dedicated URL):

```sh
docker run --rm -p 63319:6379 \
  redis:7.4.2-alpine@sha256:02419de7eddf55aa5bcf49efb74e88fa8d931b4d77c07eff8a6b2144472b6952
```

Linux/CI:

```sh
CAP_PRODUCTION_REDIS_URL=redis://127.0.0.1:63319/15 \
  ./tests/capproduction/run.sh
```

Windows PowerShell:

```powershell
$env:CAP_PRODUCTION_REDIS_URL = 'redis://127.0.0.1:63319/15'
./tests/capproduction/run.ps1
```

Both runners print the exact CAP module pin, execute the real-transport suite
three times, and then run the failure/rotation/snapshot matrix three times.
They fail if any test reports `SKIP`.
