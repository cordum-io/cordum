# Pack `metadata.aliases`

A pack manifest declares a single `metadata.id` that scopes the
namespaces the pack owns. By default, every topic the pack registers must
sit under `job.<metadata.id>.*` — so pack `cordclaw` can only own
`job.cordclaw.*` topics.

Some packs legitimately want to own multiple sibling namespaces. The
canonical case is CordClaw, which intercepts every OpenClaw hook event
and emits it as a Cordum job under `job.openclaw.*` topics — but the pack
itself is named `cordclaw` and the renames would conflict with existing
CordClaw rules under `job.cordclaw.*`.

`metadata.aliases` solves this without weakening the namespace rule for
everyone else.

## Field shape

```yaml
metadata:
  id: cordclaw
  version: "1.2.0"
  aliases:
    - openclaw
```

| Constraint | Value |
| --- | --- |
| Regex | `^[a-z][a-z0-9_-]{1,30}$` (lowercase letter first; lowercase alnum / `-` / `_` after; 2-31 chars) |
| Max entries | 8 |
| Duplicates | rejected |
| Optional | yes — packs without `aliases` keep validating under the strict prefix rule (back-compat) |

## Validator behavior

When `aliases` is set:

- Topic namespace check accepts `job.<id>.*` AND `job.<alias>.*` for
  each declared alias.
- Pool-overlay `topics` map (`overlays/pools.patch.yaml`) accepts the
  same set.
- Timeouts-overlay `topics` map accepts the same set.
- Sibling invariants (schema id prefix, workflow id prefix, pool name
  prefix, pack-id regex) are unchanged — only the topic namespace check
  honors aliases.

When `aliases` is absent or empty:

- The strict `job.<id>.*` rule applies (unchanged from prior behavior).

## Rationale

### Why declared aliases, not a weakened prefix check

A globally relaxed prefix rule lets any pack claim any namespace at
install time. With declared aliases, the namespaces a pack owns are
auditable in the manifest itself; ops can grep `metadata.aliases:`
across the pack registry to inventory cross-namespace ownership.

### Why the regex cap

The lowercase-only / hyphen-or-underscore-only pattern matches the same
character class the pack-id regex uses (`^[a-z0-9-]+$`), so aliases are
visually consistent with pack IDs. The 31-char cap matches the practical
maximum we see in pack IDs.

### Why 8 entries

Eight is enough for the realistic "this pack owns N sibling namespaces"
cases (CordClaw + OpenClaw is 2; a hypothetical pack bridging four
upstream tools could use 5). Capping at 8 prevents the alias list from
becoming a back-door way to register arbitrary topics.

### What aliases do NOT change

- `metadata.id` is still the pack's identity for install/uninstall and
  for the `cordumctl pack list` output.
- Pool names still need to be prefixed with `metadata.id` (NOT aliases).
- Schema IDs and workflow IDs still namespace under `metadata.id`.
- Aliases are not advertised over the pack discovery API as a way to
  resolve a pack — only the canonical `metadata.id` is.

## Example: cordclaw pack with openclaw alias

```yaml
apiVersion: cordum.io/v1
kind: Pack
metadata:
  id: cordclaw
  version: "1.2.0"
  title: CordClaw
  aliases:
    - openclaw
topics:
  - name: job.cordclaw.exec
    requires: []
  - name: job.openclaw.tool_call
    requires: []
  - name: job.openclaw.prompt_build
    requires: []
overlays:
  config:
    - key: pools
      strategy: json_merge_patch
      path: overlays/pools.patch.yaml
```

The `overlays/pools.patch.yaml` may declare `job.openclaw.*` topic
mappings under the same pack-install transaction.
