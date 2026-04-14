# WebSocket Streaming Protocol

Reference for the Cordum real-time event stream over WebSocket. The gateway exposes two WebSocket endpoints for live updates: a global stream and a per-job stream.

> For the REST API reference, see [api-reference.md](api-reference.md).
> For the SDK client, see [sdk-reference.md](sdk-reference.md).

---

## Endpoints

| Endpoint | Auth | Description |
|----------|------|-------------|
| `GET /api/v1/stream` | API key (subprotocol) + admin role | Global event stream — all jobs, heartbeats, audit events |
| `GET /api/v1/jobs/{id}/stream` | API key (subprotocol) + tenant match | Per-job event stream — events for a specific job only |

---

## 1. Connection

### URL

Derive the WebSocket URL from the gateway HTTP base URL:

| HTTP Base | WebSocket URL |
|-----------|--------------|
| `http://localhost:8081` | `ws://localhost:8081/api/v1/stream` |
| `https://cordum.example.com` | `wss://cordum.example.com/api/v1/stream` |

### Authentication

Authentication is performed via the WebSocket [subprotocol](https://datatracker.ietf.org/doc/html/rfc6455#section-1.9) header. This avoids sending credentials as query parameters (which appear in server logs).

**Format**: `cordum-api-key.<base64url-encoded-api-key>`

The API key is base64url-encoded (RFC 4648 without padding):

```
Original key: my-secret-api-key-1234
Base64url:    bXktc2VjcmV0LWFwaS1rZXktMTIzNA
Subprotocol:  cordum-api-key.bXktc2VjcmV0LWFwaS1rZXktMTIzNA
```

**Important**: Strip `=` padding and use base64url alphabet (`-` and `_` instead of `+` and `/`), because `=` is not valid in subprotocol names per the WebSocket RFC.

The gateway echoes back the matched subprotocol in the `Sec-WebSocket-Protocol` response header.

### Authorization

- **Global stream** (`/api/v1/stream`): Requires `admin` role.
- **Per-job stream** (`/api/v1/jobs/{id}/stream`): Requires tenant access to the job's tenant.

### Tenant Isolation

Each WebSocket client is associated with a tenant from the authenticated request context. Events are filtered server-side:

- Events with a matching `tenant` field are delivered
- Events without a tenant field are dropped for non-cross-tenant clients
- Cross-tenant clients (admin) receive all events

---

## 2. Message Format

All messages are JSON-encoded [BusPacket](AGENT_PROTOCOL.md) protobuf messages serialized with `protojson`. Each message represents a single bus event.

### Wire Format (protojson)

```json
{
  "traceId": "abc-123",
  "senderId": "cordum-scheduler",
  "createdAt": {
    "seconds": "1707840000",
    "nanos": 0
  },
  "jobResult": {
    "jobId": "job-xyz",
    "status": "JOB_STATUS_SUCCEEDED",
    "workerId": "worker-1",
    "executionMs": "1250",
    "resultPtr": "res:job:job-xyz"
  }
}
```

### Payload Variants

Each BusPacket contains exactly one payload field:

| Field | Proto Type | Description |
|-------|-----------|-------------|
| `jobRequest` | `JobRequest` | Job submitted to the bus |
| `jobResult` | `JobResult` | Job completed (succeeded, failed, cancelled) |
| `jobProgress` | `JobProgress` | Job progress update (percent, message) |
| `jobCancel` | `JobCancel` | Job cancellation signal |
| `heartbeat` | `Heartbeat` | Worker heartbeat with pool, active jobs, capacity |
| `alert` | `Alert` | System alert |

### Common Fields

| Field | Type | Description |
|-------|------|-------------|
| `traceId` | string | Trace correlation ID |
| `senderId` | string | ID of the sender (scheduler, worker, etc.) |
| `createdAt` | Timestamp | Event creation time (`{seconds, nanos}`) |
| `protocolVersion` | string | CAP protocol version |
| `signature` | bytes | Optional ECDSA packet signature |

---

## 3. Event Types

The dashboard normalizes BusPackets into `StreamEvent` objects. Here are the event types and their payloads:

### Job Events

| Event Type | Source Field | Payload Fields |
|-----------|-------------|----------------|
| `job.submit` | `jobRequest` | `jobId`, `topic`, `tenantId`, `labels` |
| `job.result` | `jobResult` | `jobId`, `status`, `errorCode`, `errorMessage`, `executionMs`, `workerId` |
| `job.result.succeeded` | `jobResult` | Same as `job.result` (status-specific) |
| `job.result.failed` | `jobResult` | Same as `job.result` (status-specific) |
| `job.result.cancelled` | `jobResult` | Same as `job.result` (status-specific) |
| `job.progress` | `jobProgress` | `jobId`, `percent`, `message`, `status` |
| `job.cancel` | `jobCancel` | `jobId`, `reason` |

### Worker Events

| Event Type | Source Field | Payload Fields |
|-----------|-------------|----------------|
| `worker.heartbeat` | `heartbeat` | `workerId`, `pool`, `activeJobs`, `maxParallelJobs` |

### System Events

| Event Type | Source Field | Payload Fields |
|-----------|-------------|----------------|
| `system.alert` | `alert` | Varies by alert type |

### Audit Events

The gateway subscribes to `sys.audit.>` NATS subjects. Audit events arrive as BusPackets and are forwarded to WebSocket clients as-is.

---

## 4. Bus Subscriptions

The gateway subscribes to these NATS subjects and forwards matching packets to WebSocket clients:

| NATS Subject | Events |
|-------------|--------|
| `sys.heartbeat` | Worker heartbeats |
| `sys.job.>` | All job lifecycle events (submit, result, progress, cancel) |
| `sys.audit.>` | Audit trail events |
| `sys.job.dlq` | Dead-letter queue entries (also persisted to DLQ store) |

---

## 5. Per-Job Streaming

Connect to `/api/v1/jobs/{id}/stream` to receive only events for a specific job:

```
ws://localhost:8081/api/v1/jobs/job-abc123/stream
```

Server-side filtering:
- Only events matching the specified `jobId` are delivered
- Tenant access is verified against the job's tenant before the upgrade
- Returns `404` if the job does not exist
- Returns `403` if the caller's tenant does not match

---

## 6. Reconnection Strategy

The gateway now sends WebSocket ping frames every 30 seconds by default and expects the client to process control frames and reply with pong frames. Clients should still implement reconnection with exponential backoff for process restarts, network partitions, credential revocation, and any transport that remains unavailable after keepalive retries.

### Server Keepalive and Revalidation

- The gateway sends a ping every `30s` by default (`GATEWAY_WS_PING_INTERVAL`)
- The server extends the read deadline when it receives a pong and treats missing pongs as a dead connection (`GATEWAY_WS_PONG_TIMEOUT`)
- Long-lived WebSocket credentials are revalidated every `120s`
- Transient auth backend failures (for example network timeouts) are retried before the connection is dropped
- The HTTP server idle timeout defaults to `120s` (`GATEWAY_HTTP_IDLE_TIMEOUT`) so quiet upgraded connections are not closed before the keepalive loop runs

**Client requirement:** keep a read loop running. In Gorilla WebSocket and most browser runtimes, ping/pong handlers only run while the connection is being read.

### Recommended Parameters

| Parameter | Value |
|-----------|-------|
| Initial backoff | 1 second |
| Maximum backoff | 30 seconds |
| Backoff factor | 2x |
| Reset on success | Yes (reset to initial on `onopen`) |

### Connection Lifecycle

```
connect() → onopen    → receiving messages...
                         ↓ (connection drops)
           onclose   → wait(backoff) → connect()
                         backoff *= 2 (capped at max)
```

### Connection Identification

Every WebSocket connection is assigned a unique `conn_id` — a 16-character hex string generated from `crypto/rand`. This ID appears in all lifecycle log entries and allows operators to trace a single connection across connect, revalidation, and disconnect events.

### Lifecycle Logging

The gateway emits structured `slog.Info` logs at connection boundaries:

**Connect:**
```
level=INFO msg="ws connected" conn_id=a1b2c3d4e5f67890 remote=10.0.1.5:52340 tenant=default user_agent=Go-http-client/1.1
```

**Disconnect:**
```
level=INFO msg="ws disconnected" conn_id=a1b2c3d4e5f67890 remote=10.0.1.5:52340 tenant=default duration=482s reason=client_close
```

Disconnect reasons:

| Reason | Meaning |
|--------|---------|
| `client_close` | Client closed the connection normally |
| `ping_timeout` | Client failed to respond to ping within the pong timeout |
| `revalidation_revoked` | Credential revalidation determined the API key is no longer valid |
| `slow_client` | Client send buffer was full (100 events queued) |
| `shutdown` | Gateway is shutting down |

### Prometheus Metrics

The gateway exports 9 WebSocket metrics on the `/metrics` endpoint (default port 9092):

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `cordum_gateway_ws_clients_active` | Gauge | — | Current active WebSocket connections |
| `cordum_gateway_ws_connection_duration_seconds` | Histogram | — | Connection lifetime (buckets: 1s to 4h) |
| `cordum_gateway_ws_pings_sent_total` | Counter | — | Ping frames sent to clients |
| `cordum_gateway_ws_pongs_received_total` | Counter | — | Pong frames received from clients |
| `cordum_gateway_ws_pong_timeouts_total` | Counter | — | Connections closed after missing pong |
| `cordum_gateway_ws_packets_dropped_total` | Counter | — | Bus packets dropped due to marshal failure |
| `cordum_gateway_ws_slow_client_evictions_total` | CounterVec | `reason` | Clients evicted (buffer full) |
| `cordum_gateway_ws_revalidation_total` | CounterVec | `outcome` | Credential revalidation outcomes (`ok`, `revoked`, `error`) |
| `cordum_gateway_ws_reconnections_total` | Counter | — | Client reconnections within the reconnect window |

### Slow Client Eviction

The server buffers up to 100 events per client (`make(chan wsEvent, 100)`). If a client falls behind and the buffer is full, the server closes the connection. The client should reconnect.

### Missed Events

There is no replay or catch-up mechanism. When reconnecting, poll the REST API to get the current state of any resources you were tracking.

---

## 7. Client Examples

### Browser (JavaScript)

```javascript
const apiKey = "your-api-key";
// Base64url encode without padding
const encoded = btoa(apiKey)
  .replace(/\+/g, "-")
  .replace(/\//g, "_")
  .replace(/=+$/, "");
const subprotocol = `cordum-api-key.${encoded}`;

const ws = new WebSocket("ws://localhost:8081/api/v1/stream", [subprotocol]);

ws.onopen = () => console.log("Connected");

ws.onmessage = (event) => {
  const packet = JSON.parse(event.data);

  if (packet.jobResult) {
    const status = packet.jobResult.status.replace(/^.*_/, "").toLowerCase();
    console.log(`Job ${packet.jobResult.jobId}: ${status}`);
  }
  if (packet.heartbeat) {
    console.log(`Worker ${packet.heartbeat.workerId}: ${packet.heartbeat.activeJobs} active`);
  }
};

ws.onclose = () => {
  console.log("Disconnected — reconnecting...");
  setTimeout(() => { /* reconnect logic */ }, 1000);
};
```

### Node.js

```javascript
import WebSocket from "ws";

const apiKey = process.env.CORDUM_API_KEY;
const encoded = Buffer.from(apiKey).toString("base64url");
const subprotocol = `cordum-api-key.${encoded}`;

const ws = new WebSocket("ws://localhost:8081/api/v1/stream", [subprotocol]);

ws.on("open", () => console.log("Connected"));

ws.on("message", (data) => {
  const packet = JSON.parse(data.toString());
  if (packet.jobResult) {
    console.log(`Job ${packet.jobResult.jobId}: ${packet.jobResult.status}`);
  }
});

ws.on("close", () => console.log("Disconnected"));
```

### wscat (Testing)

```bash
# Install wscat
npm install -g wscat

# Connect (API key as subprotocol)
KEY=$(echo -n "$CORDUM_API_KEY" | base64 | tr '+/' '-_' | tr -d '=')
wscat -c "ws://localhost:8081/api/v1/stream" \
  -s "cordum-api-key.$KEY"
```

### Go

```go
import "github.com/gorilla/websocket"

apiKey := os.Getenv("CORDUM_API_KEY")
encoded := base64.RawURLEncoding.EncodeToString([]byte(apiKey))
subprotocol := "cordum-api-key." + encoded

dialer := websocket.Dialer{
    Subprotocols: []string{subprotocol},
}
conn, _, err := dialer.Dial("ws://localhost:8081/api/v1/stream", nil)
if err != nil {
    log.Fatal(err)
}
defer conn.Close()

for {
    _, message, err := conn.ReadMessage()
    if err != nil {
        log.Println("read error:", err)
        break
    }
    fmt.Println(string(message))
}
```

---

## 8. Dashboard Integration

The Cordum dashboard uses two hooks for WebSocket integration:

### useEventStream

- Manages the single WebSocket connection to `/api/v1/stream`
- Authenticates via the `cordum-api-key.<base64url>` subprotocol
- Auto-reconnects with exponential backoff (1s to 30s)
- Converts raw `BusPacket` protojson to normalized `StreamEvent` objects
- Dispatches events to:
  - **React Query cache invalidation** — events matching `job.*`, `workflow.*`, `approval.*`, `worker.*`, `dlq.*`, `policy.*`, `run.*`, `pack.*`, `safety.*`, `audit.*` invalidate their respective query keys
  - **Zustand event store** — all events buffered for the live activity feed
  - **Safety decision store** — `safety.*` events pushed to a dedicated buffer

### useRunStream

- Subscribes to the Zustand event store (not a separate WebSocket)
- Filters events by run ID for a specific workflow run
- Optimistically patches React Query cached run data for instant UI updates
- Handles: step status changes, job result mapping to steps, run-level status changes

### Cache Invalidation Map

| Event Prefix | Query Keys Invalidated |
|-------------|----------------------|
| `job.*` | `["jobs"]` |
| `workflow.*` | `["workflows"]` |
| `approval.*` | `["approvals"]`, `["approvals", "nav"]` |
| `worker.*` | `["workers"]` |
| `dlq.*` | `["dlq"]`, `["dlq", "nav"]` |
| `policy.*` | `["policy-bundles"]`, `["policy-rules"]` |
| `run.*` | `["workflow-runs"]`, `["runs"]` |
| `pack.*` | `["packs"]` |
| `safety.*` | `["safety"]` |
| `audit.*` | `["audit"]` |

---

## 9. Server-Side Details

### Write Timeout

The server sets a 5-second write deadline per message. If the client does not consume a message within this window, the write fails and the connection is closed.

### Origin Check

The WebSocket upgrader calls `isAllowedOrigin(r)` which checks against the configured CORS allowed origins (`CORDUM_ALLOWED_ORIGINS`, `CORDUM_CORS_ALLOW_ORIGINS`, or `CORS_ALLOW_ORIGINS`).

### Event Buffer

- Internal broadcast channel: unbuffered (events are dropped if no goroutine is ready)
- Per-client channel: 100 events buffered
- Slow clients are detected during broadcast and disconnected

### Shutdown

When the gateway shuts down, it closes the broadcast channel (`stopBusTaps`), which terminates the broadcast goroutine and causes all client connections to close gracefully.

---

## Related Docs

- [api-reference.md](api-reference.md) — REST endpoint reference
- [AGENT_PROTOCOL.md](AGENT_PROTOCOL.md) — CAP bus protocol and pointer semantics
- [sdk-reference.md](sdk-reference.md) — Go SDK client and worker runtime
- [configuration.md](configuration.md) — CORS and gateway environment variables
