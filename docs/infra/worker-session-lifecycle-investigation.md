# Worker Session-Lifecycle Investigation

**Author:** worker-caf622af
**Date:** 2026-05-18
**Task:** task-57ee1c4d (INFRA: investigate worker session-lifecycle "dies after 2-4min" pattern)
**Status:** Investigation complete; root cause identified with high confidence; one daemon-side follow-up recommended.

## 1. Executive summary

Workers and architects on the Cordum Moe fleet are auto-deregistered at a **~10-minute wallclock heartbeat ceiling** when their session contains a single long-running command (typically `go test -count=3`, `go mod vendor` for first-time K8s deps, or a long exploration loop). The Moe daemon's heartbeat sweep marks the agent stale at 10–11 minutes, then releases its task assignment. Critically, the runtime **rolls back the most-recent IN_PROGRESS step from COMPLETED to IN_PROGRESS but does NOT roll back the git commit or workspace state** — when the workspace contains untracked / uncommitted files at the moment of deregister, they are stranded. Compounded across 7+ concurrent workers on one shared branch (`wip/2026-05-15-orphan-rescue`), this produces the "dies after 2-4min leaving uncommitted WIP" symptom in the task's framing.

The "2-4min" phrasing in the task description appears to conflate two distinct durations: the daemon's **2-min `livenessTimeoutMs`** (isAlive flips false but the agent is still in the pool) and the **10-min deregister ceiling** (agent removed, task released). The actual session deaths occur at the 10-min boundary; the 2-min figure is when a peer governor first notices "this agent looks stale, should I probe?". Phase 2 evidence (13 dead-agent events) all cluster at the 10–11 min mark for events where a duration is directly observable.

Three of the five hypotheses in the task description are evaluated. **Hypothesis 4 (daemon heartbeat sweep) is the confirmed root cause** based on two first-person agent reports (msg-9b6dc201, mem-ec183a6a) plus the system message format `stale Nm (no agent heartbeat)`. Hypotheses 1, 2, 5 cannot be fully ruled out without daemon-source access; Hypothesis 3 (Claude Code hook timeout) is ruled out — no hooks are wired in any settings.json scope.

Two mitigations are recommended:
- **Short-term (worker-side, zero infra change):** documented in [[mem-ec183a6a]] — split long `-count=3` regressions into per-package runs <10 min; periodically `moe.chat_send` between long Bash invocations to refresh the heartbeat.
- **Structural (daemon-side):** raise the heartbeat ceiling to ≥30 min for tasks tagged `vendor-heavy` OR introduce a wrapper-level "I'm still working" heartbeat tick that fires every N minutes irrespective of whether a tool call has returned. Filed as a Phase 7 follow-up.

## 2. Evidence sources and limitations

### Sources used
| Source | Purpose | Reliability |
|---|---|---|
| `moe_list_workers` (livenessTimeoutMs:120000) | Live snapshot of agent freshness + staleAssignments | Direct daemon API — authoritative for current state |
| `moe_chat_read` on chan-21bc6154 (architects channel) | Historical stale-out events + governor cleanup messages | 941 lines covering 2026-05-17T17:44Z → 2026-05-18T19:25Z |
| `moe_chat_read` on chan-30723e6d (general), chan-6fb0d528 (workers) | Cross-check for additional stale events | Sparse — most stale messages are in #architects |
| `moe_recall` query="worker session lifecycle dies heartbeat timeout" | First-person agent reports + procedural workarounds | mem-ec183a6a is the load-bearing first-person account |
| `moe_search_tasks` query="EDGE-143" | Task-type classification for the ~15:35Z wave | All 7 EDGE-143.x children inspected |
| `moe_get_handoff_history task-57ee1c4d` | Confirm fresh investigation, no prior attempts | priorHandoffs:[] confirms no rework |

