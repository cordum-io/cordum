# Policy Studio Telemetry (Input > Global)

This document describes the minimal, privacy-safe telemetry emitted by the Input Policy → Global hybrid editor.

## Enablement

Telemetry is disabled by default.

Set:

```bash
VITE_POLICY_STUDIO_TELEMETRY=true
```

When enabled, the client dispatches browser events to:

`cordum:policy-studio-telemetry`

## Events

### 1) `policy_editor_advanced_toggled`
- Trigger: Advanced toggle opened/closed.
- Payload:
  - `scope`: `"input_global"`
  - `advancedOpen`: boolean
  - `configuredAdvancedCount`: number
  - `decision`: current decision string

### 2) `policy_editor_section_viewed`
- Trigger: first view per editor session for:
  - `advanced`
  - `constraints`
  - `remediations`
- Payload:
  - `scope`: `"input_global"`
  - `section`: `"advanced" | "constraints" | "remediations"`
  - `decision`: current decision string
  - `configuredAdvancedCount`: number

### 3) `policy_editor_saved_with_advanced_fields`
- Trigger: rule saved with one or more configured advanced groups.
- Payload:
  - `scope`: `"input_global"`
  - `configuredAdvancedCount`: number
  - `decision`: current decision string
  - `clearRemediationsOnSave`: boolean

### 4) `policy_editor_saved_with_hidden_advanced_unviewed`
- Trigger: rule saved with configured advanced groups when Advanced was never opened in that editor session.
- Payload:
  - `scope`: `"input_global"`
  - `configuredAdvancedCount`: number
  - `decision`: current decision string
  - `clearRemediationsOnSave`: boolean

## Privacy constraints

Telemetry payloads do **not** include:
- rule IDs
- labels
- capabilities/topics/risk tags values
- MCP values
- remediation contents
- YAML content
- secrets or raw policy payloads
