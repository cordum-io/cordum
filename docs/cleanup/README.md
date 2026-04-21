# Cleanup Journal

Durable record of pre-GA legacy / dead-code sweeps. Cordum is pre-GA
with no external adopters to protect, so the governing policy is
`feedback_no_backwards_compat.md` — delete legacy rather than deprecate
for greenfield surfaces. Each audit in this directory is the product of
one cleanup task from epic-1cadd6f2 ("Pre-GA Legacy + Dead-Code Sweep");
rows stay readable after the deletion so reviewers can reconstruct the
decision later.

## Index

- [`deprecated-symbols-audit.md`](./deprecated-symbols-audit.md) —
  every `// Deprecated:` godoc marker in the Go tree, classified as
  `DELETE` / `KEEP_DOMAIN_VOCAB` / `KEEP_UNTIL_UPSTREAM`, with caller
  counts and the action taken.
- [`auth-license-compat-audit.md`](./auth-license-compat-audit.md) —
  the legacy license envelope + `auth_compat.go` shim deletion.
- [`openapi-legacy-audit.md`](./openapi-legacy-audit.md) —
  `cordum-rest.yaml` + `cordum.swagger.json` + MCP route alias
  removal.
- [`backward-legacy-sweep-20260420.md`](./backward-legacy-sweep-20260420.md)
  — residual `backward / legacy / deprecated` prose sweep across the
  non-sibling-owned surfaces (cordum core, dashboard, docs). Pattern-
  classified; three surgical comment rewrites landed, everything
  else is CONTEXTUAL, DOMAIN_VOCAB, or ALREADY_COVERED_BY_SIBLING_TASK.
- [`versioned-docs-audit.md`](./versioned-docs-audit.md) — pruned the
  never-released Docusaurus `version-2.9` snapshot from
  `cordum/docs-site/versioned_docs/`. No matching public git tag; no
  cross-repo callers; Docusaurus build clean after deletion.

## Policy

1. Delete greenfield legacy outright. Do NOT keep shims, aliases, or
   deprecation notices for unshipped surfaces.
2. Protobuf wire contracts consumed by unreleased external SDKs (e.g.
   the `core/protocol/capsdk/` handshake mirror for cap v2.9) stay
   until the upstream ships. `feedback_triple_check_deletions.md` still
   applies there.
3. Every deletion PR includes: caller audit (grep output), migration
   commits for any live caller, test suite green, redocly lint green,
   release-note bullet listing what was removed by exact symbol name.
4. One legacy surface per PR. Don't batch.
