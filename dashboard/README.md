# Cordum Dashboard

A React-based dashboard for the Cordum workflow orchestration platform. Provides real-time monitoring, workflow management, and visual workflow building capabilities.

## Features

### Core Features
- **Workflow Management** - Create, view, and manage workflows
- **Run Monitoring** - Track workflow runs with real-time status updates
- **Job Management** - Monitor and manage individual jobs
- **Policy Engine** - Configure and test safety policies
- **Pack Management** - Install and manage capability packs
- **Worker Pools** - Monitor worker health and capacity

### Policy Compliance PDF Export
The **Policies → Analytics** page now includes **Export Compliance PDF** for auditor-ready policy reporting.

- Report includes organization metadata, generation timestamp, and policy bundle version (when available).
- Per-rule detail includes decision type, framework tags, 24h hit count, and last-triggered timestamp.
- Summary includes framework coverage totals and percentage coverage.
- Export state is surfaced in UI (loading/progress/error) and disables export when required data is unavailable.

**Current limitation:** framework alignment tags are only as complete as backend policy rule data.  
If the backend does not persist `framework_tags`, exported rules may appear as **Unmapped**.

### Per-Run Chat Interface
Real-time chat functionality for each workflow run:
- View agent conversations during workflow execution
- Send messages to interact with running workflows
- Live updates via WebSocket connection
- Role-based message styling (user, agent, system)

**Components:**
- `ChatPanel` - Full chat interface with message history and input
- `ChatMessage` - Individual message bubble with metadata
- `useRunChat` - Hook combining REST API and WebSocket updates

### Navigation & Governance
The dashboard is structured into four functional governance areas:
- **SECURITY** - Security Overview (Default Landing), Approvals, Policies, Audit Trail (with Live Event Timeline), Quarantine, and Safety Controls.
- **OPERATIONS** - Workflow Runs (with Pipeline Funnel), Jobs, Agent Fleet (with Pool Utilization), and Failures (with Failures Summary).
- **BUILD** - Workflow building (with Active Runs visibility), Pack management, and Schema definitions.
- **SETTINGS** - System and user configuration, including System Health metrics and Safety Circuits.

### Security-First Design
The dashboard defaults to the **Security Overview** at `/`, providing immediate visibility into system health, pending approvals, and quarantined outputs. Safety controls for input and output filtering are centralized under `/security/safety`.

**Security Overview Key Features:**
- **Posture Metrics** - Real-time Governance Score, pending approval counts with SLA tracking, and quarantined output volume.
- **Needs Attention** - A prioritized queue of security events requiring human intervention (quarantines and urgent approvals).
- **Live Safety Feed** - Real-time stream of policy evaluation decisions from across the control plane.
- **High-Severity Audit** - Direct visibility into critical system and configuration changes.
- **Unified Search** - Instant access to all resources via the **Command Palette** (Ctrl+K or Header Search), with fuzzy matching across jobs, workflows, policies, and packs.

**Execution Monitoring:**
- **Global Runs Listing** - Unified view of all active and historical workflow runs across the entire control plane.
- **Governance Rollup** - Instant visibility into the security outcome of each run (Clean, Approval Pending, Quarantined, Denied).
- **Job Pipeline Funnel** - Live visualization of job stages integrated into the Runs page.
- **Failures Management** - Focused interface for operational issues with failure summary metrics.
- **Unified Investigation** - Reusable **Investigation Links** across all entity details provide one-click navigation between jobs, policies, approvals, and audit trails.

**Agent Fleet & Capacity:**
- **Pool Utilization Heatmap** - Visual overview of worker pool load and healthy capacity integrated into the Agent Fleet page.
- **Deep Worker Detail** - Per-agent health, capabilities, and active job load.

**Audit & Visibility:**
- **Unified Audit Trail** - Complete history of system actions and safety decisions.
- **Live Event Timeline** - Real-time stream of bus events available as a dedicated view mode in the Audit Log.

**Safety Controls & Health:**
- **Centralized Posture Management** - Unified interface at `/security/safety` for managing global **Input Safety** (Fail-Closed/Fail-Open) and **Output Safety** rules.
- **System Health Dashboard** - Detailed health metrics for Workers, NATS, Redis, and Uptime, including Safety Circuit Breaker status and Rate Limiting modes.
- **Hash-Based Navigation** - Seamless switching between input and output controls via URL fragments.

