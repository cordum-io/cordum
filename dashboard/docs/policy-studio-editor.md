# Policy Studio Editor — Drawer + Cross-link Contract

This document describes the Rule editor drawer's URL contract and the
canonical cross-link patterns peer surfaces (Decisions, Bundles, Rules
list, empty-state templates gallery) emit to deep-link into the editor.

## Drawer URL contract

The `RuleEditorDrawer` mounts on `/policies` and reads its open/closed
state from the URL search params. The contract is intentionally narrow:
six keys total. Anything outside this list is unrelated UI state and is
preserved verbatim across drawer open / close cycles.

| Key        | Required           | Allowed values                              | Notes                                                                            |
|------------|--------------------|---------------------------------------------|----------------------------------------------------------------------------------|
| `open`     | yes (to open)      | `editor`                                    | The on/off switch. Drawer is rendered iff `open === "editor"`.                   |
| `rule`     | one of {rule,new}  | `<id>` of an existing rule, or `new`        | `new` is the legacy create-new sentinel from 3A. See `new=true` for the alt.     |
| `new`      | one of {rule,new}  | `true`                                      | Alternate create-new entry. Equivalent to `rule=new`.                            |
| `type`     | required if create | `input` / `output` / `velocity` / `edge`    | Required on the create-new path. Determines which schema the editor mounts.      |
| `template` | optional, if new   | template id from `RULE_TEMPLATES`           | When set on a create-new URL, drawer pre-fills Monaco from the template's YAML.  |
| `bundle`   | optional           | bundle id                                   | Forwarded to drawer state. Save-to-bundle bind ships in 3E (currently parked).   |

The drawer's close handler removes ALL six keys atomically. Unrelated
keys (`scope`, `status`, `search`, etc.) survive a click-through; this
matches the QA contract codified in 3A reopen #3.

### Two equivalent create-new entry points

```
/policies?rule=new&type=<ruleType>&open=editor              # 3A original
/policies?new=true&type=<ruleType>&open=editor              # D4 alternate
```

The drawer canonicalizes both forms onto `NEW_RULE_ID` so downstream
hooks (`useRule`, `applyTemplateToDraft`) treat them equivalently. Peer
surfaces should prefer `new=true` because it composes naturally with
`template=` and `bundle=`.

### Template pre-fill

When `template=<id>` is set on a create-new URL, the drawer looks the id
up in `RULE_TEMPLATES` (`src/lib/policy-studio/templates/index.ts`) and
calls `yamlToPartialRule(template.yaml, base)` to seed the canonical
draft. Defensive fallbacks:

- Unknown template id → log a structured `logger.warn` and start from
  the empty draft. The drawer never crashes on stale URLs.
- Template YAML fails to parse → log warn + empty-draft fallback.

## Cross-link contract

Peer surfaces deep-link into the drawer using the URL shapes below. All
links MUST URL-encode the dynamic segments (`encodeURIComponent`).

### From the Rules table (PoliciesPage)

| Action               | URL                                                           |
|----------------------|---------------------------------------------------------------|
| Open existing row    | `/policies?rule=<id>&open=editor`                             |
| Header `+ New rule`  | `/policies?rule=new&open=editor&type=input`                   |

### From the empty-state templates gallery (D4)

```
/policies?new=true&type=<ruleType>&template=<templateId>&open=editor
```

Each `RULE_TEMPLATES` entry's `ruleType` and `id` flow into the URL.
Clicking lands the user in the editor with the template YAML pre-filled.

### From the Decisions surface (D8) — to be emitted by D8

```
/policies?rule=<id>&open=editor
```

`DecisionExpandRow`'s "→ Rule" button. The decision's `rule_id` is the
`<id>`. Already documented in D8's plan; D4 verifies the contract here
without changing D8 code.

### From the Bundles surface (D5) — to be emitted by D5

```
/policies?new=true&type=<ruleType>&bundle=<bundleId>&open=editor
```

`BundleRulesTab`'s "Add rule…" or "+ New rule in this bundle" action.
The bundle id flows through the drawer's `bundleId` control field; the
Save flow uses it once Phase 3E (Backend 5c) lands.

## Why the gallery on truly-empty only

The empty-state templates gallery renders ONLY when both conditions
hold: `data.rules.length === 0` AND `filtersActive === false`. If the
list is empty because filters are filtering everything out, the gallery
does NOT render — surfacing it there would mislead authors into
thinking creating from a template would clear the active filters (it
wouldn't). The user's mental model needs the path to be:

1. Filtered empty → "clear filters or adjust the search term".
2. Truly empty → "start from a template, or `+ New rule` for blank".

This branching lives in `PoliciesPage.tsx`'s `emptyState` ternary and is
covered by tests in `PoliciesPage.test.tsx`:

- `truly-empty state renders the templates gallery (D4 DoD #1)`.
- `filtered-empty state shows the clear-filters copy and does NOT render the templates gallery (D4 branching)`.

## Maintenance notes

- New templates: add a `<id>.yaml` file under
  `src/lib/policy-studio/templates/` and an entry in
  `index.ts`'s `RULE_TEMPLATES` array. Vite's `?raw` import inlines the
  YAML body; no build-time codegen needed.
- New URL keys: extend `EDITOR_QUERY_KEYS` in `RuleEditorDrawer.tsx` so
  the close handler clears them too. Otherwise stale params leak into
  the next drawer open.
