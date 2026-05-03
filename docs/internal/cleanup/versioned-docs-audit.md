# Docusaurus versioned_docs audit — 2026-04-20

Task: [task-e8a0ff88](../../.moe/tasks.json) (epic
[epic-1cadd6f2 Pre-GA Legacy + Dead-Code Sweep](../../.moe/tasks.json)).

## Recon

Ran from `D:\Cordum\cordum\` at audit time:

```
$ ls docs-site/versioned_docs/
version-2.9

$ ls docs-site/versioned_sidebars/
version-2.9-sidebars.json

$ cat docs-site/versions.json
[
  "2.9"
]

$ git tag -l 'v*' | sort -V
v0.1.0 v0.1.1 v0.1.2 v0.1.3 v0.1.4
v0.2.0 v0.2.1
v0.3.0
v0.4.1
v0.5.1 v0.5.6
v0.6.0 v0.6.1 v0.6.5
v0.7.0 v0.7.5
v0.8.0 v0.8.1
v0.9.1 v0.9.2 ...

$ git tag -l 'v2*'
(empty)
```

## Decision table

| versioned_docs branch | versions.json entry | Sidebar file | Matching public git tag | Decision |
|-----------------------|---------------------|--------------|--------------------------|----------|
| `version-2.9/` | `"2.9"` | `version-2.9-sidebars.json` | **none** (no `v2.9*`, no `v2.*`) | **DELETE** |

No other `versioned_docs/version-*` branches exist. The single
`version-2.9` snapshot does **not** correspond to any public release
tag; it is a never-released Docusaurus versioning artefact from an
abandoned plan. Policy [`feedback_no_backwards_compat.md`](../../.. )
governs this case: delete outright.

## Cross-repo callers

Planning-time grep of `version-2.9` and `2.9/` across `cordum`,
`cordum-packs`, `Cordum-site`, `cordum-marketing` returned **zero**
live references (the audit artefact itself and Docusaurus-internal
metadata are the only hits). Deleting creates no broken links in
any other repo.

## Actions

Step 2 executes:

1. `rm -rf cordum/docs-site/versioned_docs/version-2.9/`
2. `rm cordum/docs-site/versioned_sidebars/version-2.9-sidebars.json`
3. Overwrite `cordum/docs-site/versions.json` with `[]` (keeps the
   file present so a future `npm run docusaurus docs:version <x>`
   has a target; `[]` is a less-surprising state than a missing
   file).
4. Inspect `docusaurus.config.ts` for any `versions:` /
   `lastVersion:` / `onlyIncludeVersions:` / `includeCurrentVersion:`
   keys referencing `2.9`; recon did not find any but step 2
   re-checks for safety.

## Out of scope

- `Cordum-site/docs-site/` — separate Docusaurus site with its own
  versioning; not touched by this task.
- Current `cordum/docs-site/docs/` — that is the live unversioned
  docs tree and **stays**. Only `versioned_docs/` snapshots are
  pruned.
- Pre-creating any future versioned snapshot — that belongs to the
  actual ship-release task, not this sweep.

## No runtime impact

Docs-only change. No Go / React / SDK / gateway surface touched. No
SIEMEvent or slog emission altered.
