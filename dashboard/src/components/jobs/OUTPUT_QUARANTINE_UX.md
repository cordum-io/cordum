# Output Quarantine UX Spec

## Scope
This document defines the dashboard UX for jobs with `OUTPUT_QUARANTINED` state and output-policy findings.

## 1. Job Detail (Quarantined)
- Header state: show `quarantined` status badge and warning tone.
- Summary card:
  - Decision: `QUARANTINE` or `REDACT`
  - Reason, matched rule id, policy snapshot id
  - Check phase (`sync` or `async`)
- Findings panel:
  - Table columns: `severity`, `type`, `detail`, `scanner`, `confidence`, `matched_pattern`
  - Default sort: severity desc, confidence desc.
  - Empty-state text: `No findings recorded`.
- Action group:
  - `Release` button: sends admin action to release/retry quarantined job.
  - `Confirm Quarantine` button: confirms quarantine handling and keeps item in DLQ workflow.
  - Both actions must require confirmation modal and emit audit event payload.
- Redaction diff viewer:
  - If `original_ptr` + `redacted_ptr` are present, show before/after diff section.
  - If only one pointer exists, show whichever payload is available with warning badge.

## 2. Jobs List
- Add `Quarantined` filter chip mapped to `OUTPUT_QUARANTINED` backend state.
- Row treatment:
  - Orange left border on quarantined rows.
  - Inline badge for finding count when `output_safety.findings` exists.
- List header KPI:
  - Quarantined count badge next to total jobs.

## 3. Policy Studio: Output Rules Tab
- Add `Output Rules` tab in policy editor.
- Rule card fields:
  - id, enabled state, decision (`ALLOW`, `QUARANTINE`, `REDACT`), match criteria summary.
- Actions per rule:
  - enable/disable toggle
  - dry-run test modal with sample output content and metadata
  - last-triggered and hit-count signals (when available)

## 4. Approvals Integration
- Add optional `Quarantine Review` queue section beside approvals.
- Queue item displays: job id, topic, rule id, top finding, age.
- Review actions:
  - `Release`
  - `Confirm`
- Access control: admin-only by default.

## 5. Audit Trail
- Add `output_policy_check` event type.
- Event payload should include:
  - job id, decision, rule id, reason, findings count, actor
  - pointer references (`original_ptr`, `redacted_ptr`) when present
- UI treatment:
  - Highlight quarantine events with warning color.
  - Add event filter chip for `output_policy_check`.

## 6. Sidebar Indicator
- Show quarantined count badge in sidebar navigation.
- Badge rules:
  - hidden when zero
  - orange for non-zero
  - tooltip: `Jobs blocked by output policy`.

## Interaction Notes
- All quarantine actions should be optimistic in UI but roll back on API failure.
- Action errors should include backend message and a retry CTA.
- Quarantine state is terminal in scheduler; no auto-transition back to running.
