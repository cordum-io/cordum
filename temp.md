# CORDUM DASHBOARD: Complete Refactor Specification

**From Job Runner to AI Control Plane**

Version 2.0 | January 2026

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Information Architecture](#2-information-architecture)
3. [Module 1: Mission Control](#3-module-1-mission-control-run-detail)
4. [Module 2: Policy Studio](#4-module-2-policy-studio)
5. [Module 3: Worker Pool Topology](#5-module-3-worker-pool-topology)
6. [Module 4: Capability Marketplace](#6-module-4-capability-marketplace-builder-sidebar)
7. [Module 5: Observability Center](#7-module-5-observability-center)
8. [Module 6: Context Inspector](#8-module-6-context-inspector)
9. [Module 7: Governance Dashboard](#9-module-7-governance-dashboard-home)
10. [Component Library](#10-component-library)
11. [State Management](#11-state-management)
12. [Implementation Roadmap](#12-implementation-roadmap)
13. [Technical Requirements](#13-technical-requirements)
14. [Appendix: File Changes Summary](#14-appendix-file-changes-summary)

---

## 1. Executive Summary

### 1.1 The Problem

The Cordum backend is an **enterprise-grade AI governance engine** featuring sophisticated capabilities:

- Safety Kernel with policy check/evaluate/explain/simulate
- MCP (Model Context Protocol) integration
- Context Pointers for efficient memory management
- Worker Pool orchestration with capability-aware routing
- Crash-safe workflow execution

**The current dashboard treats this as a generic CI/CD pipeline runner.** It hides the intelligence, governance, and agent-specific features that differentiate Cordum from competitors.

### 1.2 The Solution

Transform the dashboard from a "Job Runner" into an **"AI Control Plane"** with seven core modules:

| Module | Purpose |
|--------|---------|
| **Mission Control** | Real-time agent observation with split-pane narrative + context view |
| **Policy Studio** | Visual policy authoring, simulation, and explain interface |
| **Worker Topology** | Pool visualization, heartbeat monitoring, capability mapping |
| **Capability Marketplace** | Pack browser with semantic icons and drag-to-workflow |
| **Observability Center** | Traces, DLQ, audit logs, and distributed debugging |
| **Context Inspector** | Memory visualization, context windows, RAG debugging |
| **Governance Dashboard** | Safety metrics, approval queues, compliance reporting |

---

## 2. Information Architecture

### 2.1 Navigation Structure

```
├── Home (Governance Dashboard)
├── Runs
│   ├── Active Runs
│   ├── Run History
│   └── [Run Detail] → Mission Control
├── Workflows
│   ├── Library
│   └── Builder → Capability Marketplace
├── Governance
│   ├── Policy Studio
│   ├── Approval Queue
│   └── Safety Snapshots
├── Infrastructure
│   ├── Worker Pools
│   ├── Packs
│   └── Schemas
├── Observability
│   ├── Traces
│   ├── Dead Letter Queue
│   ├── Artifacts
│   └── Audit Log
└── Settings
    ├── API Keys
    ├── Webhooks
    └── Config
```

### 2.2 Data Flow Architecture

Each view connects to specific backend APIs and real-time streams:

| View | REST Endpoints | WebSocket Streams |
|------|----------------|-------------------|
| Mission Control | `GET /runs/{id}`, `GET /jobs/{id}` | `/api/v1/stream` (run events) |
| Policy Studio | `GET/POST /policies`, `POST /policies/simulate` | None |
| Worker Pools | `GET /workers`, `GET /pools` | `/api/v1/stream` (heartbeats) |
| DLQ | `GET /dlq`, `POST /dlq/{id}/replay` | `/api/v1/stream` (dlq events) |
| Traces | `GET /traces/{id}` | None |
| Approvals | `GET /approvals`, `POST /approvals/{id}` | `/api/v1/stream` (approvals) |

---

## 3. Module 1: Mission Control (Run Detail)

### 3.1 Current State Analysis

The existing `RunDetailPage.tsx` uses a tab-based interface (Overview, Timeline, Chat, DAG) that forces context-switching. The Chat is treated as secondary, and there is no visibility into policy decisions or agent reasoning.

### 3.2 Target State: Split-Pane Layout

Replace tabs with a persistent split-pane view showing the agent narrative alongside system state.

#### 3.2.1 Layout Specification

```
┌─────────────────────────────────────────────────────────────┐
│  Header: Status │ Duration │ Tokens │ Cost │ [Cancel]      │
├────────────────────────────────┬────────────────────────────┤
│                                │  CONTEXT PANEL             │
│  ACTIVITY STREAM               │  ┌────────────────────┐    │
│  (60% width)                   │  │ Active Variables   │    │
│                                │  │ user_id: "abc"     │    │
│  ┌──────────────────────────┐  │  │ intent: "refund"   │    │
│  │ 🤔 Thought Block         │  │  └────────────────────┘    │
│  │ "Checking order history" │  │                            │
│  └──────────────────────────┘  │  ┌────────────────────┐    │
│                                │  │ Safety Status      │    │
│  ┌──────────────────────────┐  │  │ ✓ refund-limit     │    │
│  │ 🔧 Tool Call Block       │  │  │ ⏳ approval-pending│    │
│  │ orders.lookup            │  │  └────────────────────┘    │
│  │ { user_id: "abc" }       │  │                            │
│  │ ✓ 200 OK                 │  │  ┌────────────────────┐    │
│  └──────────────────────────┘  │  │ Execution Mini-Map │    │
│                                │  │    ●──[2]──○       │    │
│  ┌──────────────────────────┐  │  └────────────────────┘    │
│  │ 🛡️ Safety Alert Block    │  │                            │
│  │ Policy: refund > $50     │  │  (40% width)               │
│  │ [Approve] [Deny]         │  │                            │
│  └──────────────────────────┘  │                            │
└────────────────────────────────┴────────────────────────────┘
```

#### 3.2.2 Activity Stream Types

The `ActivityItem` type must support structured rendering:

| Type | Visual | Content |
|------|--------|---------|
| `message` | Chat bubble | User input or agent final response |
| `thought` | Grey italic, collapsible | LLM chain-of-thought reasoning |
| `tool_call` | Card with spinner | Tool name, inputs JSON, status |
| `tool_result` | Nested in tool_call | Output JSON, latency, status code |
| `safety_event` | Red border alert | Policy name, decision, action buttons |
| `state_change` | Blue info line | Workflow step transition |
| `context_update` | Purple badge | Memory/context modification |

**TypeScript Definition:**

```typescript
// src/types/activity.ts

export type ActivityType = 
  | "message"
  | "thought"
  | "tool_call"
  | "tool_result"
  | "safety_event"
  | "state_change"
  | "context_update";

export type ActivityRole = "user" | "agent" | "system" | "governance";

export type SafetyDecision = "ALLOW" | "DENY" | "REQUIRE_APPROVAL" | "CONSTRAIN";

export interface ActivityItem {
  id: string;
  type: ActivityType;
  role: ActivityRole;
  timestamp: string;
  content: string;
  
  // Type-specific payloads
  payload?: {
    // For tool_call
    tool_name?: string;
    tool_inputs?: Record<string, unknown>;
    tool_status?: "pending" | "running" | "success" | "error";
    
    // For tool_result
    tool_output?: unknown;
    latency_ms?: number;
    status_code?: number;
    
    // For safety_event
    policy_name?: string;
    policy_id?: string;
    decision?: SafetyDecision;
    matched_rules?: string[];
    requires_action?: boolean;
    
    // For state_change
    from_step?: string;
    to_step?: string;
    
    // For context_update
    memory_operation?: "read" | "write" | "delete";
    memory_key?: string;
  };
  
  metadata?: {
    step_id?: string;
    job_id?: string;
    policy_snapshot?: string;
    cost?: number;
    tokens?: { input: number; output: number };
  };
}
```

#### 3.2.3 Context Panel Components

The right panel contains four collapsible sections:

**1. Active Context Card**
- Displays current workflow variables as a tree view with syntax highlighting
- Updates in real-time via WebSocket
- Shows diff highlighting when values change

**2. Safety Status Card**
- Lists all policies evaluated for this run
- Status indicators:
  - ✓ PASS (green check)
  - ✗ FAIL (red X)
  - ⏳ PENDING_APPROVAL (yellow clock)
  - — NOT_EVALUATED (grey dash)
- Clicking a policy opens explain modal

**3. Execution Mini-Map**
- Compact DAG visualization (200px height)
- Highlights current step with pulsing animation
- States: completed (filled), active (pulsing), pending (empty)

**4. Resource Metrics Card**
- Token count (input/output breakdown)
- Estimated cost
- Latency percentiles
- Retry count

#### 3.2.4 Component Implementation

```tsx
// src/pages/RunDetailPage.tsx

import { Panel, PanelGroup, PanelResizeHandle } from "react-resizable-panels";

export function RunDetailPage() {
  const { runId } = useParams();
  const { data: run } = useRunQuery(runId);
  const { activities } = useRunStream(runId);

  return (
    <div className="h-screen flex flex-col">
      {/* Header */}
      <RunHeader run={run} />
      
      {/* Split Pane Content */}
      <PanelGroup direction="horizontal" className="flex-1">
        {/* Left: Activity Stream */}
        <Panel defaultSize={60} minSize={40}>
          <ActivityStream 
            activities={activities}
            runId={runId}
          />
        </Panel>
        
        <PanelResizeHandle className="w-1 bg-border hover:bg-primary/50" />
        
        {/* Right: Context Panel */}
        <Panel defaultSize={40} minSize={30}>
          <ContextPanel run={run} />
        </Panel>
      </PanelGroup>
    </div>
  );
}
```

```tsx
// src/components/activity/ActivityBlock.tsx

export function ActivityBlock({ activity }: { activity: ActivityItem }) {
  switch (activity.type) {
    case "thought":
      return <ThoughtBlock activity={activity} />;
    case "tool_call":
      return <ToolCallBlock activity={activity} />;
    case "tool_result":
      return <ToolResultBlock activity={activity} />;
    case "safety_event":
      return <SafetyAlertBlock activity={activity} />;
    case "state_change":
      return <StateChangeBlock activity={activity} />;
    case "message":
    default:
      return <MessageBlock activity={activity} />;
  }
}
```

```tsx
// src/components/activity/SafetyAlertBlock.tsx

import { ShieldAlert, ShieldCheck, Clock } from "lucide-react";

export function SafetyAlertBlock({ activity }: { activity: ActivityItem }) {
  const { decision, policy_name, requires_action } = activity.payload ?? {};
  
  const bgColor = {
    ALLOW: "bg-green-900/20 border-green-500",
    DENY: "bg-red-900/20 border-red-500",
    REQUIRE_APPROVAL: "bg-amber-900/20 border-amber-500",
    CONSTRAIN: "bg-indigo-900/20 border-indigo-500",
  }[decision ?? "DENY"];

  return (
    <div className={`border-l-4 p-4 rounded-r-lg ${bgColor}`}>
      <div className="flex items-center gap-2 mb-2">
        <ShieldAlert className="h-5 w-5" />
        <span className="font-semibold">Policy: {policy_name}</span>
        <Badge variant={decision === "ALLOW" ? "success" : "warning"}>
          {decision}
        </Badge>
      </div>
      
      <p className="text-sm text-muted-foreground mb-3">
        {activity.content}
      </p>
      
      {requires_action && decision === "REQUIRE_APPROVAL" && (
        <div className="flex gap-2">
          <ApproveButton runId={activity.metadata?.job_id} />
          <DenyButton runId={activity.metadata?.job_id} />
        </div>
      )}
    </div>
  );
}
```

---

## 4. Module 2: Policy Studio

### 4.1 Purpose

The Safety Kernel (`kernel.go`) supports **Check**, **Evaluate**, **Explain**, and **Simulate** operations. The Policy Studio exposes all four capabilities through a dedicated interface.

### 4.2 Layout Specification

```
┌─────────────────────────────────────────────────────────────┐
│  POLICY STUDIO                                [+ New Policy]│
├──────────────────────────┬──────────────────────────────────┤
│  POLICY LIST             │  POLICY EDITOR / SIMULATOR       │
│  (30% width)             │  (70% width)                     │
│                          │                                  │
│  ☑ refund-limit (active) │  ┌─────────────────────────────┐│
│    v1.2 | 234 evals      │  │ Policy: refund-limit        ││
│                          │  │ Version: v1.2               ││
│  ☑ pii-detection         │  │                             ││
│    v2.0 | 1.2k evals     │  │ Rules:                      ││
│                          │  │ IF action.type == "refund"  ││
│  ☐ after-hours (disabled)│  │ AND action.amount > 50      ││
│    v1.0 | 0 evals        │  │ THEN REQUIRE_APPROVAL       ││
│                          │  │                             ││
│  [Bundles ▼]             │  │ Risk Tags: [money-movement] ││
│  ├ finance-policies      │  └─────────────────────────────┘│
│  ├ security-policies     │                                  │
│  └ compliance-policies   │  ┌─────────────────────────────┐│
│                          │  │ SIMULATOR                   ││
│                          │  │ Test Input:                 ││
│                          │  │ { "action": "refund",       ││
│                          │  │   "amount": 75 }            ││
│                          │  │                             ││
│                          │  │ [Simulate]                  ││
│                          │  │                             ││
│                          │  │ Result: ⚠️ REQUIRE_APPROVAL ││
│                          │  │ Matched Rule: line 3        ││
│                          │  │ Explanation: Amount 75 > 50 ││
│                          │  └─────────────────────────────┘│
└──────────────────────────┴──────────────────────────────────┘
```

### 4.3 Core Features

#### 4.3.1 Policy List
- Display all policies with toggle for active/inactive state
- Show version number and evaluation count
- Group by policy bundle
- Search and filter support

#### 4.3.2 Policy Editor
- Monaco editor with custom language support for policy DSL
- Syntax highlighting for policy keywords (IF, AND, OR, THEN)
- Inline validation showing errors
- Version history with diff view

#### 4.3.3 Policy Simulator
- JSON editor for test input with schema validation
- Simulate button calls `POST /policies/simulate`
- Results panel shows:
  - Decision (ALLOW/DENY/REQUIRE_APPROVAL/CONSTRAIN)
  - Matched rules with line numbers
  - Explain output with natural language reasoning
  - Risk tags that would be applied

#### 4.3.4 Explain Modal

When viewing a past policy decision (from run history or DLQ), the Explain feature provides:
- The exact policy version (snapshot hash) used
- Input data at time of evaluation
- Step-by-step rule matching
- Counterfactual: "If amount was 49, result would be ALLOW"

### 4.4 API Integration

| Action | Endpoint | Notes |
|--------|----------|-------|
| List policies | `GET /policies` | Supports `?bundle=` filter |
| Get policy | `GET /policies/{id}` | Includes version history |
| Create policy | `POST /policies` | Returns new version |
| Update policy | `PUT /policies/{id}` | Creates new version |
| Toggle active | `PATCH /policies/{id}` | `{ active: bool }` |
| Simulate | `POST /policies/simulate` | `{ input, policy_ids? }` |
| Explain | `POST /policies/explain` | `{ snapshot_id, input }` |

### 4.5 Component Implementation

```tsx
// src/pages/PolicyStudioPage.tsx

export function PolicyStudioPage() {
  const [selectedPolicy, setSelectedPolicy] = useState<string | null>(null);
  const { data: policies } = usePoliciesQuery();
  
  return (
    <div className="h-screen flex">
      {/* Policy List Sidebar */}
      <div className="w-80 border-r bg-surface-glass overflow-auto">
        <PolicyList 
          policies={policies}
          selected={selectedPolicy}
          onSelect={setSelectedPolicy}
        />
      </div>
      
      {/* Main Content */}
      <div className="flex-1 flex flex-col">
        {selectedPolicy ? (
          <>
            <PolicyEditor policyId={selectedPolicy} />
            <PolicySimulator policyId={selectedPolicy} />
          </>
        ) : (
          <EmptyState message="Select a policy to edit" />
        )}
      </div>
    </div>
  );
}
```

```tsx
// src/components/governance/PolicySimulator.tsx

export function PolicySimulator({ policyId }: { policyId: string }) {
  const [input, setInput] = useState("{}");
  const [result, setResult] = useState<SimulationResult | null>(null);
  const simulateMutation = usePolicySimulateMutation();

  const handleSimulate = async () => {
    const result = await simulateMutation.mutateAsync({
      policy_ids: [policyId],
      input: JSON.parse(input),
    });
    setResult(result);
  };

  return (
    <Card className="m-4">
      <CardHeader>
        <CardTitle>Policy Simulator</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-2 gap-4">
          {/* Input Editor */}
          <div>
            <Label>Test Input (JSON)</Label>
            <JSONEditor value={input} onChange={setInput} />
            <Button onClick={handleSimulate} className="mt-2">
              Simulate
            </Button>
          </div>
          
          {/* Result Display */}
          <div>
            <Label>Result</Label>
            {result && (
              <SimulationResult result={result} />
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
```

---

## 5. Module 3: Worker Pool Topology

### 5.1 Purpose

Cordum uses capability-aware pool routing with NATS queue groups. Operators need visibility into:
- Which pools exist and their capabilities
- Worker health via heartbeats
- Queue depth and load distribution
- Capability-to-pool mapping

### 5.2 Layout Specification

```
┌─────────────────────────────────────────────────────────────┐
│  WORKER POOLS                          [Refresh] [Settings] │
├─────────────────────────────────────────────────────────────┤
│  TOPOLOGY VIEW                                              │
│  ┌─────────────────┐  ┌─────────────────┐  ┌──────────────┐│
│  │ pool: default   │  │ pool: sensitive │  │ pool: batch  ││
│  │ ● 3/3 healthy   │  │ ● 2/2 healthy   │  │ ○ 0/2 online ││
│  │                 │  │                 │  │              ││
│  │ Queue: 12 jobs  │  │ Queue: 0 jobs   │  │ Queue: 847   ││
│  │ Avg latency: 2s │  │ Avg latency: 5s │  │ Avg: N/A     ││
│  │                 │  │                 │  │              ││
│  │ Capabilities:   │  │ Capabilities:   │  │ Capabilities:││
│  │ [chat] [code]   │  │ [refund] [pii]  │  │ [bulk-email] ││
│  │ [search]        │  │ [bank-transfer] │  │ [report-gen] ││
│  └────────┬────────┘  └────────┬────────┘  └──────┬───────┘│
│           │                    │                  │        │
├───────────┴────────────────────┴──────────────────┴────────┤
│  WORKER DETAIL (click pool to expand)                      │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ Worker: worker-abc-1 │ Status: ● Online                ││
│  │ Last heartbeat: 2s ago │ Jobs completed: 1,247         ││
│  │ Current job: job-xyz │ Uptime: 4h 23m                  ││
│  ├─────────────────────────────────────────────────────────┤│
│  │ Worker: worker-abc-2 │ Status: ● Online                ││
│  │ Last heartbeat: 1s ago │ Jobs completed: 1,102         ││
│  │ Current job: idle │ Uptime: 4h 23m                     ││
│  └─────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────┘
```

### 5.3 Core Features

#### 5.3.1 Pool Cards

Each pool displays:
- Health indicator (green/yellow/red based on healthy worker ratio)
- Active workers vs total count
- Queue depth with trend indicator
- Average job latency
- Capability tags as badges

#### 5.3.2 Worker List

Expanding a pool shows individual workers:
- Worker ID and hostname
- Last heartbeat timestamp with freshness indicator
- Current job (if any) with link to run
- Jobs completed count
- Uptime duration

#### 5.3.3 Capability Map

A separate tab showing:
- Capability to pool mapping
- Which pools can handle which job types
- Routing rules visualization

#### 5.3.4 Real-Time Updates

- Subscribe to heartbeat stream via WebSocket
- Update worker status when heartbeat received
- Mark worker as unhealthy if heartbeat exceeds timeout (configurable, default 30s)
- Show visual indicator for heartbeat events

### 5.4 API Integration

| Action | Endpoint | Notes |
|--------|----------|-------|
| List pools | `GET /pools` | Returns pool config + stats |
| Get pool | `GET /pools/{id}` | Includes worker list |
| List workers | `GET /workers` | Supports `?pool=` filter |
| Get worker | `GET /workers/{id}` | Full worker detail |
| Heartbeats | `WS /api/v1/stream` | Filter: `type=heartbeat` |

### 5.5 Component Implementation

```tsx
// src/components/infrastructure/PoolCard.tsx

interface PoolCardProps {
  pool: Pool;
  onExpand: () => void;
  expanded: boolean;
}

export function PoolCard({ pool, onExpand, expanded }: PoolCardProps) {
  const healthyWorkers = pool.workers.filter(w => w.status === "healthy").length;
  const healthRatio = healthyWorkers / pool.workers.length;
  
  const healthColor = 
    healthRatio >= 0.9 ? "text-green-500" :
    healthRatio >= 0.5 ? "text-amber-500" : "text-red-500";

  return (
    <Card 
      className={cn("cursor-pointer transition-all", expanded && "ring-2 ring-primary")}
      onClick={onExpand}
    >
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <CardTitle className="text-lg">{pool.name}</CardTitle>
          <span className={cn("font-mono", healthColor)}>
            ● {healthyWorkers}/{pool.workers.length}
          </span>
        </div>
      </CardHeader>
      
      <CardContent>
        <div className="grid grid-cols-2 gap-2 text-sm">
          <div>
            <span className="text-muted-foreground">Queue:</span>
            <span className="ml-2 font-medium">{pool.queueDepth} jobs</span>
          </div>
          <div>
            <span className="text-muted-foreground">Avg latency:</span>
            <span className="ml-2 font-medium">{pool.avgLatency}s</span>
          </div>
        </div>
        
        <div className="flex flex-wrap gap-1 mt-3">
          {pool.capabilities.map(cap => (
            <Badge key={cap} variant="secondary" className="text-xs">
              {cap}
            </Badge>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}
```

```tsx
// src/hooks/useHeartbeatStream.ts

export function useHeartbeatStream(poolId?: string) {
  const queryClient = useQueryClient();
  
  useRealtimeStream({
    filters: { type: "heartbeat", pool_id: poolId },
    onEvent: (event) => {
      // Update worker status in cache
      queryClient.setQueryData(
        ["workers", event.payload.worker_id],
        (old: Worker) => ({
          ...old,
          lastHeartbeat: event.timestamp,
          status: "healthy",
        })
      );
    },
  });
}
```

---

## 6. Module 4: Capability Marketplace (Builder Sidebar)

### 6.1 Current State Analysis

The existing `BuilderSidebar.tsx` renders pack topics as raw strings ("job.default", "github-pr-list"). This feels like a database dump rather than a curated capability catalog.

### 6.2 Target State: App Store Experience

Transform the sidebar into a visual capability browser with semantic icons, categories, and intelligent defaults.

### 6.3 Layout Specification

```
┌──────────────────────────────────────┐
│  CAPABILITIES              [Search]  │
├──────────────────────────────────────┤
│  ▼ Communication                     │
│    ┌────────────────────────────────┐│
│    │ [Slack icon] Slack             ││
│    │ Send messages, manage channels ││
│    │ ├ slack.send-message           ││
│    │ ├ slack.create-channel         ││
│    │ └ slack.list-users             ││
│    └────────────────────────────────┘│
│    ┌────────────────────────────────┐│
│    │ [Email icon] Email             ││
│    │ Send and read emails           ││
│    └────────────────────────────────┘│
│                                      │
│  ▼ Development                       │
│    ┌────────────────────────────────┐│
│    │ [GitHub icon] GitHub           ││
│    │ PRs, issues, repositories      ││
│    │ 🛡️ [code-review] [repo-admin]  ││
│    └────────────────────────────────┘│
│    ┌────────────────────────────────┐│
│    │ [DB icon] PostgreSQL           ││
│    │ Query and modify databases     ││
│    │ 🛡️ [data-access] [pii]         ││
│    └────────────────────────────────┘│
│                                      │
│  ▼ Logic                             │
│    ┌────────────────────────────────┐│
│    │ [Cog icon] Condition           ││
│    │ Branch based on data           ││
│    └────────────────────────────────┘│
│    ┌────────────────────────────────┐│
│    │ [Loop icon] Loop               ││
│    │ Iterate over collections       ││
│    └────────────────────────────────┘│
│                                      │
│  ▶ Finance (2)                       │
│  ▶ AI/ML (3)                         │
└──────────────────────────────────────┘
```

### 6.4 Core Features

#### 6.4.1 Category Grouping

Group capabilities by semantic category:
- **Communication:** Slack, Email, Teams, SMS
- **Development:** GitHub, GitLab, Jira, Linear
- **Data:** PostgreSQL, MongoDB, Redis, S3
- **Finance:** Stripe, Banking, Refunds
- **AI/ML:** OpenAI, Anthropic, Embedding
- **Logic:** Condition, Loop, Delay, Approval

#### 6.4.2 Icon Mapping

Map capability strings to icons from Lucide or react-icons:

```typescript
// src/lib/capabilityIcons.tsx

import { 
  Slack, Github, Database, Mail, 
  Sparkles, GitBranch, RefreshCw, 
  CreditCard, Cloud, Globe 
} from "lucide-react";
import { SiPostgresql, SiMongodb, SiRedis } from "react-icons/si";

export const capabilityIconMap: Record<string, React.ComponentType> = {
  "slack": Slack,
  "github": Github,
  "postgres": SiPostgresql,
  "postgresql": SiPostgresql,
  "mongodb": SiMongodb,
  "redis": SiRedis,
  "email": Mail,
  "openai": Sparkles,
  "anthropic": Sparkles,
  "condition": GitBranch,
  "loop": RefreshCw,
  "stripe": CreditCard,
  "s3": Cloud,
  "http": Globe,
};

export function CapabilityIcon({ capability, className }: { 
  capability: string; 
  className?: string 
}) {
  // Match by prefix
  const prefix = capability.split(".")[0].toLowerCase();
  const Icon = capabilityIconMap[prefix] ?? Package;
  return <Icon className={className} />;
}
```

#### 6.4.3 Risk Tag Display

If a capability has associated `risk_tags` (from pack definition), show shield icons:
- 🛡️ `[money-movement]`
- 🛡️ `[pii]`
- 🛡️ `[destructive]`

Hovering shows which policies may apply.

#### 6.4.4 Intelligent Drag Payload

When dragging a capability to the canvas, pre-fill:
- `topic` from pack definition
- `pack_id`
- `capability` string
- Default configuration schema
- Risk tags for visual indication on node

### 6.5 Data Requirements

| Source | Endpoint | Data Used |
|--------|----------|-----------|
| Pack registry | `GET /packs` | Pack metadata, capabilities |
| Pack topics | `GET /packs/{id}/topics` | Topics, schemas |
| Schemas | `GET /schemas/{id}` | Input/output definitions |
| Policies | `GET /policies` | Risk tags for display |

### 6.6 Component Implementation

```tsx
// src/components/workflow/CapabilityMarketplace.tsx

const categories = [
  { id: "communication", label: "Communication", icon: MessageSquare },
  { id: "development", label: "Development", icon: Code },
  { id: "data", label: "Data", icon: Database },
  { id: "finance", label: "Finance", icon: CreditCard },
  { id: "ai", label: "AI/ML", icon: Sparkles },
  { id: "logic", label: "Logic", icon: GitBranch },
];

export function CapabilityMarketplace() {
  const { data: packs } = usePacksQuery();
  const [search, setSearch] = useState("");
  const [expandedCategories, setExpandedCategories] = useState<Set<string>>(
    new Set(["communication", "development"])
  );

  const groupedCapabilities = useMemo(() => 
    groupByCategory(packs, categories),
    [packs]
  );

  return (
    <div className="h-full flex flex-col">
      {/* Search */}
      <div className="p-3 border-b">
        <Input
          placeholder="Search capabilities..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="h-8"
        />
      </div>

      {/* Category List */}
      <ScrollArea className="flex-1">
        {categories.map(category => (
          <CategorySection
            key={category.id}
            category={category}
            capabilities={groupedCapabilities[category.id] ?? []}
            expanded={expandedCategories.has(category.id)}
            onToggle={() => toggleCategory(category.id)}
            searchFilter={search}
          />
        ))}
      </ScrollArea>
    </div>
  );
}
```

```tsx
// src/components/workflow/DraggableCapability.tsx

export function DraggableCapability({ capability }: { capability: Capability }) {
  const dragPayload: NodeDragPayload = {
    type: "capability",
    topic: capability.topic,
    pack_id: capability.packId,
    capability: capability.name,
    schema: capability.inputSchema,
    risk_tags: capability.riskTags,
  };

  return (
    <div
      draggable
      onDragStart={(e) => {
        e.dataTransfer.setData("application/json", JSON.stringify(dragPayload));
      }}
      className="flex items-center gap-3 p-2 rounded hover:bg-accent cursor-grab"
    >
      <CapabilityIcon capability={capability.name} className="h-5 w-5" />
      <div className="flex-1 min-w-0">
        <div className="font-medium truncate">{capability.displayName}</div>
        <div className="text-xs text-muted-foreground truncate">
          {capability.description}
        </div>
      </div>
      {capability.riskTags.length > 0 && (
        <ShieldAlert className="h-4 w-4 text-amber-500" />
      )}
    </div>
  );
}
```

---

## 7. Module 5: Observability Center

### 7.1 Trace Browser

#### 7.1.1 Purpose

Distributed tracing across: API Gateway → Scheduler → Safety Kernel → NATS → Worker. Each job generates a trace showing timing and dependencies.

#### 7.1.2 Layout

```
┌─────────────────────────────────────────────────────────────┐
│  TRACE: trace-abc-123                          Duration: 4.2s│
├─────────────────────────────────────────────────────────────┤
│  Timeline (Gantt)                                           │
│  ├─ gateway.receive      ████                    120ms      │
│  ├─ scheduler.dispatch       ██                  45ms       │
│  ├─ safety.check               █                 12ms       │
│  ├─ nats.publish                █                8ms        │
│  ├─ worker.execute                ████████████   3800ms     │
│  │   ├─ llm.call                    ████████     3200ms     │
│  │   └─ tool.execute                      ██     400ms      │
│  └─ gateway.respond                           █  50ms       │
├─────────────────────────────────────────────────────────────┤
│  Span Detail (click span)                                   │
│  Name: worker.execute │ Service: worker-pool-default        │
│  Tags: job_id=xyz, capability=chat, tokens=1247             │
│  Logs: [12:34:56] Starting execution...                     │
└─────────────────────────────────────────────────────────────┘
```

#### 7.1.3 Implementation Notes

```tsx
// src/components/observability/TraceTimeline.tsx

interface Span {
  id: string;
  name: string;
  service: string;
  startTime: number;
  duration: number;
  parentId?: string;
  tags: Record<string, string>;
  logs: Array<{ timestamp: number; message: string }>;
}

export function TraceTimeline({ trace }: { trace: Trace }) {
  const [selectedSpan, setSelectedSpan] = useState<Span | null>(null);
  
  // Build tree structure from flat spans
  const spanTree = useMemo(() => buildSpanTree(trace.spans), [trace.spans]);
  
  // Calculate timeline bounds
  const { minTime, maxTime } = useMemo(() => ({
    minTime: Math.min(...trace.spans.map(s => s.startTime)),
    maxTime: Math.max(...trace.spans.map(s => s.startTime + s.duration)),
  }), [trace.spans]);

  return (
    <div className="flex flex-col h-full">
      {/* Gantt Chart */}
      <div className="flex-1 overflow-auto">
        {renderSpanTree(spanTree, { minTime, maxTime, onSelect: setSelectedSpan })}
      </div>
      
      {/* Span Detail */}
      {selectedSpan && (
        <SpanDetailPanel span={selectedSpan} />
      )}
    </div>
  );
}
```

### 7.2 Dead Letter Queue

#### 7.2.1 Purpose

Jobs that fail after all retries go to DLQ. Operators need to:
- View failed jobs with error details
- Inspect the exact input that caused failure
- Replay jobs after fixing issues
- Bulk operations for similar failures

#### 7.2.2 Layout

```
┌─────────────────────────────────────────────────────────────┐
│  DEAD LETTER QUEUE                    [Bulk Replay] [Purge] │
├─────────────────────────────────────────────────────────────┤
│  Filters: [All Types ▼] [Last 24h ▼] [Search...]            │
├─────────────────────────────────────────────────────────────┤
│  ☐ job-abc │ timeout │ refund.process │ 2h ago │ [Replay]   │
│  ☐ job-def │ error   │ email.send     │ 3h ago │ [Replay]   │
│  ☑ job-ghi │ policy  │ bank.transfer  │ 5h ago │ [Replay]   │
│  ☑ job-jkl │ policy  │ bank.transfer  │ 5h ago │ [Replay]   │
├─────────────────────────────────────────────────────────────┤
│  Detail: job-ghi                                            │
│  Error: Policy DENY - max-transfer-amount exceeded          │
│  Input: { "amount": 10000, "dest": "..." }                  │
│  Retry count: 3 │ Last attempt: 5h ago                      │
│  [View Run] [View Trace] [Edit & Replay] [Delete]           │
└─────────────────────────────────────────────────────────────┘
```

#### 7.2.3 Features

- **Error Type Badges:** timeout, error, policy, cancelled
- **Bulk Selection:** Select multiple items for bulk replay/delete
- **Edit & Replay:** Modify input JSON before replaying
- **Link to Related:** View original run, view trace

### 7.3 Audit Log

Immutable log of all governance-relevant events:
- Policy changes (who changed what, when)
- Approval decisions (who approved/denied, why)
- Configuration changes
- Authentication events

```
┌─────────────────────────────────────────────────────────────┐
│  AUDIT LOG                             [Export] [Filter ▼]  │
├─────────────────────────────────────────────────────────────┤
│  2024-01-15 14:32:05 │ policy.updated │ admin@acme.com      │
│  Changed: refund-limit v1.1 → v1.2                          │
│  Diff: threshold 50 → 75                                    │
├─────────────────────────────────────────────────────────────┤
│  2024-01-15 14:28:12 │ approval.granted │ manager@acme.com  │
│  Run: run-xyz │ Policy: refund-limit                        │
│  Reason: "Customer escalation approved by VP"               │
├─────────────────────────────────────────────────────────────┤
│  2024-01-15 14:15:00 │ config.updated │ system              │
│  Changed: worker-pool.sensitive.max_workers 2 → 4           │
└─────────────────────────────────────────────────────────────┘
```

### 7.4 Artifact Browser

Jobs can produce artifacts (files, reports, exports). The artifact browser shows:
- Artifact list with type icons
- Preview for supported types (images, JSON, text)
- Download links
- Retention policy status

### 7.5 API Integration

| Feature | Endpoint | Notes |
|---------|----------|-------|
| List traces | `GET /traces` | Paginated, filterable |
| Get trace | `GET /traces/{id}` | Full span tree |
| List DLQ | `GET /dlq` | Filterable by error type |
| Replay job | `POST /dlq/{id}/replay` | Optional: modified input |
| Purge DLQ | `DELETE /dlq` | With filter options |
| Audit log | `GET /audit` | Paginated, immutable |
| Artifacts | `GET /artifacts` | List and download |

---

## 8. Module 6: Context Inspector

### 8.1 Purpose

AI agents maintain context: conversation history, retrieved documents (RAG), long-term memory. The Context Engine stores this in Redis with pointer-based references. Developers need to debug:
- Why did the agent say that?
- What was in its context window?
- Did RAG retrieval fail?

### 8.2 Layout Specification

```
┌─────────────────────────────────────────────────────────────┐
│  CONTEXT INSPECTOR: run-xyz                                 │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────────────────────────────────┐│
│  │ CONTEXT WINDOW (at step 3)              Token count: 4.2k││
│  ├─────────────────────────────────────────────────────────┤│
│  │ ▼ System Prompt                                   1,200 ││
│  │   You are a helpful assistant for Acme Corp...         ││
│  │                                                         ││
│  │ ▼ Retrieved Documents (RAG)                       2,100 ││
│  │   ├─ doc_abc: "Refund Policy v2.pdf" [Score: 0.92]     ││
│  │   │  Relevant chunk: "Refunds over $50 require..."     ││
│  │   └─ doc_def: "FAQ.md" [Score: 0.78]                   ││
│  │      Relevant chunk: "Processing time is 3-5 days"     ││
│  │                                                         ││
│  │ ▼ Conversation History                              850 ││
│  │   User: I want to return my order                       ││
│  │   Assistant: I can help with that...                    ││
│  │   User: The order was $75                               ││
│  │                                                         ││
│  │ ▶ Long-term Memory (3 entries)                      150 ││
│  └─────────────────────────────────────────────────────────┘│
│                                                             │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ MEMORY TIMELINE                                         ││
│  │ Step 1 ────● Step 2 ────● Step 3 ────○ Step 4          ││
│  │            ↑ RAG query  ↑ Memory                        ││
│  │              added        updated                       ││
│  └─────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────┘
```

### 8.3 Core Features

#### 8.3.1 Context Window Breakdown

Shows the exact content sent to the LLM at each step:
- **System prompt** (collapsible)
- **Retrieved documents** with relevance scores
- **Conversation history**
- **Long-term memory** entries

Each section shows token count for budget debugging.

#### 8.3.2 Memory Timeline

Horizontal timeline showing context modifications:
- When RAG retrieval happened
- When memory was read/written
- Context size at each step

#### 8.3.3 RAG Debug

For RAG failures:
- Show the query that was used
- Retrieval scores for returned documents
- Documents that nearly matched but were filtered
- Embedding vector visualization (optional)

### 8.4 API Integration

| Feature | Endpoint | Notes |
|---------|----------|-------|
| Get context | `GET /context/{run_id}` | Full context state |
| Context at step | `GET /context/{run_id}/step/{n}` | Historical snapshot |
| Memory entries | `GET /memory/{memory_id}` | Long-term memory |
| RAG debug | `GET /context/{run_id}/rag` | Retrieval details |

### 8.5 Component Implementation

```tsx
// src/pages/ContextInspectorPage.tsx

export function ContextInspectorPage() {
  const { runId } = useParams();
  const [selectedStep, setSelectedStep] = useState<number>(0);
  
  const { data: context } = useContextQuery(runId, selectedStep);
  const { data: timeline } = useContextTimelineQuery(runId);

  return (
    <div className="h-screen flex flex-col p-4 gap-4">
      {/* Context Window */}
      <Card className="flex-1 overflow-hidden">
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle>Context Window (Step {selectedStep})</CardTitle>
          <TokenCounter tokens={context?.totalTokens} />
        </CardHeader>
        <CardContent className="overflow-auto">
          <ContextSection 
            title="System Prompt" 
            content={context?.systemPrompt}
            tokens={context?.systemPromptTokens}
          />
          <ContextSection 
            title="Retrieved Documents (RAG)" 
            tokens={context?.ragTokens}
          >
            <RAGDocumentList documents={context?.ragDocuments} />
          </ContextSection>
          <ContextSection 
            title="Conversation History" 
            content={context?.conversationHistory}
            tokens={context?.historyTokens}
          />
          <ContextSection 
            title="Long-term Memory" 
            tokens={context?.memoryTokens}
            defaultCollapsed
          >
            <MemoryEntryList entries={context?.memoryEntries} />
          </ContextSection>
        </CardContent>
      </Card>

      {/* Memory Timeline */}
      <Card className="h-32">
        <CardContent className="p-4">
          <MemoryTimeline 
            steps={timeline?.steps}
            selectedStep={selectedStep}
            onSelectStep={setSelectedStep}
          />
        </CardContent>
      </Card>
    </div>
  );
}
```

---

## 9. Module 7: Governance Dashboard (Home)

### 9.1 Purpose

The home page should immediately communicate: **"This is an AI Governance Platform."** Current generic metrics ("Failed Runs") should be replaced with governance-specific KPIs.

### 9.2 Layout Specification

```
┌─────────────────────────────────────────────────────────────┐
│  CORDUM CONTROL PLANE                     [Last 24h ▼]      │
├─────────────────────────────────────────────────────────────┤
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────┐│
│  │ 🛡️ SAFETY    │ │ ⏳ APPROVALS │ │ 🔥 TOKEN     │ │ ⚠️ DLQ││
│  │              │ │              │ │    BURN      │ │      ││
│  │ 1,247 scans  │ │ 3 pending    │ │ 45.2k/hr     │ │ 12   ││
│  │ 23 blocked   │ │ 12 approved  │ │ $127 today   │ │ items││
│  │ 98.2% ALLOW  │ │ 2 denied     │ │ ↑ 12%        │ │      ││
│  └──────────────┘ └──────────────┘ └──────────────┘ └──────┘│
├─────────────────────────────────────────────────────────────┤
│  ACTIVE RUNS                                                │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ ● run-abc │ refund-flow │ Step 3/5 │ 2m 34s │ [View]  │ │
│  │ ● run-def │ code-review │ Step 1/3 │ 45s    │ [View]  │ │
│  │ ⏳ run-ghi │ bank-xfer   │ APPROVAL │ 5m     │ [Review]│ │
│  └────────────────────────────────────────────────────────┘ │
├────────────────────────────────┬────────────────────────────┤
│  POLICY ACTIVITY (24h)         │  POOL HEALTH               │
│  [Chart: Stacked area]         │  default: ●●●   3/3        │
│  ████ ALLOW                    │  sensitive: ●●  2/2        │
│  ████ REQUIRE_APPROVAL         │  batch: ○○      0/2 ⚠️     │
│  ▓▓   DENY                     │                            │
└────────────────────────────────┴────────────────────────────┘
```

### 9.3 Key Widgets

#### 9.3.1 Safety Shield Widget

Shows:
- Total policy evaluations
- Blocked count (DENY)
- Approval rate
- Visual: Green shield (>95% allow), Yellow (80-95%), Red (<80%)

```tsx
// src/components/dashboard/SafetyShieldWidget.tsx

export function SafetyShieldWidget({ stats }: { stats: SafetyStats }) {
  const allowRate = (stats.allowed / stats.total) * 100;
  
  const shieldColor = 
    allowRate >= 95 ? "text-green-500" :
    allowRate >= 80 ? "text-amber-500" : "text-red-500";

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center gap-2">
          <Shield className={cn("h-5 w-5", shieldColor)} />
          <CardTitle className="text-sm font-medium">Safety</CardTitle>
        </div>
      </CardHeader>
      <CardContent>
        <div className="text-2xl font-bold">{stats.total.toLocaleString()}</div>
        <div className="text-xs text-muted-foreground">policy scans</div>
        <div className="mt-2 flex items-center gap-2 text-sm">
          <span className="text-red-500">{stats.denied} blocked</span>
          <span className="text-muted-foreground">•</span>
          <span className={shieldColor}>{allowRate.toFixed(1)}% ALLOW</span>
        </div>
      </CardContent>
    </Card>
  );
}
```

#### 9.3.2 Approval Queue Widget

Shows:
- Pending approvals (actionable)
- Approved/Denied today
- Oldest pending item age
- Click through to approval queue

#### 9.3.3 Token Burn Widget

Shows:
- Tokens consumed per hour
- Estimated cost today
- Trend vs yesterday
- By model breakdown (optional)

#### 9.3.4 DLQ Alert Widget

Shows:
- Items in DLQ
- Oldest item age
- Error type breakdown
- Pulses red if count exceeds threshold

#### 9.3.5 Active Runs List

Live updating list:
- Run ID and workflow name
- Current step progress
- Duration
- Status indicator (running/waiting approval)
- Quick actions

#### 9.3.6 Policy Activity Chart

Stacked area chart showing:
- ALLOW decisions over time
- REQUIRE_APPROVAL decisions
- DENY decisions
- CONSTRAIN decisions

Hover shows exact counts.

#### 9.3.7 Pool Health Summary

Compact view of worker pools:
- Pool name with status dots
- Worker count (healthy/total)
- Alert if any pool has issues

### 9.4 Component Implementation

```tsx
// src/pages/HomePage.tsx

export function HomePage() {
  const { data: stats } = useDashboardStatsQuery();
  const { data: activeRuns } = useActiveRunsQuery();
  const { data: policyActivity } = usePolicyActivityQuery({ hours: 24 });
  const { data: poolHealth } = usePoolHealthQuery();

  return (
    <div className="p-6 space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Cordum Control Plane</h1>
        <TimeRangeSelector />
      </div>

      {/* KPI Widgets */}
      <div className="grid grid-cols-4 gap-4">
        <SafetyShieldWidget stats={stats?.safety} />
        <ApprovalQueueWidget stats={stats?.approvals} />
        <TokenBurnWidget stats={stats?.tokens} />
        <DLQAlertWidget stats={stats?.dlq} />
      </div>

      {/* Active Runs */}
      <Card>
        <CardHeader>
          <CardTitle>Active Runs</CardTitle>
        </CardHeader>
        <CardContent>
          <ActiveRunsList runs={activeRuns} />
        </CardContent>
      </Card>

      {/* Bottom Row */}
      <div className="grid grid-cols-2 gap-4">
        <Card>
          <CardHeader>
            <CardTitle>Policy Activity (24h)</CardTitle>
          </CardHeader>
          <CardContent>
            <PolicyActivityChart data={policyActivity} />
          </CardContent>
        </Card>
        
        <Card>
          <CardHeader>
            <CardTitle>Pool Health</CardTitle>
          </CardHeader>
          <CardContent>
            <PoolHealthSummary pools={poolHealth} />
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
```

---

## 10. Component Library

### 10.1 Design System Tokens

Extend existing design tokens to support governance semantics:

| Token | Value | Usage |
|-------|-------|-------|
| `--color-allow` | `#10B981` | ALLOW decisions, healthy status |
| `--color-deny` | `#EF4444` | DENY decisions, errors, DLQ |
| `--color-approval` | `#F59E0B` | Pending approval, warnings |
| `--color-constrain` | `#6366F1` | CONSTRAIN decisions, modifications |
| `--color-thought` | `#9CA3AF` | Agent thoughts, secondary text |
| `--color-tool` | `#3B82F6` | Tool calls, actions |

### 10.2 New Shared Components

#### 10.2.1 ActivityBlock

Renders different activity types in the stream.

**Props:** `type`, `content`, `metadata`, `expandable`

#### 10.2.2 PolicyBadge

Displays a policy decision.

**Props:** `decision` (ALLOW/DENY/etc), `policyName`, `clickable` (opens explain)

```tsx
export function PolicyBadge({ decision, policyName, onClick }: PolicyBadgeProps) {
  const colors = {
    ALLOW: "bg-green-100 text-green-800 border-green-200",
    DENY: "bg-red-100 text-red-800 border-red-200",
    REQUIRE_APPROVAL: "bg-amber-100 text-amber-800 border-amber-200",
    CONSTRAIN: "bg-indigo-100 text-indigo-800 border-indigo-200",
  };

  return (
    <button
      onClick={onClick}
      className={cn(
        "inline-flex items-center gap-1 px-2 py-0.5 rounded border text-xs font-medium",
        colors[decision],
        onClick && "cursor-pointer hover:opacity-80"
      )}
    >
      <PolicyIcon decision={decision} className="h-3 w-3" />
      {policyName}
    </button>
  );
}
```

#### 10.2.3 HealthIndicator

Shows health status as colored dot.

**Props:** `status` (healthy/degraded/unhealthy), `label`, `tooltip`

#### 10.2.4 RiskTag

Displays a risk tag with shield icon.

**Props:** `tag` (money-movement/pii/etc), `variant` (compact/full)

#### 10.2.5 CapabilityIcon

Maps capability strings to icons.

**Props:** `capability`, `size`, `showLabel`

#### 10.2.6 JSONTree

Collapsible JSON viewer with syntax highlighting.

**Props:** `data`, `maxDepth`, `searchable`

#### 10.2.7 MiniDAG

Compact workflow visualization.

**Props:** `steps`, `currentStep`, `onStepClick`

#### 10.2.8 TokenCounter

Displays token count with cost estimate.

**Props:** `inputTokens`, `outputTokens`, `model`

```tsx
export function TokenCounter({ inputTokens, outputTokens, model }: TokenCounterProps) {
  const totalTokens = inputTokens + outputTokens;
  const cost = calculateCost(inputTokens, outputTokens, model);

  return (
    <div className="flex items-center gap-2 text-xs text-muted-foreground">
      <Coins className="h-3 w-3" />
      <span>{totalTokens.toLocaleString()} tokens</span>
      <span className="text-muted-foreground/50">•</span>
      <span>${cost.toFixed(4)}</span>
    </div>
  );
}
```

---

## 11. State Management

### 11.1 Current State

The dashboard uses TanStack Query for server state. This should continue with additions for real-time data.

### 11.2 Query Keys Structure

```typescript
// src/lib/queryKeys.ts

export const queryKeys = {
  runs: {
    all: ["runs"] as const,
    list: (filters: RunFilters) => ["runs", "list", filters] as const,
    detail: (id: string) => ["runs", id] as const,
    stream: (id: string) => ["runs", id, "stream"] as const,
  },
  
  policies: {
    all: ["policies"] as const,
    list: (filters?: PolicyFilters) => ["policies", "list", filters] as const,
    detail: (id: string) => ["policies", id] as const,
    simulate: ["policies", "simulate"] as const,
  },
  
  workers: {
    all: ["workers"] as const,
    byPool: (poolId: string) => ["workers", "pool", poolId] as const,
    detail: (id: string) => ["workers", id] as const,
  },
  
  pools: {
    all: ["pools"] as const,
    detail: (id: string) => ["pools", id] as const,
  },
  
  dlq: {
    all: ["dlq"] as const,
    list: (filters?: DLQFilters) => ["dlq", "list", filters] as const,
    detail: (id: string) => ["dlq", id] as const,
  },
  
  traces: {
    all: ["traces"] as const,
    detail: (id: string) => ["traces", id] as const,
  },
  
  context: {
    forRun: (runId: string) => ["context", runId] as const,
    atStep: (runId: string, step: number) => ["context", runId, step] as const,
  },
  
  dashboard: {
    stats: (timeRange: string) => ["dashboard", "stats", timeRange] as const,
    policyActivity: (hours: number) => ["dashboard", "policy-activity", hours] as const,
  },
};
```

### 11.3 WebSocket Integration

Create a `useRealtimeStream` hook that:
- Connects to `/api/v1/stream`
- Filters events by type/id
- Updates TanStack Query cache on events
- Handles reconnection with exponential backoff

```typescript
// src/hooks/useRealtimeStream.ts

interface StreamOptions {
  filters?: {
    type?: string;
    run_id?: string;
    pool_id?: string;
  };
  onEvent?: (event: StreamEvent) => void;
}

export function useRealtimeStream({ filters, onEvent }: StreamOptions) {
  const queryClient = useQueryClient();
  const [isConnected, setIsConnected] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectAttempts = useRef(0);

  useEffect(() => {
    const connect = () => {
      const params = new URLSearchParams(filters as Record<string, string>);
      const ws = new WebSocket(`${WS_BASE_URL}/api/v1/stream?${params}`);
      
      ws.onopen = () => {
        setIsConnected(true);
        reconnectAttempts.current = 0;
      };
      
      ws.onmessage = (event) => {
        const data: StreamEvent = JSON.parse(event.data);
        onEvent?.(data);
        
        // Auto-update relevant queries
        if (data.type === "run_event") {
          queryClient.invalidateQueries({
            queryKey: queryKeys.runs.detail(data.payload.run_id),
          });
        }
      };
      
      ws.onclose = () => {
        setIsConnected(false);
        // Exponential backoff reconnect
        const delay = Math.min(1000 * 2 ** reconnectAttempts.current, 30000);
        reconnectAttempts.current++;
        setTimeout(connect, delay);
      };
      
      wsRef.current = ws;
    };

    connect();
    return () => wsRef.current?.close();
  }, [filters, onEvent, queryClient]);

  return { isConnected };
}
```

### 11.4 Optimistic Updates

For approval actions:
1. Optimistically update UI to show "approved"
2. Mutation calls `POST /approvals/{id}`
3. On error, rollback to pending state
4. On success, invalidate related queries

```typescript
// src/hooks/useApprovalMutation.ts

export function useApprovalMutation() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async ({ id, decision, reason }: ApprovalInput) => {
      return api.post(`/approvals/${id}`, { decision, reason });
    },
    
    onMutate: async ({ id, decision }) => {
      // Cancel outgoing refetches
      await queryClient.cancelQueries({ queryKey: queryKeys.runs.detail(id) });
      
      // Snapshot previous value
      const previous = queryClient.getQueryData(queryKeys.runs.detail(id));
      
      // Optimistically update
      queryClient.setQueryData(queryKeys.runs.detail(id), (old: Run) => ({
        ...old,
        approvalStatus: decision === "approve" ? "approved" : "denied",
      }));
      
      return { previous };
    },
    
    onError: (err, variables, context) => {
      // Rollback on error
      if (context?.previous) {
        queryClient.setQueryData(
          queryKeys.runs.detail(variables.id),
          context.previous
        );
      }
    },
    
    onSettled: (data, error, variables) => {
      // Refetch to ensure consistency
      queryClient.invalidateQueries({
        queryKey: queryKeys.runs.detail(variables.id),
      });
    },
  });
}
```

---

## 12. Implementation Roadmap

### 12.1 Phase 1: Foundation (Weeks 1-2)

**Focus:** Mission Control layout and Activity Stream types

**Deliverables:**
1. Refactor `RunDetailPage.tsx` to split-pane layout
2. Create `ActivityBlock` component with all activity types
3. Implement Context Panel with `JSONTree` and `MiniDAG`
4. Update `types/activity.ts` with `ActivityItem` types
5. Wire WebSocket stream to activity feed

**Success Criteria:**
- Run detail page shows split-pane layout
- Activities render with type-specific blocks
- Real-time updates work via WebSocket

### 12.2 Phase 2: Governance Visibility (Weeks 3-4)

**Focus:** Policy Studio and Safety visualization

**Deliverables:**
1. Create Policy Studio page with list/detail views
2. Implement Policy Simulator with JSON input
3. Add `PolicyBadge` and `RiskTag` components
4. Implement `safety_event` rendering in `ActivityBlock`
5. Add inline approval actions to stream

**Success Criteria:**
- Policies can be viewed, edited, and tested
- Safety events appear in activity stream with actions
- Approvals can be granted/denied inline

### 12.3 Phase 3: Ops Visibility (Weeks 5-6)

**Focus:** Worker pools, DLQ, and Observability

**Deliverables:**
1. Create Worker Pools page with topology view
2. Implement real-time heartbeat display
3. Create DLQ page with list/detail/replay
4. Create Trace Browser with Gantt visualization
5. Add Artifact Browser

**Success Criteria:**
- Pool health visible at a glance
- Failed jobs can be inspected and replayed
- Traces show full distributed execution

### 12.4 Phase 4: Builder Enhancement (Weeks 7-8)

**Focus:** Capability Marketplace and Context Inspector

**Deliverables:**
1. Refactor `BuilderSidebar.tsx` with category grouping
2. Implement `CapabilityIcon` mapping
3. Add intelligent drag payloads
4. Create Context Inspector page
5. Implement Memory Timeline visualization

**Success Criteria:**
- Capabilities grouped by category with icons
- Drag-drop includes rich metadata
- Context window debuggable step-by-step

### 12.5 Phase 5: Home & Polish (Weeks 9-10)

**Focus:** Governance Dashboard and final polish

**Deliverables:**
1. Redesign `HomePage.tsx` with governance widgets
2. Implement Policy Activity chart
3. Add Pool Health summary
4. Implement Audit Log viewer
5. Performance optimization and accessibility review

**Success Criteria:**
- Home page communicates "AI Control Plane"
- All widgets show real data
- Lighthouse score > 90

---

## 13. Technical Requirements

### 13.1 Backend API Additions

The following API endpoints may need to be added or modified:

| Endpoint | Purpose | Status |
|----------|---------|--------|
| `POST /policies/simulate` | Policy simulation | Verify exists |
| `POST /policies/explain` | Policy explanation | Verify exists |
| `GET /context/{run_id}` | Context window data | May need addition |
| `GET /context/{run_id}/step/{n}` | Historical context | May need addition |
| `GET /pools` | Pool listing with stats | Verify response |
| `GET /traces/{id}` | Full trace with spans | Verify format |
| `GET /audit` | Audit log | May need addition |

### 13.2 WebSocket Event Types

The stream should emit the following event types:

```typescript
type StreamEventType = 
  | "run_event" 
  | "heartbeat" 
  | "approval" 
  | "dlq" 
  | "policy";

interface StreamEvent {
  type: StreamEventType;
  timestamp: string;
  payload: RunEvent | HeartbeatEvent | ApprovalEvent | DLQEvent | PolicyEvent;
}

interface RunEvent {
  run_id: string;
  event_type: "activity" | "state_change" | "complete" | "error";
  activity?: ActivityItem;
  state?: Record<string, unknown>;
}

interface HeartbeatEvent {
  worker_id: string;
  pool_id: string;
  status: "healthy" | "degraded";
  current_job?: string;
}

interface ApprovalEvent {
  approval_id: string;
  run_id: string;
  status: "pending" | "approved" | "denied";
  approver?: string;
}
```

### 13.3 Performance Considerations

#### Activity Stream Virtualization

For runs with 1000+ activities, use virtual scrolling:

```tsx
import { useVirtualizer } from "@tanstack/react-virtual";

function VirtualizedActivityStream({ activities }: { activities: ActivityItem[] }) {
  const parentRef = useRef<HTMLDivElement>(null);
  
  const virtualizer = useVirtualizer({
    count: activities.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 100,
  });

  return (
    <div ref={parentRef} className="h-full overflow-auto">
      <div style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((virtualRow) => (
          <div
            key={virtualRow.key}
            style={{
              position: "absolute",
              top: virtualRow.start,
              height: virtualRow.size,
            }}
          >
            <ActivityBlock activity={activities[virtualRow.index]} />
          </div>
        ))}
      </div>
    </div>
  );
}
```

#### WebSocket Batching

Batch rapid events (multiple thoughts in sequence) to reduce re-renders:

```typescript
const batchedEvents = useRef<StreamEvent[]>([]);
const flushTimeout = useRef<NodeJS.Timeout>();

ws.onmessage = (event) => {
  batchedEvents.current.push(JSON.parse(event.data));
  
  clearTimeout(flushTimeout.current);
  flushTimeout.current = setTimeout(() => {
    // Process all batched events at once
    const events = batchedEvents.current;
    batchedEvents.current = [];
    events.forEach(processEvent);
  }, 50);
};
```

#### Context Caching

Cache context snapshots at each step to avoid refetching for timeline scrubbing.

#### Lazy Loading

Load trace spans on demand rather than fetching entire trace upfront.

### 13.4 Dependencies to Add

```json
{
  "@tanstack/react-virtual": "^3.0.0",
  "react-resizable-panels": "^2.0.0",
  "@monaco-editor/react": "^4.6.0",
  "recharts": "^2.12.0",
  "lucide-react": "^0.300.0",
  "react-icons": "^5.0.0",
  "date-fns": "^3.0.0"
}
```

---

## 14. Appendix: File Changes Summary

### 14.1 Files to Modify

```
src/pages/RunDetailPage.tsx       → Complete rewrite (Mission Control)
src/pages/HomePage.tsx            → Complete rewrite (Governance Dashboard)
src/components/chat/ChatPanel.tsx → Replace with ActivityStream
src/components/chat/ChatMessage.tsx → Replace with ActivityBlock
src/components/workflow/BuilderSidebar.tsx → Major refactor
src/types/chat.ts                 → Expand with ActivityItem types
src/lib/queryKeys.ts              → Add new query keys
```

### 14.2 Files to Create

```
# Pages
src/pages/PolicyStudioPage.tsx
src/pages/WorkerPoolsPage.tsx
src/pages/DLQPage.tsx
src/pages/TraceBrowserPage.tsx
src/pages/ContextInspectorPage.tsx
src/pages/AuditLogPage.tsx

# Activity Components
src/components/activity/ActivityStream.tsx
src/components/activity/ActivityBlock.tsx
src/components/activity/ThoughtBlock.tsx
src/components/activity/ToolCallBlock.tsx
src/components/activity/SafetyAlertBlock.tsx
src/components/activity/StateChangeBlock.tsx
src/components/activity/ContextUpdateBlock.tsx

# Governance Components
src/components/governance/PolicyBadge.tsx
src/components/governance/RiskTag.tsx
src/components/governance/ApprovalActions.tsx
src/components/governance/PolicySimulator.tsx
src/components/governance/PolicyEditor.tsx
src/components/governance/PolicyList.tsx

# Infrastructure Components
src/components/infrastructure/PoolCard.tsx
src/components/infrastructure/WorkerList.tsx
src/components/infrastructure/HealthIndicator.tsx
src/components/infrastructure/CapabilityMap.tsx

# Dashboard Components
src/components/dashboard/SafetyShieldWidget.tsx
src/components/dashboard/ApprovalQueueWidget.tsx
src/components/dashboard/TokenBurnWidget.tsx
src/components/dashboard/DLQAlertWidget.tsx
src/components/dashboard/PolicyActivityChart.tsx
src/components/dashboard/PoolHealthSummary.tsx
src/components/dashboard/ActiveRunsList.tsx

# Observability Components
src/components/observability/TraceTimeline.tsx
src/components/observability/SpanDetailPanel.tsx
src/components/observability/DLQItem.tsx
src/components/observability/AuditLogEntry.tsx

# Context Components
src/components/context/ContextSection.tsx
src/components/context/RAGDocumentList.tsx
src/components/context/MemoryEntryList.tsx
src/components/context/MemoryTimeline.tsx

# Shared Components
src/components/shared/JSONTree.tsx
src/components/shared/MiniDAG.tsx
src/components/shared/CapabilityIcon.tsx
src/components/shared/TokenCounter.tsx
src/components/shared/TimeRangeSelector.tsx

# Hooks
src/hooks/useRealtimeStream.ts
src/hooks/useHeartbeatStream.ts
src/hooks/usePolicySimulateMutation.ts
src/hooks/useApprovalMutation.ts
src/hooks/useDLQReplayMutation.ts

# Types
src/types/activity.ts
src/types/policy.ts
src/types/worker.ts
src/types/trace.ts
src/types/context.ts

# Utils
src/lib/capabilityIcons.tsx
src/lib/costCalculator.ts
```

### 14.3 Test Coverage Requirements

Each new component should have:
- Unit tests for rendering states
- Integration tests for API interactions
- E2E tests for critical flows

**Critical E2E Scenarios:**

1. **Run observation:** Start run → Watch activities → See approval request → Approve → See completion

2. **Policy simulation:** Select policy → Enter test input → Simulate → See result

3. **DLQ recovery:** View DLQ item → Inspect error → Modify input → Replay → Verify success

---

## Summary

This specification transforms the Cordum dashboard from a generic job runner into a purpose-built AI Control Plane. The key transformations are:

| Before | After |
|--------|-------|
| Tab-based run detail | Split-pane Mission Control |
| Flat chat messages | Rich activity blocks |
| Generic node list | Capability marketplace |
| "Failed runs" metrics | Governance KPIs |
| Hidden policy logic | Visual Policy Studio |
| No worker visibility | Pool topology view |
| No debugging tools | Traces, DLQ, Context Inspector |

**Start with Phase 1** — the Mission Control split-pane layout is the single biggest change that shifts user perception from "watching a script" to "observing an intelligent agent."

---

*Document Version: 2.0*
*Last Updated: January 2026*