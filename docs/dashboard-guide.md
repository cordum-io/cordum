# Dashboard Guide

The Cordum Dashboard is a browser-based control plane for managing jobs, workflows, safety policies, approvals, and system configuration. It connects to the Cordum API gateway and provides real-time visibility into your AI agent orchestration platform.

**Access**: Open `http://localhost:5173` (dev) or your deployed dashboard URL. Authentication is required via API key, password, or SSO depending on your configuration (see [Settings > Users & Access](#users--access---settingsusers)).

---

## Navigation

The sidebar provides access to all major sections:

| # | Route | Label | Badge |
|---|-------|-------|-------|
| 1 | `/` | Overview | |
| 2 | `/jobs` | Jobs | |
| 3 | `/workflows` | Workflows | |
| 4 | `/agents` | Agent Fleet | |
| 5 | `/approvals` | Approvals | Pending count (yellow) |
| 6 | `/policies` | Policy Studio | |
| 7 | `/packs` | Packs | |
| 8 | `/dlq` | Dead Letters | Entry count (red) |
| 9 | `/audit` | Audit Log | |
| --- | --- | *separator* | |
| 10 | `/settings` | Settings | Fixed bottom |

**Command Palette**: Press `Cmd+K` (Mac) or `Ctrl+K` (Windows/Linux) to open the command palette for quick navigation and search across all resource types.

**Global Search**: The header search bar searches across runs, workflows, packs, and jobs.

Legacy routes `/pools` and `/system` redirect to `/agents` and `/settings/health` respectively.

---

## Pages

### Overview (`/`)

The command center displays real-time system health and activity at a glance.

**Metric cards (top row)**:
- **Workers** — online worker count
- **Active Jobs** — currently running jobs
- **NATS** — connection status (connected/disconnected)
- **Redis** — store status (OK/degraded)
- **Uptime** — system uptime (formatted as days/hours/minutes)

**Dashboard panels**:
- **Quick Actions** — fast access to common operations (submit job, create workflow, etc.)
- **Safety Decision Feed** — recent allow/deny/approval decisions
- **Job Pipeline Funnel** — visual breakdown of job stages (pending, dispatched, running, succeeded, failed)
- **Pool Utilization Heatmap** — grid showing active/capacity per worker pool
- **DLQ Summary** — dead letter queue count and details
- **Event Timeline** — chronological stream of system events
- **Active Workflows** — currently running workflows with progress

---

### Jobs (`/jobs`)

Browse, filter, sort, and submit agent jobs.

**Job list table** with sortable columns:
- **ID** — first 8 characters (monospace)
- **Topic** — job type/category (e.g., `job.default`, `job.mock-bank.review`)
- **State** — lifecycle status badge (pending, dispatched, running, succeeded, failed, denied, cancelled, output_quarantined)
- **Safety Decision** — allow/deny/approval/throttle badge
- **Pool** — assigned worker pool
- **Duration** — execution time
- **Updated** — relative timestamp ("5m ago")

**Filters**: status, topic, date range (1h/24h/7d/30d), pool. Filters persist in URL for bookmarking.

**Pagination**: cursor-based with configurable rows per page (10, 25, 50, 100).

**New Job button**: Opens the [Job Submit Drawer](#how-to-submit-a-job) for creating jobs directly from the dashboard.

**Data freshness**: timestamp indicator with manual refresh button.

#### Job Detail (`/jobs/:id`)

Click any job row to view its full detail page.

**Header**: Job ID, topic, pool, duration, status badge, safety decision badge.

**Tabs**:
- **Overview** — lifecycle state machine diagram, safety evaluation card (decision + reason + matched rule), timeline with state transitions and durations, workflow context (if part of a workflow run)
- **Memory** — stored context data from the job's `contextPtr` (visible when context exists)
- **Artifacts** — original output, redacted output (if quarantined), retry attempts with errors

**Actions**:
- **Cancel** — cancel a running job
- **Retry** — retry a failed job via DLQ
- **Remediate** — opens remediation drawer for denied/quarantined jobs (resubmit with modified inputs)

---

### Workflows (`/workflows`)

Manage workflow templates and monitor execution runs.

**Active Runs Strip**: real-time list of currently executing workflow runs at the top.

**Templates grid**: searchable card grid (responsive 1/2/3 columns) showing each workflow's name, run count, created date, and last modified date.

**Card actions**:
- **Run Now** — start a new execution
- **Edit** — open in the workflow builder
- **View** — navigate to workflow detail

**Create Workflow** button links to the visual builder at `/workflows/new`.

#### Workflow Detail (`/workflows/:id`)

View a workflow's definition (step list/DAG), execution history, and individual run details.

- Step dependencies visualized as a directed acyclic graph (DAG)
- Run list with status badges and timing
- Trigger new run or edit workflow

#### Run Detail (`/workflows/:id/runs/:runId`)

Step-by-step execution overlay showing:
- Each step's status (pending, running, succeeded, failed)
- Step inputs and outputs
- Error details for failed steps
- Timing per step
- Rerun from a specific step

---

### Agent Fleet (`/agents`)

Monitor worker pools, individual agent status, and capacity.

**View toggle**: table view (default) or card view (grouped by pool).

**Filters**: pool dropdown, status filter (online, offline, draining).

**Table columns** (sortable):
- **Name** — worker identifier
- **Pool** — pool assignment
- **Status** — online/offline/draining badge
- **Capabilities** — blue info badges (e.g., `code_execution`, `web_browse`)
- **Active / Capacity** — current load vs max slots (red text when at capacity)
- **Heartbeat** — last seen ("5m ago")
- **Uptime** — formatted as "2d 3h" or "45m"
- **Version** — worker version

**Card view**: workers grouped by pool with compact metric cards.

**Worker detail drawer**: click any worker to see full metrics, capabilities, resource usage, and job slots.

---

### Approvals (`/approvals`)

Manage the human-in-the-loop approval queue for jobs flagged by safety policies.

**Tabs**: Queue (pending approvals) | History (resolved approvals)

**Queue tab**:

*Stats strip*: pending count, critical count, average wait time, SLA breaches.

*Filters*: urgency (all/normal/aging/critical), workflow, rule, risk tags, assignment, sort order (wait time, creation, updated).

*Approval cards* show:
- Urgency badge (pulsing red for critical)
- Wait time ("Waiting 5m 23s")
- Human-readable summary
- Matched policy rule
- Capability tags (blue) and risk tags (orange)
- Selection checkbox for batch actions

*Detail panel* (right side on wider screens): full approval detail with approve/reject buttons, optional comment field, and required reason for rejection.

*Batch actions toolbar* (when items selected): Approve All, Reject All, Clear Selection. Role-gated to admin/operator.

**History tab**: resolved approvals with decision, actor, reason, and timing.

All filter and selection state persists in URL parameters.

---

### Policy Studio (`/policies`)

Create, test, and manage safety policies that govern job execution decisions.

**Sub-routes**:

| Route | Page | Purpose |
|-------|------|---------|
| `/policies` | Overview | Policy summary and health |
| `/policies/rules` | Rules | Input and output rule lists |
| `/policies/rules/new` | Builder | Create a new rule |
| `/policies/rules/:id` | Builder | Edit existing rule |
| `/policies/simulator` | Simulator | Test policies against sample jobs |
| `/policies/history` | History | Policy version history |
| `/policies/analytics` | Analytics | Policy effectiveness metrics |

**Rules page** has two tabs:
- **Input Rules** — match job characteristics (capabilities, risk tags, topic) to decisions (allow/deny/require_approval/throttle)
- **Output Rules** — match LLM output patterns to quarantine/allow/redact decisions

**Builder page** offers two editing modes:
- **Visual** — point-and-click rule builder with condition groups, decision picker, and test-on-sample
- **YAML** — raw policy bundle editor with syntax highlighting

See [docs/safety-kernel.md](safety-kernel.md) for the policy engine internals and [docs/output-policy.md](output-policy.md) for output scanning configuration.

---

### Packs (`/packs`)

Browse and manage model packs — bundles of capabilities, policy fragments, and worker configurations.

**Features**:
- Pack catalog with search
- Pack metadata (name, version, author, capabilities, description)
- Install/uninstall actions
- Verification status (SHA-256 integrity)
- Policy fragments included in each pack

See [docs/pack.md](pack.md) for the pack format specification and CLI commands.

---

### Dead Letters (`/dlq`)

Recover or debug failed, denied, and quarantined jobs.

**Filters**: topic search (debounced), time range presets (1h/24h/7d/30d/all), result type (all/denied/failed/quarantined), clear filters button.

**Table columns** (sortable):
- **Job ID** — first 8 characters
- **Reason** — error message (expandable for long text)
- **Attempts** — retry count / max retries
- **Topic** — original job topic
- **Failed At** — relative timestamp

**Expandable rows** show:
- Retry attempt history (attempt number, error, timestamp)
- For quarantined entries: output safety details (matched rule, findings list, original/redacted pointers)
- **Release Output** button (admin only) for quarantined entries

**Batch actions** (when items selected): Retry All, Delete All (with confirmation dialog).

**Pagination**: cursor-based with configurable rows per page.

---

### Audit Log (`/audit`)

Track all system events for compliance, troubleshooting, and forensics.

**View modes**: Stream (chronological list, default) | Timeline (time-series chart)

**Live tail**: toggle real-time event streaming (30-second polling). Shows a floating "N new events" button when scrolled away. Auto-pauses when the page is hidden.

**Filters** (all URL-synced for bookmarking):
- Event type (multi-select: allow, deny, auth_failure, etc.)
- Actor (username or service)
- Resource type (job, workflow, policy, etc.)
- Resource ID
- Severity (low/medium/high/critical)
- Outcome (success/failure/pending)
- Time range (preset or custom from/to)
- Text search

**Saved filters**: save and load filter combinations from a dropdown.

**Stats summary**: total events matching filters, high-severity count, safety decision count.

**Event cards** show: event type with icon, actor, resource, action/outcome, timestamp. Search matches are highlighted.

**Correlation view**: click a resource link to see all events for that resource with annotated time gaps between events.

**Detail panel**: click any event card for full payload (JSON), request/response data, IP address, user agent, and related resource links.

**Export**: CSV download of filtered events.

See [docs/api-reference.md](api-reference.md) for the audit API endpoints.

---

### Settings (`/settings`)

System configuration, user management, API keys, and integrations.

**Sub-routes**:

| Route | Tab | Purpose |
|-------|-----|---------|
| `/settings/health` | System Health | Service status, uptime, resource usage |
| `/settings/keys` | API Keys | Create, rotate, revoke API keys |
| `/settings/users` | Users & Access | User management, SSO, sessions |
| `/settings/notifications` | Notifications | Alert channels and rules |
| `/settings/environments` | Environments | Multi-environment configuration |
| `/settings/config` | Configuration | System config editor |
| `/settings/output-safety` | Output Safety | Output quarantine rules |

A **setup guide** drawer appears on first visit with a completion checklist ("3/6 completed").

#### System Health — `/settings/health`

Service dependency dashboard showing status of NATS, Redis, and other infrastructure components. Resource usage metrics and error rate indicators.

#### API Keys — `/settings/keys`

**Create key**: name, scope checkboxes (jobs:read, jobs:write, workflows:read, workflows:write, policy:read, policy:write, admin), optional expiration date.

**Key display**: generated secret shown once only with copy button and warning to save immediately.

**Key table**: name, masked prefix (****ABCD), scopes as colored badges, created date, last used, usage count, actions (rotate, revoke).

**Stale key warning**: banner for keys unused 30+ days.

**Expiry badges**: green (30d+), orange (7d), red pulsing (2h), red (expired).

**Rotate**: creates a new key with the same scopes and prompts to revoke the old one.

**Revoke**: confirmation dialog warning that integrations will stop working immediately.

#### Users & Access — `/settings/users`

**User list**: username, email, roles, created date, last login, actions (edit roles, disable/enable, remove).

**Change password** section (when password auth is enabled): current password, new password, confirm password.

**Single Sign-On** (enterprise badge): SAML 2.0 and OAuth configuration panels with test connection button.

**Session management**: active sessions list with revoke button, session timeout setting.

#### Notifications — `/settings/notifications`

Configure alert channels (email, Slack, webhook, PagerDuty) and notification rules with event pattern matching, channel selection, and throttle settings.

#### Environments — `/settings/environments`

Multi-environment setup for staging/production promotion with endpoint configuration and deployment tracking.

#### Configuration — `/settings/config`

Raw system configuration editor for safety stance, approval timeouts, retention periods, rate limits, concurrency limits, and maintenance mode.

Key settings:
- `safetyStance`: permissive / balanced / strict
- `approvalTimeoutMs`: milliseconds before approval times out
- `autoDenyOnTimeout`: deny jobs when approval times out
- `rateLimitPerKey`: API calls per key per window
- `concurrentJobsLimit`: max parallel jobs
- `maintenanceMode`: toggle maintenance mode with message

See [docs/configuration.md](configuration.md) for the full configuration reference.

#### Output Safety — `/settings/output-safety`

Configure output scanning rules that inspect LLM responses for policy violations. Manage quarantine rules, scanner configurations, and remediation actions.

See [docs/output-policy.md](output-policy.md) for the operator guide.

---

## Common Workflows

### How to Submit a Job

1. Navigate to **Jobs** (`/jobs`)
2. Click the **New Job** button (top right)
3. Fill in required fields:
   - **Topic** — select from suggestions or type a custom topic (e.g., `job.default`)
   - **Prompt** — describe what the agent should do
4. Optionally set:
   - **Priority** — low / normal / high / critical
   - **Capabilities** — comma-separated tags (e.g., `code_execution, web_browse`)
   - **Risk Tags** — comma-separated risk indicators (e.g., `data_access, external_api`)
   - **Metadata Labels** — key-value pairs for tracking
5. Expand **Advanced** for: adapter ID, memory ID, budget (max tokens), context hints, workflow ID, pack ID, idempotency key
6. Click **Submit Job**
7. On success: toast notification + redirect to the new job's detail page
8. On error: inline error message (policy denial reason, validation error, or capacity alert)

### How to Approve or Reject a Job

1. Navigate to **Approvals** (`/approvals`)
2. The **Queue** tab shows pending approvals sorted by wait time
3. Click an approval card to see its detail panel
4. Review: safety decision reason, matched policy rule, job capabilities, risk tags
5. To **approve**: click Approve (optionally add a comment)
6. To **reject**: click Reject and provide a required reason
7. For bulk actions: select multiple cards with checkboxes, then use Approve All or Reject All

### How to Create a Policy Rule

1. Navigate to **Policy Studio** > **Rules** (`/policies/rules`)
2. Click **New Rule**
3. Choose editing mode:
   - **Visual**: use the condition builder to set match criteria (topic, capabilities, risk tags), then select a decision (allow/deny/require_approval/throttle)
   - **YAML**: write the rule directly in YAML format
4. Test the rule using **Simulator** (`/policies/simulator`) before saving
5. Save and publish

### How to Review Quarantined Output

1. Navigate to **Dead Letters** (`/dlq`)
2. Filter by **Result type: Quarantined**
3. Expand a quarantined entry to see:
   - Output safety findings (violation type, severity, matched pattern)
   - Original output pointer
4. To release: click **Release Output** (admin only)
5. To retry with modifications: use the **Remediate** action from the job detail page

### How to Manage API Keys

1. Navigate to **Settings** > **API Keys** (`/settings/keys`)
2. To create: click **Create Key**, set name + scopes + optional expiration, copy the secret immediately
3. To rotate: click the rotate icon on an existing key — a new key is created with the same scopes
4. To revoke: click revoke and confirm in the dialog
5. Monitor the stale keys warning banner for keys unused 30+ days

### How to Configure MCP Server

1. Navigate to **Settings** > **Output Safety** (`/settings/output-safety`)
2. Enable the MCP server toggle
3. Configure transport (HTTP+SSE recommended for remote clients, stdio for local CLI)
4. Set the HTTP port and allowed CORS origins
5. Enable API key authentication and generate an MCP API key
6. Copy the Claude Desktop or Claude Code configuration snippet from the Quick Start section

See [docs/mcp-server.md](mcp-server.md) for detailed MCP server configuration.

---

## Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| `Cmd+K` / `Ctrl+K` | Open command palette |
| `Enter` (in search) | Navigate to search results |
| Click column header | Toggle sort direction |
| `Escape` | Close drawer / modal / command palette |

---

## UI Conventions

**Status badges**: green (success/allow), orange (warning/aging), red (danger/deny/critical), blue (info/dispatched), gray (default/cancelled).

**Data freshness**: most list pages show a "last updated" timestamp with a manual refresh button. Jobs refresh every 10 seconds, approval badge every 30 seconds.

**Filters**: all filter state is persisted in URL query parameters — bookmark filtered views or share URLs with teammates.

**Empty states**: pages show a helpful message with an action button when no data exists (e.g., "No jobs found — try adjusting your filters").

**Loading states**: skeleton rows/cards appear during data fetching. Detail pages show a centered spinner.

**Responsive design**: the dashboard adapts from mobile (single column) to desktop (multi-column grids, side panels).

---

## Related Documentation

- [API Reference](api-reference.md) — REST endpoint documentation
- [Configuration Reference](configuration.md) — environment variables and config files
- [Safety Kernel](safety-kernel.md) — policy engine internals
- [Output Policy](output-policy.md) — output scanning operator guide
- [MCP Server](mcp-server.md) — Model Context Protocol integration
- [Workflow Step Types](workflow-step-types.md) — workflow step reference
- [Pack Format](pack.md) — pack bundle specification