### Design System (Control Surface)
- **Baseline** - The dashboard defaults to a **Dark** theme baseline for reduced eye strain and a modern "Control Room" aesthetic.
- **Instrument Cards** - All containers use the **Instrument Card** pattern, featuring a 2px top accent line that conveys semantic status (Nominal, Warning, Danger, Info).
- **Opacity-Based Badges** - Status badges follow a high-contrast opacity model: **15%** background, **20%** border, and **100%** text, with integrated 12px icons for improved scanability.
- **Precision Buttons** - All interactive actions use updated **6px radius** buttons with **150ms ease-out** transitions. Variants are strictly mapped to intent: **Primary** (cordum teal), **Secondary** (surface fill), **Destructive** (danger red), plus transparent **Outline** and **Ghost** styles.
- **Unified Focus** - Interactive elements share a standardized **double-ring** focus pattern (cordum/30 + cordum/15) for high visibility and consistent accessibility.
- **Motion Patterns** - Standardized Framer Motion entrance variants (`fadeIn`, `slideUp`, `scaleIn`) with a **60ms stagger** ensure a cohesive, high-performance interface.
- **Status Indicators** - Real-time state is communicated via pulsing **Live Dots** (StatusIndicator), providing glanceable healthy, warning, and critical feedback across the dashboard.
- **Immediate Application** - An inline bootstrap script ensures the theme is applied before the first paint, preventing flash-of-unstyled-content (FOUC).
- **Colors** - All colors use the **OKLCH** color space for consistent perceived lightness and better accessibility.
- **Theming** - Dual-theme support (Dark/Light) with a dark-first baseline.
- **Surfaces** - 5 semantic surface layers (`--bg`, `--surface`, `--surface-2`, `--surface-3`, `--surface-4`) for depth and hierarchy.
- **Radius** - Standardized border radius tokens: `sm: 4px`, `md: 6px` (Default), `lg: 8px`, `xl: 12px`. Never exceed 12px.
- **Motion** - All transitions use a standardized **ease-out** timing (150ms micro, 300ms page, 500ms entrance) for a responsive, non-bouncy feel.
- **Typography** - Plus Jakarta Sans (display), Inter (body), JetBrains Mono (data).

#### Primitive parity guardrails
- Canonical token contract is enforced in:
  - `src/styles/index.css`
  - `tailwind.config.cjs`
  - `src/styles/theme-tokens.test.ts`
- Parity-scoped primitives are guarded by `src/styles/design-parity.test.ts`:
  - `Button`, `Card`, `Badge`, `Input`, `Select`, `Textarea`, `Skeleton`, `Spinner`, `EmptyState`, `StatusIndicator`
  - `ComboboxInput`, `TagInput`, `ConfirmDialog`
  - `StatusBadge`
- Forbidden in parity-scoped primitives:
  - non-token white/translucent surface classes (`bg-white*`, `border-white*`, `text-white`)
  - raw `rgba(...)`/hex color literals for status/surface styling
  - oversized radius classes (`rounded-2xl`, `rounded-3xl`, custom radius > 12px)
  - ad-hoc non-token status palettes (`purple-*`, `emerald-*`, etc.)
- Required regression gates for primitive/token work:
  - `npm run typecheck`
  - `npm test`
  - `npm run build`

#### AppShell parity (task-2c60fa67)
- Shell benchmarks, measurable targets, and accepted deviations are tracked in `DESIGN_PARITY_CHECKLIST.md`.
- Implemented shell invariants:
  - fixed `240px` desktop sidebar and compact `36px` utility controls (search/theme/command/logout),
  - tokenized shell surface utilities (`shell-sidebar`, `shell-header`, `shell-panel`) to eliminate white/translucent drift,
  - explicit ARIA semantics for shell controls and section toggles (`aria-label`, `aria-expanded`, `aria-controls`).
- Validation record (2026-02-24):
  - ✅ `npm run typecheck`
  - ✅ `npm run test -- src/components/layout/AppShell.test.tsx src/components/ConnectionIndicator.test.ts`
  - ✅ `npm run test -- src/pages/settings/InputSafetySettings.test.tsx`
  - ✅ `npm test`
  - ✅ `npm run build`

#### Security Overview parity (task-eaf5f368)
- Security-specific parity criteria (KPI/feed/chart density, state snapshots, accessibility constraints) are tracked in `DESIGN_PARITY_CHECKLIST.md`.
- Implemented parity updates:
  - compacted Security Overview heading/KPI rhythm with instrument-card metric anatomy and semantic accents,
  - refined Needs Attention + Live Safety feed interaction hierarchy with stronger keyboard/focus semantics,
  - added tokenized Security Attention Breakdown chart framing (`chart-theme` + reduced-motion-safe chart animation),
  - hardened explicit loading/empty/error/success communication with retry affordances for query-backed panels.
