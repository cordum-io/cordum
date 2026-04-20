# Quickstart Cold Test — Tester Checklist

> **This doc is filled in by a human tester, not by an AI worker.**
> See task-75273093 precedent: an AI worker already has the full
> codebase in context and cannot generate a meaningful "first-time
> user" signal. The cold test only counts if a human without prior
> Cordum exposure follows `docs/quickstart.md` verbatim and records
> what happens.

A cold test is the last gate on any quickstart rewrite. The goal is
to catch documentation drift, environment assumptions baked into the
author's shell, and copy-paste breakage that none of the automated
CI jobs can see. **Do not run this against a staging machine you
already use for Cordum development** — the point is a fresh box.

## Tester background required

Answer "yes" to all before starting:

- [ ] **No prior Cordum exposure** — you have never installed, built,
      or operated a Cordum deploy on this machine.
- [ ] **Docker installed and healthy** — `docker version` succeeds,
      Docker Desktop has at least 4 GB RAM allocated, and `docker
      compose version` prints v2.x.
- [ ] **Basic shell proficiency** — you can follow a numbered list of
      `bash`/`curl`/`jq` commands and paste output into this file.
- [ ] **Network access** — you can reach `github.com`,
      `ghcr.io`, and `registry-1.docker.io` from this shell.
- [ ] **You will not skip a step** — even if you think you know what
      the next command does, run it verbatim and record the output.

If any of the above is "no", stop. Either fix the gap or find a
different tester.

## Cold-test checklist

For each step: run the command exactly as printed in the current
`docs/quickstart.md`, record **elapsed seconds**, **exit code**, and
a one-line snippet of the most relevant output. If the step deviates
from the doc in any way, capture the delta in the **Notes** column.

| # | Step (from docs/quickstart.md) | Elapsed (s) | Exit | Observed output (one line) | Notes / drift |
|---|--------------------------------|-------------|------|-----------------------------|----------------|
| 1 | Clone `github.com/cordum-io/cordum` and `cd` in | | | | |
| 2 | `export CORDUM_API_KEY="$(openssl rand -hex 32)"` + `export CORDUM_TENANT_ID=default` | | | | |
| 3 | `./tools/scripts/quickstart.sh` (wait for the green `[quickstart] stack is up` banner) | | | | |
| 4 | `curl -sS ... /api/v1/status ...` returns a JSON with `nats.connected: true`, `redis.ok: true` | | | | |
| 5 | Create the sample workflow via `POST /api/v1/workflows` with the `approve` step | | | | |
| 6 | Start a run via `POST /api/v1/workflows/{id}/runs` — capture `run_id` | | | | |
| 7 | Approve the gate job via `POST /api/v1/approvals/{job_id}/approve` | | | | |
| 8 | Poll `GET /api/v1/workflow-runs/{run_id}` until `status == "succeeded"` | | | | |
| 9 | Clean up via `DELETE /api/v1/workflow-runs/{run_id}` + `DELETE /api/v1/workflows/{id}` | | | | |
| 10 | Open `http://localhost:8082` in a browser — dashboard renders without errors | | | | |

### Common slip points to record in Notes

- Did any step require you to install a binary or package the doc
  did not mention (`jq`, `go`, `make`, a specific Python version)?
- Did `quickstart.sh` emit anything scary-looking on stderr even
  though it still exited 0?
- Did the dashboard show a 401 / empty state on first load?
- Did `/api/v1/status` return `nats.connected: false` for more than
  ~10 seconds after the banner?
- Did any command take long enough that you doubted progress? Record
  the doubt; the doc should add `(this can take N seconds)` hints.

## Sign-off

Fill this in when you finish — or when you give up. Both are useful.

- **Tester name:** ______________________________________________
- **Date tested (YYYY-MM-DD):** __________________________________
- **Host OS + version (e.g. macOS 14.5, Ubuntu 22.04, Windows 11 + MSYS):** ______________________________
- **Docker version:** ___________________________________________
- **Total elapsed, clone → succeeded run:** ______ minutes
- **Number of steps that required a fix or deviation:** ____ / 10
- **Final outcome:** `PASS` / `FAIL` / `PARTIAL` (circle one)

### Free-text feedback

> Minimum one paragraph. What surprised you? What did the doc say
> would happen vs what actually happened? If a tutorial friend
> sitting beside you would say "you should mention X" — write X.

```
(tester prose here)
```

### Recommended doc edits

For each distinct issue found, open a PR against `docs/quickstart.md`
or the mirrored copies per `RELEASING.md` §"Documentation sync".
List the resulting PRs here so the rail linking this cold test back
to the docs it verifies is visible.

| # | Issue | PR link |
|---|-------|---------|
| 1 | | |
| 2 | | |
| 3 | | |

## Submitting the artefact

When signed, commit this file back to the repo under a date-stamped
rename (e.g. `docs/cold-tests/2026-05-02-alice-macos.md`) so future
engineers can see historical test results. The template at
`docs/quickstart-cold-test.md` stays as the blank form — do not
overwrite it with your filled copy.

The PR description should link to the task that requested the cold
test (typically the task whose DoD requires "Tested by following
from scratch on clean environment") so the task reviewer can
approve on evidence.
