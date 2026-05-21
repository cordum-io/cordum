# Cordum Infra Docs

Infrastructure investigations, operational runbooks, and fleet-lifecycle reports.

| Doc | Date | Status | Summary |
|---|---|---|---|
| [worker-session-lifecycle-investigation.md](worker-session-lifecycle-investigation.md) | 2026-05-18 | Investigation complete | Workers auto-deregister at the daemon's 10-min heartbeat ceiling when a single tool call (typically `go test -count=3`, `go mod vendor` for first-time K8s deps, or a long exploration loop) blocks the wrapper for >10 min. State-machine rollback demotes the active step COMPLETED → IN_PROGRESS but does NOT roll back git/workspace state, producing the "uncommitted WIP across the fleet" symptom. Short-term: split long commands + interleave `moe.chat_send`. Structural: per-task-tag heartbeat ceiling (recommended) or wrapper keep-alive tick. Daemon-source not in this repo — Phase 7 follow-up filed for the daemon owner. |
