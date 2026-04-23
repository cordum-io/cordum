# Quickstart environment contract

This document describes exactly how `tools/scripts/quickstart.sh` shares
credentials with Docker Compose and the API gateway. It is intentionally
operational: use it when a local stack starts but `/api/v1/status` rejects the
key you expected to work.

## Precedence (verified against code as of this PR)

For variables tracked by quickstart, the shipped precedence is:

1. **Shell export** — a non-empty variable already exported in the calling shell
   wins. For `CORDUM_API_KEY` and `REDIS_PASSWORD`, quickstart also writes that
   value to `.env` so a later Compose run without the shell export stays in
   agreement.
2. **`.env`** — if the shell did not set the variable, quickstart loads the
   first matching `KEY=value` line from `.env`.
3. **Auto-gen-on-first-run** — only `CORDUM_API_KEY` and `REDIS_PASSWORD` are
   generated when still empty; generated values are written back to `.env`.

Quickstart logs the resolved source before Compose starts:

```text
[quickstart] env.source key=CORDUM_API_KEY source=env-file file=.env
[quickstart] env.source key=REDIS_PASSWORD source=auto-generated file=.env
[quickstart] env.source key=CORDUM_ADMIN_PASSWORD source=shell-override file=-
```

The log never includes secret values.

## Variables that auto-persist to `.env`

| Variable | Default behavior | Rotate safely |
| --- | --- | --- |
| `CORDUM_API_KEY` | Loaded from shell/`.env`; shell-provided values are persisted; generated and persisted if missing. | Edit `.env`, then run `./tools/scripts/quickstart.sh --clean` or `docker compose up -d --force-recreate`. |
| `REDIS_PASSWORD` | Loaded from shell/`.env`; shell-provided values are persisted; generated and persisted if missing. | Edit `.env`, then recreate Redis and dependent services with `--clean` or `--force-recreate`. |
| `CORDUM_ADMIN_PASSWORD` | Loaded from shell/`.env`; not generated. Required only when `CORDUM_USER_AUTH_ENABLED=true`. | Edit `.env`, then recreate `api-gateway` so the container receives the new value. |

Other tracked variables (`CORDUM_ADMIN_EMAIL`, `CORDUM_USER_AUTH_ENABLED`,
`CORDUM_TENANT_ID`, `CORDUM_ORG_ID`) follow the same shell-over-`.env`
precedence but are not generated.

## Rotating secrets

Recommended rotation path:

```bash
$EDITOR .env
./tools/scripts/quickstart.sh --clean
```

Equivalent manual path:

```bash
$EDITOR .env
docker compose up -d --force-recreate
```

Do **not** rely on a plain `docker compose up -d` after editing `.env`.
Compose does not rewrite environment variables inside already-created
containers, so a stale `api-gateway` container can keep the previous
`CORDUM_API_KEY` while your shell and `.env` contain the new value.

## Divergence detector

Before `docker compose up`, quickstart checks already-existing project
containers. For each tracked variable with a current shell value, it probes
service containers with:

```bash
MSYS_NO_PATHCONV=1 docker compose exec -T <service> printenv <VAR>
```

If a container value differs, quickstart logs only metadata:

```text
[quickstart] env.divergence key=CORDUM_API_KEY container=api-gateway action=abort
```

Behavior:

- No existing containers: the detector skips.
- Existing containers and no `--clean`: quickstart aborts with exit code `2`
  and prints a hint to run with `--clean` or `docker compose up -d
  --force-recreate`.
- `--clean`: quickstart warns, then removes stale containers before starting
  fresh ones.
- `--strict` or `CORDUM_QUICKSTART_STRICT=1`: any detected divergence aborts.

Secret values are never printed.

## Debugging

Use one grep for shell and container-side source metadata:

```bash
./tools/scripts/quickstart.sh --skip-smoke --skip-doctor 2>&1 | grep 'env.source'
docker compose logs api-gateway --no-color | grep 'auth.api_key.source'
```

Expected default Compose gateway log:

```text
auth.api_key.source source=compose source_file=.env
```

Outside Compose, the gateway may log `source=env` for `CORDUM_API_KEY` /
`CORDUM_API_KEYS`, or `source=file` when `CORDUM_API_KEYS_PATH` is used.

If the quickstart banner says `source=env-file` but the gateway logs an old
source or the divergence detector aborts, recreate the container:

```bash
./tools/scripts/quickstart.sh --clean
```

## Shell caveats

On Git Bash / MSYS, manual `docker exec` commands can rewrite POSIX-looking
paths unless `MSYS_NO_PATHCONV=1` is set. Quickstart sets this internally for
its divergence probes. Operators only need it for manual debugging commands,
for example:

```bash
MSYS_NO_PATHCONV=1 docker compose exec -T api-gateway printenv CORDUM_API_KEY
```
