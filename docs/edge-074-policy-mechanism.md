# EDGE-074 — A policy mechanism better than regex

**Status:** PLANNED epic. Author: governance review following EDGE-073 (cross-tool secret-read bypass).
**Why:** EDGE-073 fixed `cat .env` walking past the `Read .env` deny, but its adversarial review proved the deeper problem: **string/regex classification cannot be both complete and precise.** It missed bare secret names (`cat credentials`) *and* false-positived legitimate assets (`openssl x509 -in server.crt`). You cannot tune your way out of that — a classifier reasons over a *representation* of an adversarial, dynamic action and is always guessing. This epic moves Edge governance off guessing.

> Moe import note: each `T#` below is a ready-to-create task (title = subject, **DoD** = definitionOfDone). The epic = this document. The Moe daemon was WS-only / no CLI when this was authored, so it was written here for clean import rather than hand-poked into the store.

## The principle

A "no" is only complete when it is enforced where it **cannot be expressed**, not where it is **recognized**. Three layers, each doing what it is good at:

```
  Action ─▶ (1) Canonicalize ─▶ (2) Analyzable policy ─▶ decision        ← detection / UX / audit
                                                          │
  Resource ◀───────────────── (3) Capability mediator ───┘              ← the actual guarantee
```

1. **Canonicalize, don't pattern-match.** Parse the action into structured facts (shell AST → `reads/writes/sinks`; SQL → `operation/tables`). Reason over structure, not substrings. Kills the EDGE-073 precision failures; dynamic/unparseable → `unresolved` → default-deny.
2. **Analyzable policy-as-code.** Express policy in an engine you can run *proofs* against (Cedar's validator, or a Rego property suite), so "no request this policy permits deletes the DB" is *checked*, not hoped. This is the determinism + "once we say no it can't happen" guarantee at the *reasoning* level.
3. **Capability mediation.** Hard "no"s are enforced at reference monitors on the resource (DB grants, FS broker, egress proxy). Completeness comes from non-bypassability; the classifier+policy become detection, UX ("here's why"), and audit. This is also why fail-closed is the wrong lever: a read-only DB role holds even when the policy engine is down — safe *and* available, no DoS-on-outage.

## Distinctions this epic makes precise

- **Determinism ≠ completeness.** The current classifier is already a deterministic pure function (sorted tags, no rand/time/map-order). But a deterministic classifier that doesn't tag `cat .env` deterministically *allows* it. Determinism buys "no random escape"; default-deny buys "no unforeseen escape"; capability mediation buys "no escape." You need all three.
- **Fail-closed is a degraded-case tradeoff, not a mechanism for a specific "no".** Relying on it means the policy engine being up is load-bearing for safety. Push the guarantee to the resource and the engine's fail mode becomes low-stakes/tunable.
- **Denylist vs allowlist.** The demo runs `default_decision: allow` (why `.env` slipped); production runs `default_decision: deny` + a narrow safe allowlist. Only the latter is a security posture.

## Tasks

### T0 — EDGE-073 polish: bare-name bypass + TLS-asset false positives *(immediate, independent)*
Stopgap hardening of the shipped fix in `core/edge/classifier.go`.
**DoD:** `cat credentials`/`cat password`/file-named-`token` flagged secret (parity with Read tool); explicit tested decision on `.crt/.key/.pem` public-cert FP (parity between Bash and Read); `TestClassifyBashSecretPathCrossToolClosure` extended; edge package green `-count=3`; no new benign FPs.

### T1 — Spike: canonical `ActionFacts` schema + structured parsers (shell, SQL)  *(blocks T2, T4)*
`ActionFacts{ argv, reads[], writes[], net_sinks[], deletes[], sql{operation,tables}, unresolved }`. Shell parser via `mvdan.cc/sh`; SQL parse to operation+tables; dynamic constructs → `unresolved` (→ default-deny).
**DoD:** schema + shell parser producing reads/writes/sinks correct on the EDGE-073 vector set (quoted, redirected, `<(...)`, `=`-inline); SQL operation+tables; `unresolved` for `$VAR`/eval/glob; table tests on EDGE-073 vectors + benign set; <2ms/cmd; parser-choice decision note.

### T2 — Spike: policy-as-code engine (Rego vs Cedar) + PROVABLE "no DB delete / no secret read"  *(blocked by T1; blocks T5)*
Spike OPA/Rego and Cedar over `ActionFacts`; express the two canonical "no"s; produce an automated completeness check (Cedar validate / Rego adversarial property suite) proving **no allow-path** to the prohibited action.
**DoD:** both engines spiked; both "no"s expressed; mechanical proof artifact; engine recommendation with tradeoffs (provability/ecosystem/perf/deps); determinism confirmation.

### T3 — Design: capability mediators (reference monitors) for 3 flagship "no"s  *(design; informs T5/T6)*
Per "no": enforcement point, the 3 reference-monitor properties (tamper-proof, always-invoked, verifiably small), and an invariant test that holds for all inputs. (1) no DB delete → read-only role / SQL proxy; (2) no secret exfil → runtime injection + read-only mount + egress allowlist; (3) no prod deploy → withheld deploy cred / approval gate.
**DoD:** design doc with per-"no" bypass analysis + invariant test sketch; explicit list of "no"s that *can't* be pushed to capability (content-style) and stay at the policy layer with a deliberate fail mode; `no → layer → invariant → owner` table.

### T4 — Integrate `ActionFacts` into the classifier; resolve EDGE-073 findings structurally  *(blocked by T1; supersedes T0)*
`ClassifyEvent` reasons over facts; secret/source classification derived from facts; keep risk_tags/labels for kernel back-compat.
**DoD:** facts-based classification; EDGE-073 bypass+FP resolved structurally; edge tests green `-count=3`; `policy-simulations.json` asserts fact-derived inputs; redaction contract preserved (no raw path in labels).

### T5 — Integrate engine into Safety Kernel behind a flag + completeness analysis in CI  *(blocked by T2, T4)*
Flag-gated engine path in `core/controlplane/safetykernel` edge evaluation; parity tests vs current rules; CI runs the completeness analysis so a prohibited allow-path fails the build.
**DoD:** flag-gated path (default off); decision parity on fixture corpus; CI completeness gate; rollback plan + flag docs.

### T6 — Default-deny posture + layered-enforcement docs (honesty rail)  *(blocked by T3)*
**DoD:** doc of the layered model + which "no" lives at which layer + determinism-vs-completeness; production default-deny confirmed; demo labeled "default-allow = not a security posture"; residual-gap register (copy-then-read, var/glob expansion, content policies) with the covering layer each; fail-mode guidance corrected.

## Sequencing

```
T0 ─ (now, independent)
T1 ─▶ T2 ─▶ T5
  └─▶ T4 ─▶ T5
T3 ─▶ (informs) T5, T6
T4 ─▶ T6
```
