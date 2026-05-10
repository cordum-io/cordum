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

## Deployment timeline (Gantt) — Bundles tab

The `BundleDeploymentTimeline` component (D6) renders a Gantt-style chart
above the scope × version matrix on `/policies/bundles/:id?tab=deployments`.
Source: `dashboard/src/pages/policies/BundleDeploymentTimeline.tsx` and the
pure helper `dashboard/src/lib/policy-studio/timeline-segments.ts`.

### Zoom contract

| Preset | Range          | Notes                                                  |
|--------|----------------|--------------------------------------------------------|
| 1d     | last 24h       | Same-day incident windows.                             |
| 7d     | last 7 days    | Default sprint-scope investigation window.             |
| 30d    | last 30 days   | Default preset on first paint.                         |

Range state is local component state (not URL state) to avoid
collision with `?tab=` and `?v=` keys on the bundle detail page. The
range can be reset on each visit; per-page persistence is intentional.

### Segment colour encoding

Per-version colours rotate through 5 CSS-variable tokens, in order of
first occurrence in the segment list:

```
--color-cordum    (idx 0)
--color-success   (idx 1)
--color-warning   (idx 2)
--color-info      (idx 3)
--color-accent    (idx 4)
```

`versionColorIndex(version, versionOrder)` returns the stable index;
rollback to a previous version reuses that version's colour for visual
continuity (a deploy of v1, then v2, then rollback-to-v1 yields two v1
segments that share the same colour).

### Tooltip content (Path-A)

Each segment exposes a native SVG `<title>` element so screen readers
and hover both surface:

```
Version <version> on <scope>
Deployed <relative-time> (<deployed_at ISO 8601>)
```

`author` and `audit_hash` are intentionally NOT shown in this phase —
the `BundleDeployment` shape from `useBundleDeployments` doesn't include
them yet. Backend 2.5 (task-2a3050b3) extends the OpenAPI yaml +
regenerates the dashboard TS to add `deployed_by`, `audit_hash`, and
`action: "deploy" | "rollback"`. When that lands, the tooltip extension
is a one-line additive change in `BundleDeploymentTimelineSvg`.

### Mobile fallback

The SVG container is `<div className="hidden sm:block">`; below the
`sm` Tailwind breakpoint (~640px), only the fallback paragraph
("Open this page on a wider screen…") renders. The scope × version
matrix below is the primary mobile view.

### Click → navigate

Each segment is a `<Link to={`/policies/bundles/${bundleId}?tab=versions&v=${version}`}>`.
Clicking lands the user on the Bundle's "Versions" tab pre-filtered to
that segment's version. The link's href is stable and SR-friendly.

## Decisions live mode (D8b — `useDecisionsStream`)

The `/policies/decisions` surface ships a live-mode toggle that streams
incoming policy decisions into a 200-event ring buffer at the top of the
DataTable. The hook is `src/hooks/useDecisionsStream.ts` and the toggle
sits in `DecisionsFilterBar`.

### Lifecycle

```
                  ┌───────────────────────────┐
   enabled=false  │                           │  enabled=true
   ─────────────► │       mode: 'closed'      │ ──────────────►  new WebSocket()
                  │ (no socket, ring empty)   │
                  └───────────────────────────┘
                                                 ┌─ ws.onopen fires <3s ──► mode: 'ws'
                                                 │
                                                 ├─ ws.onerror / 3s timeout
                                                 │  / new WebSocket() throws ──► mode: 'polling'
                                                 │       (refetchInterval=2s on /policy/decisions)
                                                 │
                                                 └─ ws.onclose AFTER onopen ──► mode: 'polling'
```

