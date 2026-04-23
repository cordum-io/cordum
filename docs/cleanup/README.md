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
