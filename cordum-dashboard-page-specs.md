# Cordum Dashboard — Page-by-Page Feature Specification

**Version:** 3.0 (Post-PR #101 Redesign)
**Date:** February 2026
**Scope:** Every page in the redesigned dashboard, grouped by the new sidebar structure

---

## Navigation Restructure

**Current state (`AppShell.tsx` line 37–48):** Flat list of 10 items — Overview, Jobs, Workflows, Agent Fleet, Approvals, Policy Studio, Packs, Dead Letters, Audit Log, Settings. No grouping, no hierarchy. The sidebar tells the story "this is an ops tool."

**New structure:** Four collapsible section groups. The sidebar now tells the story "this is a governance platform that also does ops."

```
SECURITY
  Security Overview  (new — replaces /)
  Approvals          (moved from position 5 → 2)
  Policies           (moved from position 6 → 3)
  Audit Trail        (moved from position 9 → 4)
  Quarantine         (new — split from DLQ)
  Safety Controls    (new — wraps Input/Output Safety from Settings)

OPERATIONS
  Runs               (new — top-level run listing)
  Jobs               (kept)
  Agent Fleet        (kept)
  Failures           (renamed from "Dead Letters", minus quarantined items)

BUILD
  Workflows          (kept)
  Packs              (kept)
  Schemas            (was hidden — now in nav)

SETTINGS
  Settings           (kept, minus Input/Output Safety sub-routes)
```

**Implementation:** The `navItems` array in `AppShell.tsx` becomes a nested structure with `section` keys. Each section gets a header label. The "SECURITY" header renders in the accent color to draw the eye immediately. Badge queries for approvals (`approvalsQuery`) and DLQ (`dlqQuery`) on lines 80–96 stay the same, but the DLQ badge splits: quarantined count goes to the Quarantine nav item, operational failures go to the Failures nav item. This requires filtering `dlqQuery.data.items` by checking whether the entry's `status` or `lastState` includes `output_quarantined`.

**File:** `src/components/layout/AppShell.tsx`
**Effort:** ~2 hours

---

## SECURITY CENTER

---

### Page 1: Security Overview

**Route:** `/` (replaces current HomePage)
**Status:** New page — `SecurityOverviewPage.tsx`
**Purpose:** Single-glance governance posture. This is what opens when you launch the dashboard. It answers: "Is my AI fleet governed? What needs my attention right now?"

#### 1.1 Posture Metrics Strip

Five metric cards in a horizontal row at the top of the page.

**Card 1 — Governance Score.** Reuses the `computeGovernanceScore()` function from `PoliciesOverviewPage.tsx` (lines 54–93). Inputs are policy bundles, audit entries, snapshot count, and latest publish timestamp. The score weights 40% enabled-bundle ratio, 30% violation rate (inverse), and 30% freshness of last publish. Displays as a number out of 100 with a color indicator (green >80, yellow 50–80, red <50) and a one-word label (Healthy, Degraded, At Risk). Subtitle shows total active policy count, pulled from the bundles array length.

**Card 2 — Pending Approvals.** Count from `approvalsQuery.data.items.length` (already fetched in `AppShell.tsx` line 80–84 for the nav badge). Subtitle shows SLA breach count by filtering items where `waitMs > approvalSlaMs` from `useConfigStore`. Color turns warning (yellow) if any pending, danger (red) if any SLA breaches exist.

**Card 3 — Quarantined Outputs.** Count from DLQ entries where `status === 'output_quarantined'` OR where the entry signal contains `output_quarantined` (see `normalizeEntrySignal()` in `DLQPage.tsx` line 74–79). Uses the existing `dlqQuery` from AppShell. Subtitle shows the most recent finding type (PII, secret, injection). Color is danger if count >0, success if 0.

**Card 4 — Input Safety Mode.** Reads from the `/api/v1/settings` config endpoint (the same one `InputSafetySettings.tsx` uses). Displays "Fail-Closed" or "Fail-Open" as the value. Subtitle shows kernel connection status. Color is accent for fail-closed, danger for fail-open (since fail-open in production is a risk posture). This card links to `/security/safety#input` on click.

**Card 5 — Output Safety Status.** Reads from `/api/v1/output-policy/status` (same endpoint as `OutputSafetySettings.tsx`). Displays "Enabled" or "Disabled" with kernel connection status in subtitle. Shows 24h scan count from the `/api/v1/output-policy/stats` endpoint. Links to `/security/safety#output` on click.

**Data sources:** `useStatus()`, `usePolicyBundles()`, `usePolicyAudit()`, `usePolicySnapshots()`, approvals nav query, DLQ nav query, `/api/v1/settings`, `/api/v1/output-policy/status`, `/api/v1/output-policy/stats`.

#### 1.2 Needs Attention Column

Left column. A prioritized list of items requiring human action, rendered as compact alert cards. Items are sourced from three places and merged into a single sorted list.

**Source A — Quarantined outputs.** Each quarantined DLQ entry becomes a card showing: the finding summary (PII pattern, secret type, injection type), the job topic, the matched output rule, the scanner that flagged it, and how long it has been quarantined. Card background uses a subtle red tint. Clicking navigates to `/security/quarantine?id={dlqEntryId}`.

**Source B — SLA-critical approvals.** Approvals where `urgencyLevel` is `critical` or `breach` (see `UrgencyLevel` type in `types.ts` line 534). Card shows the job topic, the matched policy rule, the waiting duration, and the SLA remaining. Background uses subtle yellow tint. Clicking navigates to `/approvals?id={approvalId}`.

**Source C — Other pending approvals.** Non-critical approvals rendered as compact rows (not full cards) with just the job topic, rule, and wait time. These provide completeness without visual noise.

**Sorting:** Quarantined items and SLA breaches sort to the top. Within each group, sort by age descending (oldest first — most urgent).

**Empty state:** When nothing needs attention, show a success message: "All clear — no items require action." This is a positive signal for demos.

#### 1.3 Live Safety Decision Feed

Right column (wider). This is the existing `SafetyDecisionFeed` component from `src/components/home/SafetyDecisionFeed.tsx`, promoted from its current position below the fold on the HomePage to the hero position.

**Current features (keep all):** Real-time WebSocket stream of safety decisions via `useSafetyDecisions()` hook. Each row shows timestamp, job topic, decision type (allow/deny/require_approval/throttle) as a colored badge, matched rule name, and eval time in milliseconds. The feed has a live/paused toggle and shows WebSocket connection status.

**New features to add:**

**Decision summary counters.** Four small counter boxes above the feed showing the count of each decision type in the current visible window: Allow (green), Deny (red), Approval Required (yellow), Throttle (teal). These update in real-time as new events stream in.

**Clickable rows.** Each decision row becomes a link. For `deny` and `throttle` decisions, clicking navigates to the audit trail filtered to that event (`/audit?correlationId={traceId}`). For `require_approval` decisions, clicking navigates to the approval queue (`/approvals?id={approvalRef}`). For `allow` decisions, clicking navigates to the job detail (`/jobs/{jobId}`).

#### 1.4 Recent Audit Events Strip

Below the two-column layout. A compact card showing the last 4–6 high-severity audit events. Each row shows a severity dot, timestamp, event description, entity reference, and an "Investigate →" link that navigates to the audit detail view for that entry. This reuses the `classifyEvent()` function from `AuditEventCard.tsx` to determine severity coloring.

**Purpose:** Closes the loop between the posture strip and the audit trail. An evaluator can see the live decisions, see what needs attention, and see the audit evidence — all on one page.

**Data source:** `useAuditLog()` hook with filters `{ severity: ['high', 'medium'], limit: 6 }`.

#### 1.5 What Moves from the Current HomePage

The current HomePage components have different fates:

**SafetyDecisionFeed** → moves to Security Overview as the hero element (Section 1.3 above).

**MetricCards (Workers, Active Jobs, NATS, Redis, Uptime)** → these are infrastructure metrics. They move to the Settings > System Health page, which already has a `SystemHealthTab.tsx` component that shows similar data. The SecurityOverview replaces them with governance-focused metrics.

**CircuitBreakerCard** → moves to Settings > System Health.

**RateLimiterModeBadge** → moves to Settings > System Health.

**QuickActions** → the "Search" button currently navigates to `/search` which 404s. Fix: wire the search button to call `setCommandOpen(true)` instead, opening the existing `CommandPalette.tsx`. The "Submit Job" button stays as a quick action on the Jobs page. The "View Audit" button is unnecessary with the new audit strip.

**JobPipelineFunnel** → moves to the Runs page as a summary visualization.

**PoolUtilizationGrid** → moves to the Agent Fleet page.

**DLQSummary** → split between Failures page and Quarantine page.

**EventTimeline** → moves to the Audit Trail page as an alternate view.

**ActiveWorkflowCards** → moves to the Workflows page.

**File:** `src/pages/SecurityOverviewPage.tsx` (new)
**Effort:** ~4–5 hours

---

### Page 2: Approvals

**Route:** `/approvals`
**Status:** Existing page — `ApprovalsPage.tsx` (219 lines). Mature implementation from PR #101.
**Purpose:** Human-in-the-loop governance decisions. Approve, reject, or condition agent actions that triggered policy rules.

#### 2.1 Current Features (Keep All)

**Queue/History tabs.** Tabs at the top switch between pending approvals and resolved history. The queue tab shows a badge count. Both use URL search params for deep linking.

**Stats strip** (`StatsStrip` component, line 48–60). Shows pending count, critical count, SLA breach count, and average wait time. Includes a select-all checkbox for bulk actions.

**Queue filters** (`ApprovalQueueFilters` component). Composable filters for urgency level, workflow, risk tags, pool assignment, and capability. Filter state persists in URL params via `useSearchParams`. The `applyFilters()` function handles the multi-dimensional filtering.

**Urgency indicators.** Each approval card has a colored dot based on `urgencyLevel` from the API: green for `fresh`, yellow for `aging`, red for `critical`/`breach`. The `urgencyToVariant()` function on line 39–43 maps these. SLA tracking uses `waitMs` and `approvalSlaMs` from the config store.

**Card-based inbox.** Each approval renders as a row/card showing the job topic, pool, workflow context, matched policy rule, risk tags, wait time, and SLA remaining.

**Slide-out detail panel** (`ApprovalDetailPanel` component, 22KB). Opens to the right when clicking an approval. Contains the safety explanation, payload viewer, job context, workflow context, and action buttons. The detail panel URL-syncs with `?id={approvalId}`.

**Safety explanation panel** (`SafetyExplanation` component). Shows why the Safety Kernel flagged this job — the matched rule, the eval path, and the risk tags that triggered it.

**Payload viewer** (`PayloadViewer` component). Renders the job's context pointer data as formatted JSON with syntax highlighting. Shows both the input payload and any metadata.

**Workflow context** (`WorkflowContext` component). When the job is part of a workflow run, shows the workflow name, run ID, step index, step name, and total steps. This helps approvers understand the blast radius of their decision.

**Bulk actions** (`BulkActionBar` component). Checkbox selection on rows enables bulk approve or bulk reject with a reason field. The bar appears at the bottom of the queue when items are selected.

**Approval history** (`ApprovalHistory` component, 17KB). Shows resolved approvals with pattern detection — identifies repeated approval patterns, common rules, and resolution times.

**Approve-with-conditions.** The approval action supports attaching conditions/constraints via the `constraints` field on the `Approval` type. The approver can specify runtime constraints that get passed back to the Safety Kernel.

**Conflict handling.** The `useApproveJob` and `useRejectJob` mutations handle HTTP 409 (conflict) when another user resolves the same approval concurrently.

#### 2.2 New Features to Add

**"Decision Required Because…" block.** Add a prominent explanation block at the top of the detail panel (before the current safety explanation). This should be a human-readable sentence: "This job requires approval because rule `{policyRule}` matched: {humanSummary}." The `humanSummary` field already exists on the `Approval` type (line 563). If not populated, fall back to the rule name and risk tags.

**Investigation links.** Add three links at the bottom of the detail panel: (1) "Open Job Detail" → `/jobs/{jobId}`, (2) "Open Policy Rule" → `/policies/rules#{ruleId}`, (3) "Open Audit Lifecycle" → `/audit?correlationId={traceId}`. These create the cross-page investigation flow that makes governance feel connected.

**Role-based actions.** The `RequireRole` component (already imported on line 24) should wrap the approve/reject buttons so only users with the `approver` role see them. Read-only users see the detail but not the actions.

**Data sources:** `useApprovals()`, `useApprovalHistory()`, `useApproveJob()`, `useRejectJob()`, `useConfigStore()` for SLA config.
**File:** `src/pages/ApprovalsPage.tsx`
**Effort:** ~2 hours for new features (page already exists)

---

### Page 3: Policies (Policy Studio)

**Route:** `/policies` with sub-routes `/policies/rules`, `/policies/builder`, `/policies/simulator`, `/policies/history`, `/policies/analytics`
**Status:** Existing — 6 pages + `PolicyLayout.tsx` wrapper. Most complete module in the dashboard.
**Purpose:** Create, edit, test, version, and monitor the policy rules that the Safety Kernel enforces.

#### 3.1 Overview Tab (`PoliciesOverviewPage.tsx`, 678 lines)

**Governance Score Card.** SVG ring visualization showing the computed score. Breakdown shows the three components: enabled ratio (40%), violation rate (30%), freshness (30%). Clicking links to the rules tab.

**Bundle listing.** Each installed policy bundle shows: name, version, health status (healthy/degraded/unhealthy via `HealthDot`), enabled/disabled toggle, rule count, and last-updated timestamp. Bundles are fetched via `usePolicyBundleContext()`.

**Decision distribution pie chart.** Recharts `PieChart` showing the proportion of allow/deny/require_approval/throttle decisions in the last 24 hours. Data comes from `usePolicyAudit()`.

**Quick stats row.** Metric cards for: total rules, total decisions (24h), violations (24h), last published version. The publish button triggers `usePublishPolicy()` mutation.

**Snapshot comparison.** The `SnapshotComparison` component lets you diff two policy snapshots side by side to see what changed between publishes.

**Security controls.** The `SecurityControls` component shows lockdown mode toggle and safety stance selector (permissive/balanced/strict). Lockdown mode prevents any policy changes — it reads from `GeneralConfig.maintenanceMode` but specifically for policy lockdown.

**Policy approvals.** The `usePolicyApprovals()` hook shows pending policy-change approvals (when policy changes themselves require approval in strict mode).

#### 3.2 Rules Tab (`PoliciesRulesPage.tsx`)

**Rules table** (`RulesTable.tsx`). Lists all rules across all bundles. Columns: enabled toggle, rule name, match criteria (rendered as human-readable condition), decision type badge, priority, hit count (24h), last triggered timestamp, source bundle.

**Rule cards** (`RuleCard.tsx`, 8KB). Expandable cards showing full rule detail: condition group tree, decision type, reason string, YAML source, and edit button.

**Rule editor** (`RuleEditor.tsx`, 19KB). Slide-out editor with: name, decision type dropdown, priority, reason text, and the condition builder.

**Condition group builder** (`ConditionGroupBuilder.tsx`). Visual AND/OR condition tree builder. Supports nested groups, multiple condition types (risk_tags contains, capability equals, topic matches, etc.), and the `conditionTypes.ts` registry of all supported condition operators.

**Visual rule builder** (`VisualRuleBuilder.tsx`). Drag-and-drop alternative to the condition group builder. More visual, less technical.

**New feature to add — Hit count prominence.** The rules list should show `hitCount24h` and `lastTriggered` from the `PolicyRule` type (lines 387–388) directly in the table row. Currently these fields exist on the type but the table doesn't always surface them.

#### 3.3 Simulator Tab (`PoliciesSimulatorPage.tsx`)

**Single simulation.** Input a job description (topic, risk tags, capabilities, pool) and run it through the current policy bundle to see what decision would be made, which rule matched, and the full eval path.

**Batch simulation** (`BatchSimulator.tsx`). Upload a JSON array of test cases and run them all. Shows pass/fail results with suggestions for rule improvements.

**Policy simulator** (`PolicySimulator.tsx`, 23KB). Full-featured simulator with: test case editor, result display with `ExplainResult.tsx`, suggestion engine, and the ability to save test suites.

**Impact preview** (`ImpactPreview.tsx`). Before publishing a rule change, shows how many of the last N decisions would have been affected. Answers "if I add this rule, what would have been blocked that was previously allowed?"

#### 3.4 History Tab (`PoliciesHistoryPage.tsx`, 24KB)

**Snapshot listing.** Every published version of the policy bundle, with author, message, timestamp, and SHA-256 hash. Each snapshot is downloadable.

**Diff viewer.** Select two snapshots to see a side-by-side diff of rules added, removed, or modified.

**Replay timeline** (`PolicyReplay.tsx`, 14KB). Replay historical decisions against a selected snapshot to understand how policy changes affected outcomes over time.

**Policy timeline** (`PolicyTimeline.tsx`). Visual timeline of policy publishes, showing version progression and annotation of significant changes.

#### 3.5 Analytics Tab (`PoliciesAnalyticsPage.tsx`)

**Policy analytics** (`PolicyAnalytics.tsx`, 26KB). Charts showing: decisions over time (stacked area), rule hit distribution (bar chart), violation trends, approval turnaround times, and top-triggered rules.

**PDF compliance export.** The `PublishControls.tsx` component includes a PDF export function that generates a compliance report suitable for auditors, documenting current policy state, recent changes, and decision statistics.

**New feature to add — Framework alignment labels.** Each rule should optionally carry framework alignment tags (e.g., `owasp-llm-top10`, `nist-rmf`, `eu-ai-act`, `iso-42001`). These are metadata labels that help enterprises map their Cordum policies to compliance requirements. This is a new field on `PolicyRule` — display only, no functional impact.

**Data sources:** `usePolicyBundles()`, `usePolicyAudit()`, `usePolicySnapshots()`, `usePublishPolicy()`, `usePolicyApprovals()`.
**Files:** `src/pages/Policies*.tsx`, `src/components/policy/*`
**Effort:** ~2 hours for new features (pages already exist)

---

### Page 4: Audit Trail

**Route:** `/audit`
**Status:** Existing — `AuditLogPage.tsx` (596 lines). Full-featured from PR #101.
**Purpose:** Tamper-evident record of every governance decision, human action, and system event. The compliance trail.

#### 4.1 Current Features (Keep All)

**Three view modes.** Toggle between Stream, Timeline, and Correlation views. Stream shows a chronological event list. Timeline (`AuditTimeline.tsx`, 15KB) shows events on a visual timeline with zoom/pan. Correlation view groups related events by their correlation chain (e.g., all events related to a single job lifecycle).

**Event stream cards** (`AuditEventCard.tsx`, 16KB). Each event renders as a card showing: severity dot, timestamp, action badge (allow/deny/quarantine/publish/etc.), human-readable event description, entity reference (job/policy/approval), correlation ID, and actor. The `classifyEvent()` function categorizes events into `safety_decision`, `human_action`, `system_event`, and `access_event`.

**Composable filter bar** (`AuditFiltersBar.tsx`, 22KB). Multi-filter bar with dropdowns for: action type, severity, actor, resource type, category, time range, and free-text search. Filters combine with AND logic. Filter state syncs to URL params via `useSearchParams`.

**Detail panel** (`AuditDetailPanel.tsx`, 13KB). Slide-out panel showing full event detail including: raw payload, snapshot before/after diffs (for mutation events), actor info, resource links, and bundle IDs.

**Saved filters** (`SavedFiltersDropdown.tsx`). Save frequently used filter combinations with a name. Persisted in localStorage.

**Export** (`AuditExport.tsx`, 13KB). Export filtered results as CSV, JSON, or PDF. Includes metadata headers and tamper-evident checksums.

**Integrity verification** (`AuditIntegrityPanel.tsx`, 11KB). SHA-256 chain verification across audit entries. Verifies that no entries have been tampered with by checking the hash chain. Shows verification status and any chain breaks.

**Live tail.** Auto-scroll mode that pins the view to the latest events, similar to `tail -f`. New events appear at the top with a brief highlight animation.

**Pagination.** Cursor-based pagination via `next_cursor` from the API. Configurable page size (25, 50, 100).

**Audit transport badge** (`AuditTransportBadge.tsx`). Shows whether audit events are being transported via the primary or fallback transport (for HA configurations).

**Correlation view.** When viewing a single entity's events, groups all related events by correlation ID. Shows time gaps between events with `formatGap()`. This creates a "lifecycle view" for any job — from submission through safety decision, approval, execution, and output scanning.

#### 4.2 New Features to Add

**Default view presets.** Add a toggle row above the filter bar with quick-access preset filters: "Safety Decisions" (filter to deny/require_approval/throttle), "Approvals" (filter to approval-related actions), "Auth Failures" (filter to auth_failure events), "All" (clear filters). This lets a security reviewer quickly zoom into the event category they care about without manually configuring filters each time.

**High-severity event count badge.** Show a count of high-severity events in the last hour in the page header. This gives immediate context about the current threat/activity level.

**Data sources:** `useAuditLog()`, `useAuditCorrelation()`.
**Files:** `src/pages/AuditLogPage.tsx`, `src/components/audit/*`
**Effort:** ~1 hour for new features

---

### Page 5: Quarantine (New Page)

**Route:** `/security/quarantine`
**Status:** New page — `QuarantinePage.tsx`
**Purpose:** Dedicated view for output-quarantined items. These are agent outputs that the Output Safety scanner flagged (PII, secrets, prompt injection, content policy violations) and held for human review.

#### 5.1 Why Split from DLQ

The current `DLQPage.tsx` mixes two fundamentally different things: operational failures (timeouts, panics, connection errors) and governance quarantine (PII detected, secret detected). For Gartner, a buyer needs to see quarantine as a security feature, not a debugging page. The DLQ already has a `RESULT_FILTERS` array (line 51–56) with an `output_quarantined` option — this split formalizes that filter into a dedicated page.

#### 5.2 Features

**Quarantine queue listing.** Filtered view of DLQ entries where the entry signal contains `output_quarantined` (using the same `normalizeEntrySignal()` logic from `DLQPage.tsx`). Each row shows: severity badge, finding summary, job topic, scanner name, matched output rule, and quarantine duration.

**Findings detail panel.** Split-view panel (same pattern as Approvals) showing the full `OutputSafetyRecord` from the job: the `findings` array with each `OutputFinding` (type, severity, detail, scanner, confidence, matched_pattern, offset, length), the `decision` (QUARANTINE/REDACT), the `reason`, and the `rule_id`. This data comes from `Job.output_safety` on the job associated with the DLQ entry.

**PII/Secret highlighting.** When the finding includes `offset` and `length`, highlight the flagged region in the output payload. This shows exactly what the scanner caught.

**Actions: Release or Confirm.** Two primary actions: (1) "Release Output" — calls the `useReleaseQuarantinedJob()` mutation from `useOutputPolicy.ts`, which releases the quarantined output and resumes the job. (2) "Confirm Quarantine" — marks the entry as confirmed-quarantined (the output stays blocked). Both actions are audited.

**Investigation links.** Each quarantine entry links to: the Job Detail page (to see the full job context), the matched Output Policy Rule (in the Output Rules tab of Policy Studio), and the Audit Trail lifecycle view for this job's trace.

**Remediation drawer.** Reuses the `RemediateDrawer` component from `src/components/jobs/RemediateDrawer.tsx`. After confirming a quarantine, the user can submit a remediation job that retries the work with modified parameters.

**Data sources:** `useDLQ()` (filtered to quarantined), `useJob()` (for output_safety record), `useReleaseQuarantinedJob()`, `useOutputFindings()`.
**File:** `src/pages/QuarantinePage.tsx` (new)
**Effort:** ~3–4 hours

---

### Page 6: Safety Controls (New Wrapper Page)

**Route:** `/security/safety` with `#input` and `#output` hash tabs
**Status:** New wrapper — wraps existing `InputSafetySettings.tsx` and `OutputSafetySettings.tsx`
**Purpose:** Centralize the Safety Kernel configuration under Security instead of hiding it in Settings.

#### 6.1 Input Safety Tab

**Existing features from `InputSafetySettings.tsx` (10KB):**

Fail-mode configuration: toggle between fail-closed (deny all jobs when Safety Kernel is unreachable) and fail-open (allow jobs when kernel is unreachable). This is the most critical security setting in the entire dashboard.

Safety Kernel connection status: shows whether the kernel is connected, last ping time, and connectivity health.

Fail-open counter (`FailOpenCounter.tsx`): tracks how many times the system has fallen back to fail-open mode in the last 24 hours. This is an important operational metric — even one fail-open event could be a security concern.

Configuration fields: scan timeout, max payload size, topic overrides (per-topic fail mode settings).

Audit log of changes: shows recent configuration changes with actor and timestamp.

#### 6.2 Output Safety Tab

**Existing features from `OutputSafetySettings.tsx` (33KB — the largest settings page):**

Output scanning toggle: enable/disable output scanning globally.

Fail-mode configuration: same fail-closed/fail-open toggle as input, but for the output scanning pipeline.

Scanner status: shows each active scanner (PII Scanner, Secret Detector, Prompt Injection Detector, Content Policy) with their enabled/disabled status.

Output policy rules (`OutputRulesTab.tsx`, 21KB): manage the rules that determine what to quarantine, redact, or allow. Each rule has match criteria, decision type (ALLOW/QUARANTINE/REDACT), and a reason.

Output rule detail (`OutputRuleDetail.tsx`): slide-out editor for individual output rules with match criteria builder.

Statistics: 24h scan count, quarantine count, average latency, last scan timestamp from `/api/v1/output-policy/stats`.

Topic overrides: per-topic scanner configuration (enable/disable specific scanners for specific topics).

Scan timeout and max payload configuration.

#### 6.3 What Changes

The only structural change is relocation: these pages move from being sub-routes of `/settings` to being tabs under `/security/safety`. The `SettingsLayout.tsx` sub-route definitions remove the Input Safety and Output Safety entries. A redirect from `/settings/input-safety` to `/security/safety#input` preserves any bookmarks.

The Settings page should show a note: "Input Safety and Output Safety controls have moved to Security → Safety Controls."

**Files:** `src/pages/SafetyControlsPage.tsx` (new wrapper), existing `InputSafetySettings.tsx` and `OutputSafetySettings.tsx` (imported as tab content)
**Effort:** ~1.5 hours

---

## OPERATIONS CENTER

---

### Page 7: Runs (New Page)

**Route:** `/runs`
**Status:** New page — `RunsPage.tsx`
**Purpose:** Top-level listing of all workflow runs across all workflows. Currently, runs are only visible nested inside a workflow detail page — you have to navigate to a specific workflow to see its runs. This page gives a unified "what's executing right now" view with a governance lens.

#### 7.1 Features

**Run listing table.** Columns: status badge, workflow name (linked to workflow detail), started timestamp, duration, job progress (completed/total), and a governance outcome column.

**Governance outcome column.** This is the key differentiator from a generic run list. For each run, roll up the governance state of its child jobs into a single indicator:
- **Clean** (green ✓) — all jobs allowed, no denies, no pending approvals, no quarantines.
- **Approval Pending** (yellow ⏳) — at least one job in the run is awaiting approval.
- **Quarantined** (red ⚠) — at least one job output was quarantined.
- **Denied** (red ✕) — at least one job was denied by policy.

The computation requires fetching jobs for each run (via `WorkflowRun.steps[].status` and checking for `approval_required`, `denied`, `output_quarantined` statuses) or aggregating from the job list filtered by `workflowRunId`.

**Filters.** Filter by status (running/succeeded/failed/blocked), workflow, governance outcome, and time range.

**Run detail panel.** Split-view panel showing: governance outcome explanation (which specific jobs are pending/denied/quarantined), job list with status and decision badges, run timeline, and investigation links.

**Data sources:** `useWorkflows()` to get workflow metadata, plus a new query that lists runs across all workflows. The `WorkflowRun` type (line 352–376) has all needed fields. The existing `RunDetailPage.tsx` (15KB) and `RunHistoryTable.tsx` components provide reference implementations for run display.

**Note:** The `useRunStream()` hook (7.5KB) provides real-time run status updates via WebSocket — use this for live status updates in the run list.

**File:** `src/pages/RunsPage.tsx` (new)
**Effort:** ~4 hours

---

### Page 8: Jobs

**Route:** `/jobs` (listing) and `/jobs/:id` (detail)
**Status:** Existing — `JobsPage.tsx` (14KB) and `JobDetailPage.tsx` (18KB)
**Purpose:** View all jobs with their safety decisions. The detail page shows the full lifecycle of a single job.

#### 8.1 Jobs Listing (`JobsPage.tsx`)

**Current features (keep all):**

Job table with columns: status badge, topic, pool, created timestamp, duration, and decision badge (allow/deny/require_approval/throttle). The `JobStatusBadge` component renders status with appropriate colors.

Filter bar (`JobFiltersBar.tsx`, 13KB): composable filters for status, pool, decision type, risk tags, workflow, and time range. URL-synced.

Job submit drawer (`JobSubmitDrawer.tsx`, 16KB): slide-out form to submit a new job with topic, prompt, priority, capabilities, risk tags, labels, and advanced options (pack_id, memory_id, idempotency_key, max_total_tokens).

Pagination with cursor-based loading.

**New feature — Decision column emphasis.** The decision badge should be more prominent in the table row — currently it can be small text. Make the `safetyDecision.type` badge render with the same visual weight as the status badge so you can scan the column and immediately see the governance pattern.

#### 8.2 Job Detail (`JobDetailPage.tsx`)

**Current tab order:** Overview → Memory → Artifacts (dynamically shown based on data availability).

**Current Overview tab content:** Lifecycle state machine visualization (`JobStateMachine.tsx`), safety explain card (`SafetyExplainCard.tsx`), output safety findings (for quarantined jobs), job metadata (topic, pool, capabilities, risk tags, labels, timestamps), error information, and detailed field listing.

**SafetyExplainCard** (`SafetyExplainCard.tsx`, 3.5KB): shows the safety decision type, reason, matched rule, eval time, and eval path. This is already a well-built component.

**Job actions** (`JobActions.tsx`, 6KB): cancel, retry, remediate. The `RemediateDrawer` lets you resubmit a failed/denied job with modified parameters.

**Output safety findings** (`useOutputFindings()` hook): for quarantined jobs, shows the full `OutputSafetyRecord` with findings, release button (`useReleaseQuarantinedJob()`), and remediation option.

**Memory panel** (`MemoryPanel.tsx`, 8KB): renders the job's context and result memory entries with role-based message formatting.

**Artifact panel** (`ArtifactPanel.tsx`, 17KB): shows job artifacts with content type detection, size, and download.

**New features to add:**

**Reorder tabs: Decision → Timeline → Input/Output → Memory → Artifacts.** The "Decision" tab replaces "Overview" as the first tab. It leads with a large decision badge (ALLOW/DENY/APPROVAL/THROTTLE) and the SafetyExplainCard content, making the governance outcome the hero of the page.

**Investigation links on Decision tab.** Link to: the policy rule that matched (`/policies/rules#{ruleId}`), the approval if one was created (`/approvals?id={approvalRef}`), and the full audit lifecycle (`/audit?correlationId={traceId}`).

**Timeline tab.** Dedicated tab showing the `JobStateMachine` visualization and the state transition timeline, which currently lives on the Overview tab.

**Data sources:** `useJob()`, `useJobDecisions()`, `useOutputFindings()`, `useReleaseQuarantinedJob()`.
**Files:** `src/pages/JobsPage.tsx`, `src/pages/JobDetailPage.tsx`
**Effort:** ~2 hours for new features

---

### Page 9: Agent Fleet

**Route:** `/agents`
**Status:** Existing — `AgentsPage.tsx` (13KB)
**Purpose:** View and manage the worker agents that execute jobs.

#### 9.1 Current Features (Keep All)

**Agent listing.** Table of all registered workers showing: name, pool, status (online/offline/draining), active jobs, capacity, capabilities, last heartbeat, and uptime.

**Pool grouped view** (`PoolGroupedView.tsx`): groups agents by pool with collapsible sections showing pool totals.

**Worker detail drawer** (`WorkerDetailDrawer.tsx`, 10KB): slide-out drawer showing full worker details including version, address, region, type, CPU/GPU/memory load, and capabilities list.

**Snapshot writer badge** (`SnapshotWriterBadge.tsx`): shows which agent holds the snapshot writer lock in HA configurations.

#### 9.2 New Features to Add

**Top metrics strip.** Four cards above the table: Online count (of total), Pool count, Active Jobs (sum across all agents), Total Capacity (sum of all capacity slots). These provide an instant fleet health summary.

**Move PoolUtilizationGrid here.** The `PoolUtilizationGrid` component from the current HomePage shows a heatmap of pool utilization. This belongs on the Agent Fleet page, not the homepage. Import and render it above the agent table.

**Data sources:** `useWorkers()`, `useStatus()`.
**File:** `src/pages/AgentsPage.tsx`
**Effort:** ~1 hour

---

### Page 10: Failures

**Route:** `/failures` (or keep `/dlq` for backward compatibility with a redirect)
**Status:** Existing — `DLQPage.tsx` (877 lines), renamed and filtered
**Purpose:** Operational failure queue — jobs that failed due to timeouts, panics, connection errors, or policy denials. Excludes quarantined outputs (those go to Quarantine).

#### 10.1 Current Features (Keep, With Filter)

The `DLQPage.tsx` already has the `RESULT_FILTERS` array (line 51–56) with options for All, Denied, Failed, and Quarantined. The Failures page simply applies a default filter that excludes `output_quarantined` entries.

**Entry listing.** Each DLQ entry shows: status badge, job ID, original topic, error message, retry count/max retries, failed-at timestamp, and time since failure.

**Retry attempts panel** (`RetryAttemptsPanel.tsx`): expandable section showing each retry attempt with timestamp and error.

**Row actions** (`DLQActions.tsx`): retry, delete, and details for each entry. Retry triggers re-dispatch of the job.

**Time range presets.** Filter by 1h, 24h, 7d, 30d, or all time.

**Free-text search.** Debounced search across job IDs, topics, and error messages.

**Bulk actions.** Select multiple entries for bulk retry or bulk delete. Requires confirmation dialog (`ConfirmDialog.tsx`).

**Expandable row detail.** Each row expands to show: full error message, retry attempts list, job metadata, and the original topic.

**Data freshness indicator** (`DataFreshness.tsx`): shows when the data was last refreshed.

#### 10.2 New Features

**Rename label.** In the sidebar, change "Dead Letters" to "Failures." The route can stay `/dlq` for backward compatibility, or migrate to `/failures` with a redirect.

**Cross-reference note.** Show a subtle note at the top of the page: "Quarantined outputs are in Security → Quarantine." This teaches users the new structure and prevents confusion about where their quarantined items went.

**Default filter.** On page load, default the result filter to exclude `output_quarantined`. Users can still toggle the "Quarantined" filter option if they want to see everything in one place.

**Data sources:** `useDLQ()`, `useRetryDLQ()`, `useDeleteDLQ()`.
**File:** `src/pages/DLQPage.tsx` (renamed/refactored)
**Effort:** ~1 hour

---

## BUILD CENTER

---

### Page 11: Workflows

**Route:** `/workflows` (listing), `/workflows/new` (create), `/workflows/:id` (detail)
**Status:** Existing — `WorkflowsPage.tsx` (6.5KB), `WorkflowCreatePage.tsx` (5KB), `WorkflowDetailPage.tsx` (18KB)
**Purpose:** Define and monitor multi-step job workflows with DAG-based orchestration.

#### 11.1 Workflow Listing (`WorkflowsPage.tsx`)

Table of all workflows showing: name, step count, last run timestamp, success rate sparkline (`SuccessSparkline.tsx`), active runs count, trigger type, and status.

Active runs strip (`ActiveRunsStrip.tsx`): shows currently running workflow runs with live status.

Workflow template cards (`WorkflowTemplateCard.tsx`): starter templates for common workflow patterns.

#### 11.2 Workflow Create (`WorkflowCreatePage.tsx`)

Form-based workflow creation with: name, description, timeout, input schema selection, and step configuration.

#### 11.3 Workflow Detail (`WorkflowDetailPage.tsx`, 18KB)

**DAG visualization** (`WorkflowCanvas.tsx`, 15KB + `dag/` directory, 83KB): visual node-and-edge graph showing workflow steps, their dependencies, and execution status during runs. Supports the `WorkflowBuilder.tsx` for visual editing.

**Node config panel** (`NodeConfigPanel.tsx`, 39KB): the largest component in the codebase. Full configuration editor for each workflow step including: step type, topic routing, input/output schemas, retry configuration, timeout, delay, conditional execution, and error handling.

**Run history table** (`RunHistoryTable.tsx`): paginated table of past workflow runs with status, duration, and drill-down to run detail.

**Run visualization** (`RunVisualization.tsx`): live DAG view during execution showing which steps have completed, which are running, and which are pending.

**Gantt timeline** (`GanttTimeline.tsx`): time-based view of step execution showing parallelism and bottlenecks.

**Run now modal** (`RunNowModal.tsx`, 14KB): modal for triggering a workflow run with input parameters, schema-based form, and dry-run option.

**Schema form** (`SchemaForm.tsx`): dynamically generates input forms from JSON schemas.

**Delay timer badge** (`DelayTimerBadge.tsx`): shows countdown for steps with `delay_sec` or `delay_until`.

**Builder sidebar** (`BuilderSidebar.tsx`): step palette for drag-and-drop workflow construction.

**Move ActiveWorkflowCards here.** The `ActiveWorkflowCards` component from the current HomePage shows running workflows with their status. This belongs on the Workflows listing page, not the homepage.

**Data sources:** `useWorkflows()`, `useRunStream()`.
**Files:** `src/pages/Workflow*.tsx`, `src/components/workflow/*`, `src/components/workflows/*`
**Effort:** ~30 minutes (just moving ActiveWorkflowCards)

---

### Page 12: Packs

**Route:** `/packs`
**Status:** Existing — `PacksPage.tsx` (8.5KB)
**Purpose:** Install and manage governance-aware capability packs.

#### 12.1 Current Features (Keep All)

**Installed packs listing.** Table of installed packs showing: name, version, status, capabilities count, pool assignment, installed date, and installed-by user.

**Pack detail** (`PackDetail.tsx`, 11KB): slide-out panel showing full pack metadata including description, author, homepage, license, capabilities list, configuration, resources, and SHA-256 hash.

**Marketplace browser** (`MarketplaceBrowser.tsx`, 6.5KB): browse available packs from configured catalogs (`MarketplaceCatalog` type). Shows pack title, description, author, version, and install button. Marketplace data comes from the `/api/v1/packs/marketplace` endpoint returning `MarketplaceResponse`.

**Pack validation.** Backend validates pack manifests on install (improved in PR #101).

**Data sources:** `usePacks()`.
**File:** `src/pages/PacksPage.tsx`, `src/components/packs/*`
**Effort:** No changes needed

---

### Page 13: Schemas

**Route:** `/schemas` and `/schemas/:id`
**Status:** Existing — `SchemasPage.tsx` (8.5KB), `SchemaDetailPage.tsx` (1.5KB). Currently NOT in the sidebar navigation.
**Purpose:** Manage JSON schemas for workflow step inputs and outputs.

#### 13.1 Current Features (Keep All)

**Schema listing.** Table of registered schemas showing: name, version, field count, created/updated timestamps, and usage count (how many workflow steps reference this schema).

**Schema register form** (`SchemaRegisterForm.tsx`): form to register a new schema by name, version, and JSON schema definition.

**Schema viewer** (`SchemaViewer.tsx`): renders schema fields with their types, required flags, and descriptions.

**Schema detail page** (`SchemaDetailPage.tsx`): shows full schema definition with field listing and referencing workflows.

#### 13.2 What Changes

**Add to navigation.** The only change is adding the Schemas route to the sidebar under the BUILD section. The page exists and works — it just has no nav entry. This is a one-line addition to the `navItems` structure in `AppShell.tsx`.

**Data sources:** `useSchemas()`.
**Files:** `src/pages/SchemasPage.tsx`, `src/components/schemas/*`
**Effort:** 1 minute (add nav entry)

---

## SETTINGS

---

### Page 14: Settings

**Route:** `/settings` with sub-routes `/settings/health`, `/settings/keys`, `/settings/users`, `/settings/notifications`, `/settings/environments`, `/settings/mcp`, `/settings/config`
**Status:** Existing — `SettingsLayout.tsx` (5.5KB) + 7 sub-route pages
**Purpose:** System configuration, infrastructure health, user management, and integrations.

#### 14.1 Settings Layout (`SettingsLayout.tsx`)

Sidebar navigation within settings showing sub-route links. The layout wraps each sub-page with consistent padding and breadcrumbs.

**Change:** Remove "Input Safety" and "Output Safety" entries from the settings sub-navigation. Add a note linking to their new location at Security → Safety Controls.

#### 14.2 System Health (`SettingsHealthPage.tsx` + `SystemHealthTab.tsx`, 18KB)

System status overview: NATS connection, Redis status, worker count, uptime, and version.

**Receives from HomePage:** The Workers, NATS, Redis, Uptime metric cards move here. The `CircuitBreakerCard` and `RateLimiterModeBadge` also move here.

Dependency graph visualization: shows the relationships between system components (gateway, safety kernel, NATS, Redis, workers).

Setup checklist (`SetupChecklist.tsx`): guided onboarding wizard for new installations.

Diagnostics panel with replica table (`ReplicaTable.tsx`) for HA configurations.

HA config section (`HAConfigSection.tsx`): horizontal scaling configuration.

Lock inspector (`LockInspector.tsx`): shows distributed locks held in Redis via `useAdminLocks()`.

Circuit breaker panel (`CircuitBreakerPanel.tsx`): circuit breaker status and configuration.

Effective config panel (`EffectiveConfigPanel.tsx`): shows the resolved configuration from all sources.

#### 14.3 API Keys (`SettingsKeysPage.tsx`, 21KB)

CRUD for API keys: create, revoke, regenerate. Shows key prefix (masked), scopes, created date, last used, usage count, and expiration. Supports multi-tenant API keys.

#### 14.4 Users & Access (`SettingsUsersPage.tsx` + `UsersTab.tsx`, 24KB)

User management: list users, create/invite users, edit roles, reset passwords, delete users. RBAC permission matrix showing role-to-permission mappings.

SAML configuration (`SamlConfigPanel.tsx`): SSO setup for enterprise.

OAuth configuration (`OAuthConfigPanel.tsx`): OIDC provider configuration.

Session management (`SessionManagement.tsx`): view and terminate active sessions.

Change password section (`ChangePasswordSection.tsx`).

#### 14.5 Notifications (`SettingsNotificationsPage.tsx`, 6.5KB)

Notification channel management: create/edit channels (email, Slack, webhook, PagerDuty) via `NotificationChannelModal.tsx`.

Notification rules (`NotificationRulesTable.tsx` + `NotificationRuleModal.tsx`): configure which events trigger which channels with throttling and mute schedules.

Channel cards (`NotificationChannelCard.tsx`): visual cards showing channel status, last sent, and error state.

#### 14.6 Environments (`SettingsEnvironmentsPage.tsx`, 8.5KB)

Environment management: list environments (production, staging, dev) with status indicators. Environment configuration editor (`EnvironmentConfigEditor.tsx`). Promotion drawer (`PromotionDrawer.tsx`): promote configuration from one environment to another.

#### 14.7 MCP Server (`SettingsMcpPage.tsx`, 24KB)

MCP (Model Context Protocol) server configuration: enable/disable, transport selection (HTTP/stdio/both), port, auth requirements, allowed origins.

Tool management: enable/disable individual MCP tools with input schema display.

Resource management: enable/disable MCP resources with URI, description, and mime type.

MCP status: running state, connected clients, uptime, enabled tools/resources count.

#### 14.8 Configuration (`SettingsConfigPage.tsx`, 18KB)

General configuration: safety stance (permissive/balanced/strict), approval timeout, auto-deny on timeout, log retention, audit retention, DLQ retention, rate limits, concurrent job limits, WebSocket connection limits.

Maintenance mode (`MaintenanceModeSection.tsx`, 16KB): enable/disable maintenance mode, scheduling, history, and maintenance windows.

**Data sources:** `useSettings()` (31KB hook), `useStatus()`, `useAuth()`, `useAuthConfig()`, `useSetupStatus()`.
**Files:** `src/pages/Settings*.tsx`, `src/components/settings/*`
**Effort:** ~30 minutes (remove safety pages from layout, add redirect)

---

## CROSS-CUTTING FEATURES

---

### Fix: Search 404

**Problem:** The header search input in `AppShell.tsx` (line 219 area) and the "Search" button in `QuickActions.tsx` (line 39) both navigate to `/search`, which doesn't exist. Meanwhile, a full `CommandPalette.tsx` (19KB) exists with fuzzy search across jobs, workflows, packs, policies, and settings — but it's only accessible via ⌘K.

**Fix:** Wire both the search input and the QuickActions search button to call `setCommandOpen(true)` instead of navigating. The `setCommandOpen` function already exists in `useUiStore` and is already imported in `AppShell.tsx` (line 56).

**Files:** `AppShell.tsx` (header search handler), `QuickActions.tsx` (search button handler)
**Effort:** 20 minutes

---

### Investigation Links Pattern

Every detail panel across the dashboard (Job Detail, Approval Detail, Quarantine Detail, Audit Detail, Run Detail) should provide cross-reference links to related entities. This creates an investigation flow where any entry point leads to the full governance lifecycle.

**Standard link set per entity type:**

For a **Job**: link to Policy Rule, Approval (if exists), Audit Lifecycle, Workflow Run (if exists), Quarantine (if quarantined).

For an **Approval**: link to Job Detail, Policy Rule, Audit Lifecycle.

For a **Quarantine entry**: link to Job Detail, Output Policy Rule, Audit Lifecycle.

For an **Audit event**: link to the referenced Resource (job/policy/approval), Correlation chain.

For a **Run**: link to Workflow Definition, child Jobs, Audit Lifecycle.

**Implementation:** Create a shared `InvestigationLinks` component that accepts an entity type and relevant IDs, and renders the appropriate link set. Each link uses React Router's `Link` component for client-side navigation.

---

### Attention Badge on Security Section

The "SECURITY" section header in the sidebar should show a count badge when there are items needing attention: pending approvals + active quarantine items. This is computed from the same queries already running for the nav badges. The badge draws the eye to the Security section and creates urgency.

---

## IMPLEMENTATION PRIORITY

**Phase 1 — Before Gartner (highest leverage, ~18 hours):**
1. Sidebar restructure in `AppShell.tsx` (2h)
2. `SecurityOverviewPage.tsx` — new landing page (4–5h)
3. Fix `/search` 404 (20min)
4. Rename "Dead Letters" → "Failures" (1min)
5. Add Schemas to navigation (1min)
6. `QuarantinePage.tsx` — split from DLQ (3–4h)
7. `SafetyControlsPage.tsx` — wrap input/output safety (1.5h)
8. `RunsPage.tsx` — new runs listing (4h)

**Phase 2 — Before customer demos (~6 hours):**
1. Reorder Job Detail tabs (Decision first) (2h)
2. Investigation links on all detail panels (2h)
3. Move HomePage widgets to their new homes (1h)
4. Default audit view presets (1h)

**Phase 3 — Ongoing polish:**
1. Framework alignment labels on policy rules
2. Attention badge on Security section header
3. Agent Fleet metrics strip
4. Governance outcome column optimization on Runs page