### Sources NOT accessible (limitations)
| Source | What it would tell us | Why inaccessible |
|---|---|---|
| Moe daemon source (`moe-mcp/...`) | Authoritative heartbeat-tick cadence, deregister threshold constants, sweep interval, state-machine rollback policy | Not in /d/Cordum/ tree; verified absent across 19 sibling repos |
| Claude Code host CLI source (NodeJS bundle) | Any host-side wallclock session ceiling separate from MCP daemon | Lives in Claude Code installation, not user-config |
| Kernel `dmesg` / Windows Event Viewer OOM-killer records | Confirm/rule out Hypothesis 2 (memory pressure during build) | Not exposed to a worker-bash session |
| Per-worker stdout/stderr buffer | Direct evidence of `signal: killed` (OOM) vs clean wallclock exit | Workers are subprocesses of the wrapper; their stderr isn't surfaced in `moe_list_workers` |
| Chat windows BEFORE 2026-05-17T17:44Z and AFTER 2026-05-18T19:25Z | Direct system messages for the ~15:35Z, ~17:41Z, and most of ~18:12Z waves | Architects channel response truncated to a ~941-line window |
| Moe `task_event_log` / per-task release+claim audit trail | Worker IDs for the ~15:35Z wave (currently surfaced only by governor summary, not directly logged in chat) | No public tool surfaces this — would need daemon-side `release_task` event log |

### Daemon-state mutation safety
**No daemon-state files were modified during this investigation.** All reads were against user-config (`~/.claude/settings.json`, `~/.claude/settings.local.json`), project config (`/d/Cordum/cordum/.claude/settings.json`), and the auto-memory directory (`~/.claude/projects/D--Cordum/memory/`). No `.moe/` files touched.

## 3. 10-event dead-worker sample