- Validation record (2026-02-24):
  - ✅ `npm run typecheck`
  - ✅ `npm test`
  - ✅ `npm run build`

#### Operations pages parity (task-a013821c)
- Operations parity criteria (Approvals, Jobs, Runs density/state/accessibility contracts) are tracked in `DESIGN_PARITY_CHECKLIST.md`.
- Implemented parity updates:
  - **Approvals:** deterministic loading/empty/error/success queue messaging, stronger filter accessibility semantics, and dialog-level a11y hardening for review flows.
  - **Jobs:** compact table/control-surface rhythm with mono ID/time columns, compact semantic status chips, and consistent pagination affordances.
  - **Runs:** compact KPI/filter/list/detail hierarchy with semantic governance outcomes and preserved run stream + drill-down behavior.
  - **Cross-page status language:** unified compact status chip voice via `StatusBadge`, `Badge` density controls, and normalized status token formatting in `lib/status`/`lib/format`.
- Validation record (2026-02-24):
- ✅ `npm run typecheck`
- ✅ `npm test`
- ✅ `npm run build`

#### Parity validation workflow (task-eab4b87f)
- Reproducible parity evidence workflow and route-level outcomes are tracked in `DESIGN_PARITY_CHECKLIST.md` (Global parity evidence template + runbook).
- Reference comparators for every run:
  - manuscript routes: `/`, `/dashboard`, `/components`, `/colors`, `/typography`
  - local artifacts: `example/Layout.tsx`, `example/DashboardExample.tsx`, `example/index.css`
- Validation command sequence (run from `dashboard/`):
  - `npm run test -- src/styles/design-parity.test.ts src/styles/theme-tokens.test.ts`
  - `npm run test -- src/components/layout/AppShell.test.tsx src/pages/SecurityOverviewPage.test.tsx src/pages/ApprovalsPage.test.tsx src/pages/JobsPage.test.tsx src/pages/RunsPage.test.tsx src/components/ui/StatusIndicator.test.tsx`
  - `npm run typecheck`
  - `npm test`
  - `npm run build`
- Go/no-go criteria:
  - **GO** only when AppShell, Security, Approvals, Jobs, Runs, and shared primitive rows are PASS in the evidence matrix,
  - no unresolved high-severity parity deltas,
  - all validation commands pass.
- Final gate record on **February 24, 2026**:
  - ✅ `npm run typecheck`
  - ✅ `npm test`
  - ✅ `npm run build`

#### Parity maintenance guide (cross-platform)

When UI changes touch parity-critical surfaces (Shell, Security, Approvals, Jobs, Runs, shared primitives):
- Update token + primitive sources as needed:
  - `src/styles/index.css`
  - `tailwind.config.cjs`
  - `src/components/ui/*`, `src/components/StatusBadge.tsx`, `src/lib/status.ts`, `src/lib/format.ts`
- Update parity guard tests:
  - `src/styles/design-parity.test.ts`
  - `src/styles/theme-tokens.test.ts`
- Update accessibility/page behavior tests:
  - `src/components/layout/AppShell.test.tsx`
  - `src/pages/SecurityOverviewPage.test.tsx`
  - `src/pages/ApprovalsPage.test.tsx`
  - `src/pages/JobsPage.test.tsx`
  - `src/pages/RunsPage.test.tsx`
  - `src/components/ui/StatusIndicator.test.tsx`
- Update parity documentation:
  - `DESIGN_PARITY_CHECKLIST.md`
  - `DESIGN_LANGUAGE_MAPPING.md`
  - `README.md` + `../wiki/Dashboard.md`

Validation commands (same sequence on Windows/macOS/Linux):

```bash
cd dashboard
npm run test -- src/styles/design-parity.test.ts src/styles/theme-tokens.test.ts
npm run test -- src/components/layout/AppShell.test.tsx src/pages/SecurityOverviewPage.test.tsx src/pages/ApprovalsPage.test.tsx src/pages/JobsPage.test.tsx src/pages/RunsPage.test.tsx src/components/ui/StatusIndicator.test.tsx
npm run typecheck
npm test
npm run build
```

