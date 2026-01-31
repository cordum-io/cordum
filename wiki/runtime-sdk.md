# Runtime SDK

Tags: runtime-sdk, cap, sdk, workers, heartbeats

Cordum ships a thin wrapper around the CAP runtime at `sdk/runtime`.

What it provides:
- Typed handlers with context pointer hydration (CAP runtime).
- Deterministic envelope signing + publishing helpers.
- Heartbeat/progress/cancel helpers that match CAP subjects.
- Direct worker subject helper (`worker.<id>.jobs`) for scheduler routing.

Notes:
- The legacy worker API in `sdk/runtime` was removed. Use `runtime.Agent` for handlers.
- For heartbeats/progress/cancel, use the helpers in `sdk/runtime`.