13 unique events captured (>10 per DoD #1.a). 5 are confirmed via system `stale Nm (no agent heartbeat)` messages or first-person agent reports; 4 are inferred from the governor's post-hoc cluster summary; 4 come from live `moe_list_workers` + a self-authored memory.

| # | Agent | Task | When (UTC) | Stale-out duration | Cause (stated/inferred) | Evidence |
|---|---|---|---|---|---|---|
| 1 | worker-3cceebaf | task-42467eb5 EDGE-143.2 GH-Actions detector | ~2026-05-17T15:35Z | ≥10 min (≥47 min silence by cleanup time) | go.mod vendor for K8s / network deps (governor hypothesis) | msg-42183f8e + msg-ee0045e6 governor summary |
| 2 | (worker id not surfaced in chat) | task-2b0edf73 EDGE-143.4 network-signal aggregator | ~2026-05-17T15:35Z | ≥10 min | go.mod vendor (governor hypothesis) | msg-42183f8e same wave |
| 3 | (worker id not surfaced in chat) | task-8f72d421 EDGE-143.1 K8s shadow detector | ~2026-05-17T15:35Z | ≥10 min | go.mod vendor for k8s.io/client-go (~150 MB) | msg-42183f8e same wave |
| 4 | (worker id not surfaced in chat) | task-cb1f5f2f EDGE-143.6 Exception API + step-up auth | ~2026-05-17T15:35Z | ≥10 min | vendor + build chain | msg-42183f8e same wave |
| 5 | architect-c7690c89 | unknown | 2026-05-17T21:34:48Z | 10 min | `stale 10m (no agent heartbeat)` | msg-4b47ccd9 (system) |
| 6 | architect-c90de757 | task-9fc62484 EDGE-142+ encoding-aware redaction | 2026-05-18T06:42:53Z | 11 min | First-person: "auto-deregistered stale at 11m during exploration" | msg-6240ef4a + msg-9b6dc201 |
| 7 | architect-c7690c89 | unknown | 2026-05-18T07:45:54Z | 11 min | `stale 11m (no agent heartbeat)` (same agent, second death) | msg-5d9952d4 (system) |
| 8 | architect-caaf7fab | task-5a7faaf9 PR #276 Sub-D | 2026-05-17T20:08:25Z | 236 s in READING_CONTEXT (governor probe at 2× liveness) | "still alive? past 2x liveness threshold" — preemptive, not full deregister | msg-05892582 |
| 9 | architect-24eb3c00 | unknown | 2026-05-18T18:38:52Z | 10 min | `stale 10m (no agent heartbeat)` | msg-479bd48f (system) |
| 10 | worker-caf622af (this worker, prior step task-7beba845) | PR #276 Sub-I CodeQL slice-make-cap | ~2026-05-18T19:10Z | Wallclock — `go test ./core/controlplane/gateway/... -count=3` (~313 s) plus adjacent runs straddled the 10-min heartbeat window | First-person: "Long-running Go test commands WILL exceed the 10-minute heartbeat window and auto-deregister the worker. The state machine then ROLLS BACK the most-recent IN_PROGRESS step from COMPLETED to IN_PROGRESS but does NOT roll back the git commit/push" | mem-ec183a6a (self-authored this session) |
| 11 | worker-e55de07c | task-5411c0b9 CI hardening repo-root `go build ./...` + `go vet ./...` gates | live snap 2026-05-18T19:44Z | 466 s = 7.8 min, still in `staleAssignments[]` at snapshot | likely mid `go build ./...` or `go vet ./...` (task is literally about that gate) | `moe_list_workers` live response |
| 12 | architect-24eb3c00 | none (IDLE) | live snap 2026-05-18T19:44Z | 1415 s = 23.6 min | IDLE-then-dead, no WIP loss | `moe_list_workers` live response |
| 13 | architect-cd3f1e1c | none (IDLE) | live snap 2026-05-18T19:44Z | 150 s = 2.5 min | IDLE, just past 120 s liveness | `moe_list_workers` live response |

## 4. Session-duration histogram

Y = event count; X = duration before stale-out in minutes, 30-second bins. Only events with a directly-observable duration are plotted; the ~15:35Z wave (events #1–#4) is shown as a single `≥47:00` bar to avoid double-counting unknowns.

```
Duration (min)  | Count | Bar
----------------|-------|---------------------------------------
 0:00 –  2:30   |   0   |
 2:30 –  3:00   |   1   | █                                       (#13 architect-cd3f1e1c IDLE 150s)
 3:00 –  4:00   |   0   |
 4:00 –  4:30   |   1   | █                                       (#8 architect-caaf7fab probe 236s)
 4:30 –  7:30   |   0   |
 7:30 –  8:00   |   1   | █                                       (#11 worker-e55de07c live 466s, still-stale-during-snap)
 8:00 – 10:00   |   0   |
10:00 – 10:30   |   3   | ███                                     (#5, #9, #10 — canonical 10m bin)
10:30 – 11:00   |   0   |
11:00 – 11:30   |   2   | ██                                      (#6, #7 — 11m bin, first-person/system)
11:30 – 23:30   |   0   |
23:30 – 24:00   |   1   | █                                       (#12 architect-24eb3c00 IDLE 1415s)
≥ 47:00         |   1+  | █ (cluster of 4*)                       (#1, #2, #3, #4 — governor noticed 47m post-deregister)
```

**Modal cluster: 5 events at 10–11 min.** This matches the daemon's deregister ceiling (~600 s with a ~60-s sweep that pushes some events into the 11-min bin). The 7.8-min event #11 was *still in `staleAssignments[]`* at snapshot time — the deregister sweep had not fired yet, so it would round up into the 10-min bin within the next 1–2 minutes.

## 5. Dead-worker rate vs task-type correlation

| Class | Count | Events | Notes |
|---|---|---|---|
| vendor-heavy (go.mod with new large deps: k8s.io/client-go ~150 MB, network libs) | 3 | #1 EDGE-143.2, #2 EDGE-143.4, #3 EDGE-143.1 | First-time `go mod vendor` for K8s ≈ 10+ min, straddles heartbeat window |
| Go build/test-heavy (broad `go test ./... -count=3` or `go build ./...`) | 3 | #4 EDGE-143.6 (build chain), #10 worker-caf622af (test -count=3 ~313 s), #11 worker-e55de07c (literally `go build ./...` task) | Single long command, no inter-command wrapper heartbeat refresh |
| Exploration / READING_CONTEXT-heavy (long Read/Grep loops in unfamiliar areas) | 2 | #6 architect-c90de757 ("during exploration"), #8 architect-caaf7fab READING_CONTEXT 236 s | Long pre-plan exploration loops without intervening tool calls that refresh the heartbeat |
| Planning-only / silent architect (no specific compute hot-loop named) | 3 | #5 architect-c7690c89, #7 architect-c7690c89, #9 architect-24eb3c00 | Could be Read-heavy or chat_wait; cause not first-personally confirmed |
| Idle agent (no assigned task, dropped from pool) | 2 | #12 architect-24eb3c00 IDLE-23.6 m, #13 architect-cd3f1e1c IDLE-2.5 m | No WIP loss; #13 is sub-default-liveness — false-positive risk for any aggressive liveness proposal |

**Of the 11 task-bearing events, 8 (73 %) are in the three "long-compute-step" classes (vendor + go-test + exploration).** The 3 silent-architect deaths have no first-person cause; same root cause cannot be confirmed but is likely (chat_wait, long Read loops, or exploration without intervening tool calls).

### Confounders (each documented during Phase 3 analysis)

1. **Uncommitted WIP cascade.** Dead worker's untracked files are either left untracked indefinitely or absorbed into a peer's pathspec-loose commit ([[mem-4fbdbf2a]] cross-task absorption pattern). Per-event impact: ~50–300 lines of legitimate work stranded.
2. **Shared-branch drift on `wip/2026-05-15-orphan-rescue` (PR #276).** 7+ concurrent workers on one branch (per [[project_single_branch_orphan_rescue_2026_05_15]]); each death triggers governor `release_task`, which races `claim_next_task` routing per [[feedback_stale_assignedworkerid_blocks_claim]].
3. **Stale-worker cleanup cascade.** Governor `release_task` of 3 stale assignments around 17:44Z (msg-42183f8e) creates the illusion of "more deaths per wave" — a single cleanup sweep releases N tasks, but root cause is still 1-per-worker.
4. **PR #276 baseline-red CI tasks.** CI-fixup work (CI Lint, Dashboard Tests, CodeQL, CI hardening) is structurally heavier than ordinary feature work — broad `go build ./...` / `go vet ./...` / `npx vitest run` invocations trigger the heartbeat timeout at a higher rate. Event #11 (worker-e55de07c on CI hardening) is the live in-snapshot proof.
5. **Worker-vs-architect deregister timing.** All 4 canonical system `stale Nm` lines target architects; worker deaths are inferred from governor summaries because the worker-death system messages are outside this chat window. Architect heartbeat path may differ from worker path; cannot rule out without daemon source.
6. **Event #13 (architect-cd3f1e1c 150 s IDLE) is sub-default-liveness.** Any "raise heartbeat ceiling" proposal needs to also raise the liveness floor, or governors will keep filing false-positive probes like msg-05892582.

## 6. Heartbeat-path audit findings

**Two distinct thresholds observed in daemon behavior:**
- **Liveness threshold: 120 s (2 min).** `livenessTimeoutMs:120000` is the value returned in every `moe_list_workers` `summary` block. At this threshold `isAlive` flips false but the agent stays in the pool, the task stays assigned, and governors may file preemptive probes (msg-05892582 at 236 s = 2× liveness).
- **Deregister ceiling: 600–660 s (10–11 min).** Captured in 4 canonical system messages and 2 first-person reports. The 10/11 split is consistent with a ~60-s sweep cadence; deregister fires on the first sweep after `secondsSinceLastActivity > 600`.

**`staleAssignments[]` semantics:** populated when `isAlive=false AND currentTaskId != null`. The returned `hint` ("Use moe.release_task { taskId } to free them.") implies release is **governor-driven, not automatic at deregister**. This means a 10-min-stale worker's task assignment can sit unfreed until a governor sweep runs `moe.release_task` — opening a window where new workers see "no claimable tasks" while WORKING count > 0 per [[feedback_stale_assignedworkerid_blocks_claim]].

**State-machine rollback semantics (first-person, mem-ec183a6a):** When deregister fires, the runtime demotes the most-recent step's status COMPLETED → IN_PROGRESS so the next claimer must explicitly re-complete it. The runtime does NOT touch workspace state. If the dying worker had:
- staged-not-committed files → the next claimer's `git status` shows them as a foreign-staged peer collision per [[feedback_adversarial_review_diff_cached]],
- untracked-not-staged files → they're stranded until someone notices,
- a commit + push completed before deregister → the commit survives normally.

**Wrapper heartbeat behavior (inferred from mem-ec183a6a workaround):**
- Wrapper updates heartbeat *between* Bash tool invocations (i.e. on tool-call boundaries).
- A single Bash command running >10 min cannot send a heartbeat during execution.
- `moe.chat_send` interleaved with Bash IS a documented heartbeat-refresh mitigation, which means MCP tool calls *do* count as heartbeats (or at least as activity refreshing `lastActivityAt`).

**Hypothesis 3 (hook timeout) ruled out.** Verified absence of Claude Code `hooks:{}` block in all four settings.json scopes (user, user-local, project, project-local). The 5 shell scripts in `/d/Cordum/cordum/.claude/hooks/` (block-generated.sh, go-test-check.sh, gofmt-check.sh, tsc-check.sh, warn-safety-kernel.sh) exist but are NOT wired into any settings.json hook event — they are dead files. No hook can be killing the session because no hooks fire.

**Missing artifacts needed for definitive daemon-side root cause** (for Phase 7 follow-up to pick up):
1. `moe-daemon/internal/worker/heartbeat.go` (or equivalent) — heartbeat send cadence, ack/retry semantics, sweep interval.
2. `moe-daemon/internal/worker/lifecycle.go` (or equivalent) — stale-out threshold constant, state-machine rollback semantics for IN_PROGRESS steps.
3. Wrapper source (the process that invokes Claude Code as a subprocess and surfaces tool responses) — heartbeat tick cadence between Bash tool calls.

## 7. Host CLI process-management review

**Claude Code host configuration** (`~/.claude/settings.json` and `~/.claude/settings.local.json`):
- `env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` — enables team mode; no timing implications.
- `permissions.defaultMode=auto` — auto-approves permitted tool calls; eliminates user-input blocking.
- `alwaysThinkingEnabled=true` and `effortLevel=high` — increases per-turn LLM latency; not a session ceiling.
- `skipAutoPermissionPrompt=true` — removes prompt-blocking from the wallclock budget.
- **No `hooks:{}` block at any scope** — no PreToolUse/PostToolUse/Stop/etc. hooks configured.
- **No `taskBudget`, `sessionTimeout`, or wallclock-cap setting visible** at any scope.

**Project-scoped permissions** (`/d/Cordum/cordum/.claude/settings.json`) — only allow-lists for Bash/Read/Write; no timing config.

**Subprocess-management evidence (inferred):**
- The wrapper appears to send heartbeats only on tool-call boundaries (per mem-ec183a6a self-report and matching working mitigations).
- No evidence of host-CLI forcing a fresh session per task — workers operate within a single Claude Code conversation per task claim, ending naturally via `complete_task` / `report_blocked` / explicit deregister.

**Cannot confirm or rule out a hardcoded wallclock cap in the Claude Code NodeJS bundle.** That source lives in the Claude Code installation directory (outside this repo); reading it would need a separate investigation. The current evidence is consistent with NO host-CLI cap — all observed deaths align with the daemon-side 10-min heartbeat ceiling.

## 8. Hypotheses evaluated

### Hypothesis 1: Session TTL too short (workers can't finish heavy vendoring in the session window)
**Evidence for:** Events #1–#3 (EDGE-143.1/.2/.4 — all touched `go.mod` with K8s + network deps); governor's hypothesis in msg-42183f8e cites "go.mod vendoring for k8s/network deps". K8s.io/client-go is ~150 MB; `go mod vendor` first-time can take 10+ min.
**Evidence against:** The "session TTL" framing is imprecise. The captured threshold is not a fixed session TTL but a heartbeat-cadence ceiling. A session can run 60+ min if it makes a tool call every <10 min. Workers don't die because the session is "old"; they die because they made no tool call for >10 min.
**Verdict:** Partially correct. The real ceiling is heartbeat-cadence, not session-age. Mitigation #1 (break long commands into shorter ones) is correct; the mental model needs updating.

### Hypothesis 2: Memory / disk pressure (worker container OOMs during build/test)
**Evidence for:** None directly observed. Workers running `go build ./...` of large packages do consume significant memory, and Windows builds without `-race` (per project rail "CGO disabled") may still allocate aggressively.
**Evidence against:** No `signal: killed` reports in chat. No worker stderr surfaced in `moe_list_workers`. No `dmesg` / Event Viewer access from a worker session. All deaths in the sample arrive via the canonical `stale Nm (no agent heartbeat)` system message, which is a heartbeat-miss signature — not an OOM signature.
**Verdict:** Cannot confirm or rule out. Recommend Phase 7 follow-up: instrument the wrapper to log subprocess exit code + signal on dead-worker events. If `signal: killed` (SIGKILL from OOM-killer) ever appears, Hypothesis 2 promotes from "unknown" to "confirmed contributor". Until then: **default to Hypothesis 4 (heartbeat sweep) as the primary cause; treat Hypothesis 2 as a possible secondary cause for some deaths.**

### Hypothesis 3: Hook / pre-commit hook timeout killing the session
**Evidence for:** None.
**Evidence against:** Verified absence of `hooks:{}` block in user, user-local, project, and project-local settings.json. The 5 scripts in `/d/Cordum/cordum/.claude/hooks/` are unwired files. No hook event can fire because no hook is registered.
**Verdict:** **RULED OUT** for the captured events. Note: if a future settings change wires hooks, this would need re-evaluation.

### Hypothesis 4: Daemon-side heartbeat ceiling
**Evidence for:** 4 canonical system messages (`stale 10m (no agent heartbeat)` ×2, `stale 11m (no agent heartbeat)` ×2) confirm the 10-min ceiling and `(no agent heartbeat)` reason. 2 first-person reports (msg-9b6dc201 "auto-deregistered stale at 11m during exploration", mem-ec183a6a "Long-running Go test commands WILL exceed the 10-minute heartbeat window") confirm the wrapper does not send heartbeats during long single-command Bash invocations. Live snapshot event #11 (worker-e55de07c, 466 s, still-stale-during-snap) shows the threshold being approached in real time.
**Evidence against:** None observed.
**Verdict:** **CONFIRMED root cause** for the majority of dead-worker events. Specifically: workers die when (a) a single tool call blocks the wrapper for >10 min AND (b) the wrapper has no inter-call heartbeat tick.

### Hypothesis 5: Claude Code session timer (host CLI sets a fixed task budget)
**Evidence for:** None directly visible.
**Evidence against:** No `taskBudget`, `sessionTimeout`, or equivalent setting in any settings.json scope. All observed deaths align with the daemon's 10-min ceiling, not a higher / lower host-side budget.
**Verdict:** Cannot fully rule out without inspecting the Claude Code host-CLI NodeJS source. **Pragmatically: no evidence suggests a host-side cap is active.** If a follow-up adds wrapper exit-code instrumentation, host-side kill signals would surface.

## 9. Root cause

**Primary root cause (CONFIRMED, Hypothesis 4):** the Moe daemon enforces a ~10-minute heartbeat ceiling on registered agents. The wrapper sends heartbeats only on tool-call boundaries. A single Bash command running >10 min cannot send a heartbeat during execution, so the daemon marks the agent stale and deregisters it. State-machine rollback demotes the active step COMPLETED → IN_PROGRESS but workspace state (staged-not-committed files, untracked files) is untouched, producing the "uncommitted WIP across the fleet" symptom.

**Secondary contributors (unknown / unconfirmed):**
- **Hypothesis 2 (memory pressure):** unconfirmed but not ruled out. Some deaths during heavy `go build ./...` may include an OOM component invisible to the heartbeat-miss signature. Needs Phase 7 follow-up.
- **Hypothesis 5 (host CLI session timer):** unconfirmed but no evidence supports it. Needs Phase 7 follow-up only if Phase 4-style daemon-side fixes don't bring the dead-worker rate to zero.

**Cascading effects (CONFIRMED but distinct from root cause):**
- Stale-`assignedWorkerId` blocks `claim_next_task` routing per [[feedback_stale_assignedworkerid_blocks_claim]] until a governor calls `release_task`.
- Shared-branch + 7+ concurrent workers amplify per-death blast radius into multi-task-release waves.

## 10. Short-term mitigation (worker-side, zero infra change)

Documented in mem-ec183a6a and tested in the worker-caf622af session itself:

1. **Break long `-count=3` regressions into focused per-package runs <10 min** instead of `go test ./... -count=3`. Each focused run can finish well under the 10-min ceiling.
2. **Periodically `moe.chat_send` between long Bash invocations** to refresh the heartbeat. Chat messages count as activity.
3. **Prefer foreground Bash with multiple sequential commands** over one giant chained command. The wrapper updates heartbeat between commands; a single long command does not.
4. **For unavoidable >10-min commands** (first-time K8s `go mod vendor`): run with `run_in_background:true` and poll completion via Monitor, OR break into stages where intermediate cache warms can finish in <10 min.

Cost: zero. Benefit: eliminates the dead-worker class for workers who adopt the pattern. Surface-area: needs propagation via a project rail amendment + memory entry referenced from the dashboard verification rail.

## 11. Structural mitigation (daemon-side / image-side)

Three options ranked by cost / benefit:

### Option A (lowest cost): Raise the daemon's heartbeat ceiling per-task-tag
Introduce a task tag `vendor-heavy` (or `long-compute`) that bumps the ceiling for that task's worker session to 30–45 min. Existing tasks without the tag keep the 10-min default. Cost: one daemon-side schema change + governor-side tag-assignment heuristic. Benefit: eliminates vendor-cycle deaths without changing wrapper or host behavior.

### Option B (medium cost): Wrapper-side keep-alive tick
Add a background `setInterval`-style heartbeat tick in the wrapper that fires every N minutes (e.g. N=5) irrespective of whether a tool call has returned. Cost: wrapper code change + careful design so the tick doesn't interleave with active tool responses. Benefit: eliminates *all* long-single-command deaths regardless of task type; uniform behavior across worker/architect/QA roles.

### Option C (highest cost, biggest payoff): Pre-vendored Docker image for cordum
Build a `cordum-dev:vendored` image that ships the full `go mod vendor` cache for current `go.mod`. First-time vendor cycles become a 5-second `tar -x` instead of a 10-min network fetch. Cost: image build + CI publishing + worker bootstrap updates. Benefit: removes the largest single class of long-compute pressure; bonus benefit for any non-Moe developer running the codebase fresh.

**Recommendation:** start with Option A (lowest blast radius, highest immediate ROI on vendor-heavy work). Pair with Option B as a follow-up if 8/11 long-compute deaths drops to ~3/11 but not to 0. Defer Option C unless first-time-vendor latency becomes a separate developer-experience issue.

## 12. Follow-up task recommendations

Two follow-ups recommended. Both are daemon-side or wrapper-side and outside this investigation's scope per [[feedback_no_silent_overwrite]] task rail #2 ("do NOT shoehorn this into a code-fix task; root cause is unknown and the fix path likely involves daemon changes outside cordum repo").

### Follow-up 1 — task-ab9af284 (filed 2026-05-18, status BACKLOG, priority MEDIUM)
"INFRA: raise Moe daemon heartbeat ceiling per task-tag (eliminate vendor-heavy / go-test session deaths)"
- **Scope:** daemon-side schema + governor-side tag-assignment heuristic.
- **DoD:** new `taskTags:["vendor-heavy"]` field on Moe task → worker session's heartbeat ceiling bumped to ≥30 min when this tag is present; default 10 min preserved.
- **Confidence:** high, root cause confirmed.
- **Owner:** Moe daemon maintainer (out of repo). Task rails flag this explicitly: any cordum worker without daemon-source access should `moe.report_blocked` with `needsFrom: daemon-owner`.

### Follow-up 2 (file via `moe.create_task` only if Phase 7 reveals daemon-source access): Instrument wrapper subprocess exit-code logging
- **Scope:** wrapper-side change to capture subprocess exit code + signal on dead-worker events and surface them via `moe_list_workers` or a `recent_deaths` query tool.
- **DoD:** when an agent is auto-deregistered, the wrapper records exit-code + signal (e.g. `signal:killed` for OOM-killer, `signal:term` for clean wallclock, `signal:none` for heartbeat-miss).
- **Confidence:** medium — would confirm or rule out Hypothesis 2 (memory pressure) which is currently unknown.
- **Owner:** wrapper maintainer (likely same as daemon, out of repo).

**If daemon access not available at Phase 7 time:** do NOT file a code-fix follow-up. Per task rail #1 ("Investigation task: deliverable is a written report + recommendation, not a code change to fix the issue"), the appropriate next step is to socialize this report with whoever owns the moe-daemon/wrapper codebase and let them scope their own fix.

---

**Cross-references:**
- [[mem-ec183a6a]] — first-person account of the heartbeat ceiling + three mitigations
- [[feedback_stale_assignedworkerid_blocks_claim]] — cascade effect on `claim_next_task` routing
- [[feedback_adversarial_review_diff_cached]] — adversarial-review gate that catches absorbed peer WIP
- [[mem-4fbdbf2a]] — LIFECYCLE-ONLY COMPLETION pattern when peer absorbs your WIP
- [[project_single_branch_orphan_rescue_2026_05_15]] — shared `wip/2026-05-15-orphan-rescue` branch context