If a visual delta is intentional, add it to the deviation register in `DESIGN_PARITY_CHECKLIST.md` with rationale, risk level, owner, and rollback trigger before merging.

### Visual Workflow Builder
Drag-and-drop workflow builder similar to n8n:
- **7 Node Types:**
  - Worker (WO) - Execute jobs via topic
  - Approval (AP) - Human approval gate
  - Condition (IF) - If/else branching with true/false outputs
  - Delay (DL) - Wait or schedule execution
  - Loop (LP) - Iterate over items with body/done outputs
  - Parallel (PA) - Concurrent execution branches
  - Subworkflow (SW) - Nested workflow calls

- **Features:**
  - Drag nodes from sidebar to canvas
  - Drag pack topics to create pre-configured worker nodes
  - Node configuration panel with type-specific fields
  - MiniMap for navigation
  - Snap-to-grid alignment
  - Real-time workflow JSON generation

## Tech Stack

- **React 18** - UI framework
- **TypeScript** - Type safety
- **Vite** - Build tool
- **TanStack Query** - Data fetching and caching
- **Zustand** - State management
- **React Flow** - Workflow visualization
- **Framer Motion** - Animations (Control Surface DS)
- **Tailwind CSS** - Styling
- **Lucide React** - Icons
- **Fontsource** - Typography (Plus Jakarta Sans, Inter, JetBrains Mono)

## Project Structure

```
src/
├── components/
│   ├── chat/           # Chat components
│   │   ├── ChatMessage.tsx
│   │   └── ChatPanel.tsx
│   ├── workflow/       # Workflow builder
│   │   ├── nodes/      # Node components
│   │   │   ├── WorkerNode.tsx
│   │   │   ├── ApprovalNode.tsx
│   │   │   ├── ConditionNode.tsx
│   │   │   ├── DelayNode.tsx
│   │   │   ├── LoopNode.tsx
│   │   │   ├── ParallelNode.tsx
│   │   │   └── SubworkflowNode.tsx
│   │   ├── BuilderSidebar.tsx
│   │   ├── NodeConfigPanel.tsx
│   │   ├── StepOutputViewer.tsx
│   │   ├── WorkflowBuilder.tsx
│   │   ├── WorkflowCanvas.tsx
│   │   ├── nodeTypes.ts
│   │   └── types.ts
│   └── ui/             # Shared UI components
├── hooks/
│   ├── useLiveBus.ts   # WebSocket event handling
│   └── useRunChat.ts   # Chat hook
├── lib/
│   └── api.ts          # API client
├── pages/              # Route pages
├── state/
│   ├── chat.ts         # Chat store
│   ├── config.ts       # Config store
│   └── events.ts       # Events store
├── styles/
│   └── index.css       # Global styles
└── types/
    ├── api.ts          # API types
    └── chat.ts         # Chat types
```

## Getting Started

### Prerequisites
- Node.js 18+
- npm or yarn

### Installation

```bash
# Install dependencies
npm install

# Start development server
npm run dev

# Build for production
npm run build

# Type check
npm run typecheck

# Run tests
npm test
```

### Configuration

The dashboard connects to the Cordum API. Configure the base URL via:
- Environment variable or
- Settings in the dashboard UI

## API Endpoints

### Chat API
- `GET /api/v1/workflow-runs/:runId/chat` - Get chat history
- `POST /api/v1/workflow-runs/:runId/chat` - Send message

### WebSocket Events
The dashboard subscribes to `/api/v1/stream` for real-time updates:
- `jobRequest` - New job submitted
- `jobResult` - Job completed
- `jobProgress` - Job progress update
- `jobCancel` - Job cancelled
- `chatMessage` - Chat message received
- `heartbeat` - Worker heartbeat
- `alert` - System alert

## Testing

```bash
# Run all tests
npm test

# Run tests in watch mode
npm test -- --watch

# Run tests with coverage
npm test -- --coverage
```

## Development

### Adding a New Node Type

1. Create component in `src/components/workflow/nodes/`
2. Add type definition to `src/components/workflow/types.ts`
3. Register in `src/components/workflow/nodes/index.ts`
4. Add to `src/components/workflow/nodeTypes.ts`

### Adding New Chat Features

1. Extend `ChatMessage` type in `src/types/chat.ts`
2. Update `useChatStore` in `src/state/chat.ts`
3. Handle in `useRunChat` hook
4. Update `ChatMessage` component for rendering

## License

Proprietary - Cordum Inc.
