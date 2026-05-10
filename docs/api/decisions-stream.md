# Policy Decisions Stream

`GET /api/v1/policy/decisions/stream` upgrades to a WebSocket and emits one
`Decision` JSON per message as the gateway publishes evaluator output. The
endpoint complements `GET /api/v1/policy/decisions` (REST list); use the
list endpoint for historical queries and the stream endpoint for live mode.

## Authentication

Same auth gate as the REST endpoint (`auth.PermPolicyRead`). The standard
`cordum-api-key` Sec-WebSocket-Protocol subprotocol is honored — pass the
identifier first, the credential as the next subprotocol entry per the
existing gateway WS pattern.

## Per-message schema

Each WebSocket TEXT frame carries one `Decision` JSON object:

```json
{
  "source": "edge",
  "rule_id": "edge.tool.shell-block",
  "bundle_id": "bundle.acme.edge",
  "bundle_version": "v1",
  "type": "deny",
  "trace": [
    {
      "rule_id": "edge.tool.shell-block",
      "decision_type": "deny",
      "reason": "shell.exec denied at edge",
      "timestamp": "2026-05-10T12:00:00Z"
    }
  ],
  "input_ref": "blob://edge/01HT",
  "audit_hash": "sha256:0009",
  "timestamp": "2026-05-10T12:00:00Z"
}
```

The schema matches `components.schemas.Decision` in `cordum-api.yaml` 1:1.
Decisions are emitted as they happen — there is no batching.

## Server-side filtering

Two optional query parameters narrow what reaches your socket; filtering
runs in the gateway before WriteMessage so the network never carries
unwanted decisions:

- `?source=job` or `?source=edge` — drops the other source.
- `?type=deny` (or any unified DecisionType) — drops the others.

Both filters can combine.

## Back-pressure

The gateway's in-process `DecisionBroker` is the fan-out:

- Each WS connection gets a 64-decision buffered channel.
- Publishers use a non-blocking send; if the channel is full, the publish
  is dropped (the slow consumer loses messages, the emit path keeps
  flowing).
- After 3 consecutive drops, the broker auto-unsubscribes the slow
  consumer and closes the channel. The handler's writer goroutine sees
  the close and tears the WebSocket down.
- Per-message `WriteDeadline=5s`. If a TCP write blocks longer than that
  the connection closes; the broker subscriber is removed in the same
  shutdown path.

Net: a slow WS client gets disconnected within a few seconds, never
back-pressures the safetykernel/edge evaluator. Healthy clients
re-establish.

## Reconnect behavior

Reconnects are unconditional — clients are expected to reconnect after
disconnects (network, idle timeout, broker auto-evict). The broker has no
fixed subscriber cap; storms of 1k simultaneous reconnects fit inside a
~344 KB envelope (88-byte subscriber + 64-decision buffer ≈ 256 bytes per
client × 1000 = ~344 KB). The OS's open-fd limit on the gateway process
is the practical ceiling.

There is no resume cursor on the stream. To recover decisions emitted
during a disconnect, query the REST endpoint with a `since=` timestamp
covering the gap; the Dashboard's "Live mode" surfaces this as a "catch
up" nudge.

## Multi-replica deployments

This is an in-process broker. A multi-replica gateway deployment would
need NATS-fronted fan-out so a Decision emitted on replica A reaches a WS
client connected to replica B. Tracked as a follow-up; the broker
interface is small enough that swapping in a NATS-backed implementation
is mechanical.

## Job vs Edge decisions

- **Edge decisions** (gateway-side hook events) are emitted to the broker
  immediately after `EmitDecisionForEdgeEvent` succeeds and AppendDecision
  has persisted the row. Order-of-write guarantees a WS client never sees
  a Decision that isn't yet in `gov:dec:*`.
- **Job decisions** (scheduler-side pipeline events) are persisted to
  `gov:dec:*` but not yet published to the gateway broker — the scheduler
  runs in a separate process. Live-mode UIs that need job decisions today
  should poll the REST endpoint with a rolling `since=` window.
