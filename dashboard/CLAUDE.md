# Dashboard - Claude CLI Configuration

React-based UI for Cordum control plane management.

## Tech Stack

| Technology | Purpose |
|------------|---------|
| React 18 | UI framework |
| TypeScript | Type safety |
| Vite | Build tool |
| TailwindCSS | Styling |
| React Query | Data fetching/caching |
| React Router | Navigation |
| Zustand | State management |
| Recharts | Visualizations |

## Directory Structure

```
dashboard/
├── src/
│   ├── components/       # Reusable UI components
│   │   ├── ui/           # Base components (Button, Input, etc.)
│   │   ├── jobs/         # Job-related components
│   │   ├── workflows/    # Workflow components
│   │   ├── policies/     # Policy management
│   │   └── layout/       # Layout components
│   ├── pages/            # Route pages
│   │   ├── Dashboard.tsx
│   │   ├── Jobs.tsx
│   │   ├── Workflows.tsx
│   │   ├── Policies.tsx
│   │   ├── Approvals.tsx
│   │   └── Settings.tsx
│   ├── hooks/            # Custom React hooks
│   │   ├── useJobs.ts
│   │   ├── useWorkflows.ts
│   │   ├── useWebSocket.ts
│   │   └── useApi.ts
│   ├── api/              # API client
│   │   ├── client.ts     # Base HTTP client
│   │   ├── jobs.ts       # Job endpoints
│   │   ├── workflows.ts  # Workflow endpoints
│   │   └── types.ts      # API types
│   ├── store/            # Zustand stores
│   │   ├── auth.ts
│   │   └── ui.ts
│   ├── lib/              # Utilities
│   │   ├── utils.ts
│   │   └── constants.ts
│   ├── App.tsx
│   └── main.tsx
├── public/
├── index.html
├── package.json
├── tsconfig.json
├── vite.config.ts
└── tailwind.config.js
```

## API Integration

### Base Client
```typescript
// src/api/client.ts
const API_BASE = import.meta.env.VITE_API_URL || '/api/v1';

export const apiClient = {
  async get<T>(path: string): Promise<T> {
    const res = await fetch(`${API_BASE}${path}`, {
      headers: {
        'Authorization': `Bearer ${getApiKey()}`,
      },
    });
    if (!res.ok) throw new ApiError(res);
    return res.json();
  },
  // post, put, delete...
};
```

### WebSocket for Real-time Updates
```typescript
// src/hooks/useWebSocket.ts
export function useWebSocket() {
  const ws = useRef<WebSocket | null>(null);
  
  useEffect(() => {
    ws.current = new WebSocket(`${WS_URL}/api/v1/stream`);
    
    ws.current.onmessage = (event) => {
      const data = JSON.parse(event.data);
      // Handle: job_update, workflow_update, approval_request
      handleEvent(data);
    };
    
    return () => ws.current?.close();
  }, []);
}
```

### React Query Hooks
```typescript
// src/hooks/useJobs.ts
export function useJobs(filters?: JobFilters) {
  return useQuery({
    queryKey: ['jobs', filters],
    queryFn: () => jobsApi.list(filters),
    refetchInterval: 5000, // Poll every 5s
  });
}

export function useJob(id: string) {
  return useQuery({
    queryKey: ['job', id],
    queryFn: () => jobsApi.get(id),
  });
}

export function useApproveJob() {
  const queryClient = useQueryClient();
  
  return useMutation({
    mutationFn: (id: string) => jobsApi.approve(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['jobs'] });
    },
  });
}
```

## Component Patterns

### Base UI Components
```typescript
// src/components/ui/Button.tsx
interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'danger';
  size?: 'sm' | 'md' | 'lg';
  loading?: boolean;
}

export function Button({ 
  variant = 'primary', 
  size = 'md',
  loading,
  children,
  ...props 
}: ButtonProps) {
  return (
    <button
      className={cn(
        'rounded-md font-medium transition-colors',
        variants[variant],
        sizes[size],
        loading && 'opacity-50 cursor-not-allowed'
      )}
      disabled={loading}
      {...props}
    >
      {loading ? <Spinner /> : children}
    </button>
  );
}
```

