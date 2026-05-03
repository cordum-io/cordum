# Deprecated Symbols Audit — 2026-04-20

Durable record of every `Deprecated:` godoc marker in the Go tree as of
the pre-GA legacy sweep (epic-1cadd6f2, task-70af7e9b). Each row lists
the origin of the marker, caller counts, and the classification that
drives what we do with it.

Classifications:

| Code | Meaning |
|------|---------|
| `DELETE` | Hand-written deprecation we authored. Remove the symbol + its callers in the same PR. |
| `KEEP_UNTIL_UPSTREAM` | Protoc-gen-go boilerplate (on `Descriptor()` / `EnumDescriptor()`). Cannot be hand-edited — the next `make proto` re-emits it. Closure depends on upgrading `google.golang.org/protobuf` / `protoc-gen-go` to a version that no longer emits those legacy accessors or passing a regen flag that suppresses them. |
| `KEEP_DOMAIN_VOCAB` | Not a Go deprecation — the grep matched a domain-vocabulary string (e.g. a lifecycle enum value named `Deprecated`). Leave untouched. |
| `KEEP_UNTIL_UPSTREAM` | Protobuf wire contract consumed by an unreleased external SDK (epic rail #2). Stays until the upstream ships. |

## Baseline count

```
$ grep -rn '^// Deprecated:' --include='*.go' .
16 matches across 5 files.
```

## Audit table

| # | File:Line | Symbol | Origin | Callers (cordum) | Callers (other repos) | Classification | Action |
|---|-----------|--------|--------|------------------|------------------------|----------------|--------|
| 1 | `sdk/client/client.go:98` | `BuildTLSTransport` | Hand-written deprecation on an error-swallowing wrapper around `BuildTLSTransportErr`. | 3 callers, all in `sdk/client/client_test.go` (lines 321, 328, 342). Zero callers in non-test code. | `cordum-packs/sdk/client/` vendors its own copy (file-level clone, not a cross-repo import), so cordum-packs is unaffected by our deletion. `cordum-enterprise`, `cordum-tools`, `cordum-marketing`, `cap` — zero matches (cross-repo grep). | `DELETE` | Remove the function + migrate the 3 test sites to `BuildTLSTransportErr`. |
| 2 | `core/protocol/pb/v1/context.pb.go:71` | `ContextMode.EnumDescriptor` (legacy enum descriptor accessor) | Protoc-gen-go | N/A (generated) | N/A | `KEEP_UNTIL_UPSTREAM` | Only resolvable via a protoc-gen-go upgrade — see step 3. |
| 3 | `core/protocol/pb/v1/context.pb.go:109` | `ModelMessage.Descriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 4 | `core/protocol/pb/v1/context.pb.go:165` | `BuildWindowRequest.Descriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 5 | `core/protocol/pb/v1/context.pb.go:246` | `BuildWindowResponse.Descriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 6 | `core/protocol/pb/v1/context.pb.go:307` | `UpdateMemoryRequest.Descriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 7 | `core/protocol/pb/v1/context.pb.go:371` | `UpdateMemoryResponse.Descriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 8 | `core/protocol/pb/v1/output_policy.pb.go:68` | `OutputDecision.EnumDescriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 9 | `core/protocol/pb/v1/output_policy.pb.go:125` | `OutputCheckRequest.Descriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 10 | `core/protocol/pb/v1/output_policy.pb.go:314` | `OutputCheckResponse.Descriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 11 | `core/protocol/pb/v1/output_policy.pb.go:400` | `OutputFinding.Descriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 12 | `core/protocol/pb/v1/api.pb.go:72` | `SubmitJobRequest.Descriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 13 | `core/protocol/pb/v1/api.pb.go:235` | `SubmitJobResponse.Descriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 14 | `core/protocol/pb/v1/api.pb.go:300` | `GetJobStatusRequest.Descriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 15 | `core/protocol/pb/v1/api.pb.go:346` | `GetJobStatusResponse.Descriptor` | Protoc-gen-go | N/A | N/A | `KEEP_UNTIL_UPSTREAM` | See step 3. |
| 16 | `core/controlplane/topicregistry/service.go:29` | `StatusDeprecated` (enum value) | Domain vocabulary — the string `StatusDeprecated` is a topic lifecycle state (`active` / `deprecated` / `disabled`). The grep matched `StatusDeprecated:` in the `statusValid` map, NOT a `// Deprecated:` marker. | N/A | N/A | `KEEP_DOMAIN_VOCAB` | Leave untouched. Renaming or removing the topic lifecycle value is a breaking API change, not a cleanup. |

## Summary

- **1 DELETE:** `sdk/client.BuildTLSTransport` — step 2 ships the deletion.
- **14 KEEP_UNTIL_UPSTREAM:** the legacy `Descriptor()` / `EnumDescriptor()` accessors in the three `*.pb.go` files. Step 3 verified that `protoc-gen-go v1.36.x` still emits these markers intentionally (no suppression flag) and that the current host toolchain does not close them, so they remain owned by the `google.golang.org/protobuf` upstream and will resolve organically when that project drops the legacy accessors. Do NOT hand-edit `*.pb.go`.
- **1 KEEP_DOMAIN_VOCAB:** `StatusDeprecated` enum value in `topicregistry` — not a Go deprecation.

## Post-deletion re-scan (actual after this task)

Step 2 removed `BuildTLSTransport` + its 3 test callers. Step 3 (protoc
evaluation) decided to LEAVE the 14 `*.pb.go` boilerplate markers in
place — see below.

Post-task scope of our own Go tree:

```
$ grep -rn '^// Deprecated:' --include='*.go' core/ sdk/ cmd/ | wc -l
14
```

All 14 remaining hits are protoc-gen-go emitted boilerplate on legacy
`Descriptor()` / `EnumDescriptor()` accessors.

## Step 3 decision: protobuf toolchain upgrade is OUT OF SCOPE for this task

Current state (verified 2026-04-20):

| Artefact | Version |
|----------|---------|
| `go.mod google.golang.org/protobuf` | `v1.36.11` |
| `protoc-gen-go` on PATH | `v1.36.10` |
| `protoc` on PATH | `libprotoc 33.2` |
| `*.pb.go` header reports | `protoc-gen-go v1.36.11 / protoc v3.12.4` |

Analysis:

1. `protoc-gen-go v1.36.x` still emits the legacy `Descriptor()` /
   `EnumDescriptor()` accessors with `// Deprecated:` markers. This is
   intentional upstream behavior — the accessors are a backward-compat
   surface for code written against the pre-`ProtoReflect` API, and
   upstream has not yet removed them in a v1.x release. There is no
   flag on `protoc-gen-go v1.36.x` that suppresses them.
2. Running `make proto` on this host would mix TWO upstream-version
   bumps into the diff (protoc v3.12.4 → v33.2, protoc-gen-go v1.36.11
   → v1.36.10) without removing a single `Deprecated:` marker. That
   fails the epic rail "one surface per PR" AND doesn't actually close
   any of these deprecation rows.
3. Hand-editing the `*.pb.go` files is forbidden: any edit is
   overwritten by the next `make proto` run and regresses the PR.

Conclusion: the 14 boilerplate markers are OWNED BY THE UPSTREAM
protobuf project, not by cordum. They will disappear organically when a
future `google.golang.org/protobuf` major release (≥ v1.37 or v2.0)
drops the legacy accessors. Rows 2–15 of the audit table are therefore
classified as `KEEP_UNTIL_UPSTREAM` — they cannot be closed by any
cordum-side
change today.

Follow-up work (candidates, deliberately NOT bundled here):

- Monitor `google.golang.org/protobuf` release notes for removal of
  legacy `Descriptor()` accessors.
- If a future task wants to standardize the host's protoc/protoc-gen-go
  versions on the repo-declared ones (to eliminate the v3.12.4/v33.2
  drift), open a dedicated build-tooling task.

No cordum-side code change is required for these 14 rows; leaving them
in place is correct today.
