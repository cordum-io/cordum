# CortexOS Dashboard & Capabilities

**Status:** Implemented & Live.

## 1. Overview

The CortexOS Dashboard provides a "Control Room" interface for the AI Control Plane. It visualizes the distributed system state, traces execution flows, and allows direct interaction via Chat.

## 2. Architecture

```mermaid
graph TD
    User[Browser] -->|HTTP/REST| API[API Gateway]
    User -->|WebSocket| API
    API -->|NATS Request| Scheduler
    API -->|Redis Get| Memory[Redis (JobStore + ctx/res)]
    API -->|Sub sys.audit./sys.job.*| NATS
```

## 3. Core Capabilities

### A. Mission Control (Overview)
- **Real-time Metrics:** Active Jobs, Completed Jobs, Event Count.
- **Live Feed:** Terminal-style scrolling log of system events (Job Submitted, Worker Joined, Safety Alert).
- **Tech:** Powered by `WS /api/v1/stream`.

### B. Worker Mesh (Compute Plane)
- **Visual Topology:** Grid of active worker nodes.
- **Metrics:** Real-time CPU/GPU load bars, active job counts.
- **Status:** Online/Offline indicators.
- **Tech:** Polls `GET /api/v1/workers`.

### C. Job Explorer (Traceability)
- **Search:** Filter jobs by ID.
- **Deep Inspection:** View full job state, timestamps, and result pointers.
- **Workflow Tracing:** Visualize parent-child relationships (Trace ID based).
- **Tech:** Polls `GET /api/v1/jobs` and `GET /api/v1/traces/{id}`.

### D. Cortex Chat (App)
- **Interactive UI:** Chat interface to submit jobs directly.
- **Feedback Loop:** Shows "Thinking..." state and updates with real-time results.
- **Tech:** `POST /api/v1/jobs` -> NATS -> Worker -> Redis -> NATS -> `WS /api/v1/stream`.

## 4. API Endpoints

The `cortex-api-gateway` exposes the following:

| Method | Endpoint | Description |
|---|---|---|
| GET | `/health` | System health check |
| GET | `/api/v1/workers` | List of active workers (from Scheduler registry) |
| GET | `/api/v1/jobs` | List recent jobs (Redis ZSET) |
| GET | `/api/v1/jobs/:id` | Get job details + result payload |
| POST | `/api/v1/jobs` | Submit a new job (JSON: `{prompt, topic}`) |
| GET | `/api/v1/traces/:id` | Get all jobs associated with a trace ID |
| WS  | `/api/v1/stream` | WebSocket for real-time `BusPacket` stream (camelCase JSON) |

**Auth:** If `API_KEY` is set on the gateway, every HTTP/WebSocket request must include header `X-API-Key: <value>`.
`VITE_API_KEY` in `.env.local` will add this header and append `api_key` to the WebSocket URL automatically.
Gateway also sets `TENANT_ID` (env) on all jobs; planner can be toggled via `USE_PLANNER` in compose/k8s.

## 5. Design System

- **Theme:** "Pro/DataDog" Dark Mode (`slate-950`).
- **Typography:** `Inter` / Monospace for data.
- **Visuals:** High-density cards, colored status badges, sparkline-style bars.
- **Stack:** React + Vite + Tailwind CSS (v4) + Lucide Icons.

## 6. Running the Dashboard

```bash
cd web/dashboard
npm install
npm run dev
```

Access at: `http://localhost:5173`