### Job Status Badge
```typescript
// src/components/jobs/StatusBadge.tsx
const statusColors: Record<JobStatus, string> = {
  pending: 'bg-yellow-100 text-yellow-800',
  dispatched: 'bg-blue-100 text-blue-800',
  running: 'bg-purple-100 text-purple-800',
  succeeded: 'bg-green-100 text-green-800',
  failed: 'bg-red-100 text-red-800',
  cancelled: 'bg-gray-100 text-gray-800',
};

export function StatusBadge({ status }: { status: JobStatus }) {
  return (
    <span className={cn('px-2 py-1 rounded-full text-xs', statusColors[status])}>
      {status}
    </span>
  );
}
```

### Approval Actions
```typescript
// src/components/approvals/ApprovalCard.tsx
export function ApprovalCard({ job }: { job: Job }) {
  const approve = useApproveJob();
  const reject = useRejectJob();
  
  return (
    <Card>
      <CardHeader>
        <h3>{job.type}</h3>
        <StatusBadge status={job.status} />
      </CardHeader>
      <CardBody>
        <p className="text-gray-600">{job.safetyDecision?.reason}</p>
        <pre className="mt-2 p-2 bg-gray-50 rounded text-sm">
          {JSON.stringify(job.metadata, null, 2)}
        </pre>
      </CardBody>
      <CardFooter className="flex gap-2">
        <Button 
          variant="primary" 
          onClick={() => approve.mutate(job.id)}
          loading={approve.isPending}
        >
          Approve
        </Button>
        <Button 
          variant="danger" 
          onClick={() => reject.mutate(job.id)}
          loading={reject.isPending}
        >
          Reject
        </Button>
      </CardFooter>
    </Card>
  );
}
```

## Key Pages

### Dashboard
- Overview metrics (jobs/hour, success rate, pending approvals)
- Recent activity feed
- System health indicators

### Jobs
- Job list with filters (status, type, time range)
- Job detail view with timeline
- Real-time status updates via WebSocket

### Workflows
- Workflow list and management
- Visual DAG editor (future)
- Run history with timeline

### Policies
- Policy list and editor
- Policy simulation tool
- Policy version history

### Approvals
- Pending approval queue
- Approval/reject with comments
- Approval history

## Styling Guidelines

### TailwindCSS Classes
```typescript
// Use consistent spacing
<div className="p-4 space-y-4">

// Consistent colors from design system
<div className="bg-primary-500 text-white">
<div className="text-gray-600">

// Responsive design
<div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
```

### Dark Mode Support
```typescript
// Use dark: variants
<div className="bg-white dark:bg-gray-900">
<p className="text-gray-900 dark:text-gray-100">
```

## TypeScript Types

```typescript
// src/api/types.ts
export interface Job {
  id: string;
  type: string;
  status: JobStatus;
  capabilities: string[];
  riskTags: string[];
  metadata: Record<string, unknown>;
  safetyDecision?: SafetyDecision;
  createdAt: string;
  updatedAt: string;
}

export type JobStatus = 
  | 'pending' 
  | 'dispatched' 
  | 'running' 
  | 'succeeded' 
  | 'failed'
  | 'cancelled';

export interface SafetyDecision {
  type: 'allow' | 'deny' | 'require_approval' | 'throttle';
  reason: string;
  matchedRule?: string;
}

export interface Workflow {
  id: string;
  name: string;
  steps: WorkflowStep[];
  timeout: number;
  metadata: Record<string, unknown>;
}
```

## Build & Development

```bash
# Install dependencies
npm install

# Development server
npm run dev

# Type checking
npm run typecheck

# Linting
npm run lint

# Build for production
npm run build

# Preview production build
npm run preview
```

## Environment Variables

```bash
# .env
VITE_API_URL=http://localhost:8080/api/v1
VITE_WS_URL=ws://localhost:8080
VITE_APP_TITLE=Cordum Dashboard
```

## Testing

```bash
# Run tests
npm test

# Watch mode
npm run test:watch

# Coverage
npm run test:coverage
```

### Component Testing
```typescript
// src/components/jobs/__tests__/StatusBadge.test.tsx
import { render, screen } from '@testing-library/react';
import { StatusBadge } from '../StatusBadge';

describe('StatusBadge', () => {
  it('renders correct color for succeeded status', () => {
    render(<StatusBadge status="succeeded" />);
    expect(screen.getByText('succeeded')).toHaveClass('bg-green-100');
  });
});
```