- **WS open**: `ws://<host>/api/v1/policy/decisions/stream?source=<src>&type=<typ>`. Subprotocol auth `["cordum-api-key", base64url(apiKey)]`.
- **3s connect timeout**: if `ws.readyState !== OPEN`, the hook closes the socket and sets `mode: 'polling'` directly (rather than relying on `onclose`, which is asynchronous on real WebSocket and synchronous on the test mock).
- **Polling fallback**: `useQuery` on `listPolicyDecisions({source, type, limit: 100})` with `refetchInterval: 2_000`. Each snapshot REPLACES the ring buffer (server is source of truth in polling mode). No append-with-dedupe needed.
- **Filter change**: a collapsed `fkey = "<source>|<type>"` is in the effect deps. React tears down the old socket (effect cleanup runs first) before opening the new one, so source/type changes are zombie-free.
- **Unmount / enabled=false**: cleanup runs `clearTimeout(connectTimerRef)` → `wsRef.current.close()` → `wsRef.current = null`; on enabled=false the ring buffer is also reset to `[]` and mode goes back to `'closed'`.

### Why JOB decisions sometimes go via polling

Backend 5b ships the per-API-gateway-replica broker incrementally. EDGE
decisions emit on the same Go process that serves the WS, so they
arrive end-to-end live. JOB decisions land in the Scheduler process and
won't fan out to the gateway broker until the cross-process NATS bridge
ships. Until then, JOB-source live mode auto-falls back to polling — the
toggle still works, the user sees decisions within 2s of arrival, and
nothing UI-side needs to change once the bridge lands.

### Ring buffer cap (200 events)

```ts
const next = [raw, ...ringRef.current];
ringRef.current = next.length > 200 ? next.slice(0, 200) : next;
```

Newest first; oldest evicted on overflow. The buffer is a `useRef` (not
state) and re-renders are driven by a manual `tick` setter to avoid
per-frame React state churn under high decision rate. The 200 cap is
both a memory bound and a UX bound (the user is scanning, not auditing —
authoritative history lives in the `Load more` cursor-paged path below
the live feed).

### Charts toggle + panel (D8b toggle + D9b panel)

The `Charts ▾` toggle writes `?charts=on` to the URL via `nuqs`
`parseAsStringLiteral(["on"])`. DecisionsPage gates a lazy
`<DecisionsChartsPanel decisions={…} />` (React.lazy + Suspense) on that
flag so the eager `/policies/decisions` chunk doesn't pull Recharts.

The panel renders four compact Recharts charts in a responsive 1-col
/ 2-col grid:

1. **Decision distribution** — PieChart, count by `DecisionType`.
2. **Top firing rules** — horizontal BarChart, top 10. Below the chart,
   the top 5 are surfaced as `<Link>` anchors carrying
   `data-row-action="cross-link-decisions-rule"` + href
   `/policies?rule=<id>&open=editor` (matches D10b's cross-link-2
   contract; clicking the rule label deep-links to the rule editor).
3. **Decisions per minute** — LineChart bucketed to ISO-minute UTC
   slugs; the chart shows the last hour of data.
4. **Decisions by source** — stacked BarChart with x=source (job vs
   edge), stacked by DecisionType. Decision shape lacks `scope_kind`;
   `source` is the truthful proxy until a sibling backend task
   exposes scope on the unified Decision envelope.

Each ChartCard exposes `data-decision-count` (the throttled total)
plus a chart-specific `aria-label` so screen readers announce the
metric without needing the chart axes.

**1Hz throttle (live mode):** the panel wraps incoming `decisions[]`
through co-located `useThrottledValue(value, 1000)`. Leading-edge
emit on first input; subsequent inputs inside the 1s window are
deferred via `setTimeout(intervalMs - elapsed)` so the trailing-edge
update lands once with the latest snapshot. A 100/s prop-update
burst from `useDecisionsStream` produces **zero** chart-data
recomputes inside the window — the trailing tick wins. Implemented
with native `setTimeout`/`Date.now`; no lodash dep.

**Bundle-size separation:** verified via `npm run build` —
`DecisionsPage-*.js` (~26KB) does NOT contain Recharts;
`DecisionsChartsPanel-*.js` (~6.6KB) ships the panel logic; the
Recharts library lives in a separate `generateCategoricalChart-*.js`
chunk (~353KB), transitively lazy through the panel's `React.lazy`.

