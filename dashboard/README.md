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
- **Tailwind CSS** - Styling
- **Lucide React** - Icons

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