### Decisions actions (D9b — Replay + What-if + cross-link)

The DecisionExpandRow's Actions row exposes three operations, all
keyed off the unified `Decision` payload:

**Replay** (`useReplayDecision`): clicking issues a POST to
`/api/v1/policy/replay` with body
`{from, to, filters: {original_decision: decision.type},
use_current_policy: true, max_jobs: 1}` — a 1-second window around
`decision.timestamp` is the closest per-decision approximation
available against the bulk endpoint (Decision shape lacks
`tenant_id`/`topic`/`principal_id`, so a single-decision lane needs
a sibling backend task). The hook derives `{was, now, bundleVersion,
changed}` from `response.changes[0].new_decision` (when present) or
`response.summary.unchanged` (when changes[] is empty). The result
panel renders inline under the action row:

- `data.changed === true` → "If evaluated now: \<NowBadge\> (was:
  \<WasBadge\>)" with warning border highlight.
- `data.changed === false` → neutral "No change: still \<Badge\>
  under the active policy".
- Loading / error states render dedicated `data-testid` markers
  (`decision-replay-loading`, `decision-replay-error`).

On mutation success the hook invalidates the
`policy-studio-decisions` queryKey so the list refetches in case
the bundle rolled forward between record and replay.

**What-if** (`<WhatIfDrawer>`): clicking opens a Drawer (size=xl)
loaded with the firing rule (via `useRuleAtVersion(decision.rule_id,
decision.bundle_version)`). The drawer hosts the shared lazy
`<RuleMonacoEditor>` so the YAML editing experience is identical to
the Rule editor surface. Side-by-side Actual / Hypothetical panels
let the author compare the on-the-wire decision (read-only) against
a re-evaluation of their edited rule (computed on Re-evaluate via
`POST /api/v1/policy/evaluate`).

**No-save semantics** (spec § L141): `WhatIfDrawer` never imports
or calls `updatePolicyRule` / `createPolicyRule`. Closing the
drawer or unmounting fires `useEffect [open=false]` which resets
draft + hypothetical state — the in-progress edit is intentionally
lost. Authors who want to persist the experiment open the rule
editor via the row's "Open rule" cross-link instead. A regression
test asserts PUT/POST to `/api/v1/policy/rules` MUST never fire
while the drawer is open.

**Rule fetch fallback:** `useRuleAtVersion(ruleID, bundleVersion)`
calls `listPolicyRules({limit: 500})` and client-side filters for
`rule.id === ruleID`. Backend 5d's GET `/api/v1/policy/rules` does
not yet accept a `version` query parameter, so when `bundleVersion`
is provided the hook logs a structured warn
(`logger.warn("decisions-whatif", "rule-version filter unsupported;
returning latest rule", {rule_id, bundle_version})`) and returns
the latest snapshot. Sibling backend task tracks the version-pin.

**Open rule** (unchanged from D8b): react-router `<Link>` to
`/policies?rule=<id>&open=editor`, matching the D10a cross-link
contract used elsewhere in the studio.

### Cursor pagination vs Live mode

The "Load more" button under the table is gated `{!live && list.hasMore}`.
Reasoning: pulling another cursor page while the ring buffer is
streaming would interleave server-history with live-prepended rows
and corrupt the cursor's intended ordering. The UX rule is: flip Live
OFF to scroll back through history; flip Live ON to follow the present.
React Query caches paged data so the user resumes at the same cursor
position when they flip Live OFF again.

## Maintenance notes

- New templates: add a `<id>.yaml` file under
  `src/lib/policy-studio/templates/` and an entry in
  `index.ts`'s `RULE_TEMPLATES` array. Vite's `?raw` import inlines the
  YAML body; no build-time codegen needed.
- New URL keys: extend `EDITOR_QUERY_KEYS` in `RuleEditorDrawer.tsx` so
  the close handler clears them too. Otherwise stale params leak into
  the next drawer open.
