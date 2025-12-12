# coretexOS Dashboard (web/dashboard)

Thin React/Vite UI for the control plane:
- Worker Mesh: `/api/v1/workers`
- Job Explorer: `/api/v1/jobs`, `/api/v1/jobs/:id`, `/api/v1/traces/:id`
- Live stream: WebSocket `/api/v1/stream`
- Chat: submits `POST /api/v1/jobs` (defaults to `job.chat.simple`)

## Run locally
```bash
cd web/dashboard
npm install
# Configure .env.local (see below)
npm run dev
# open http://localhost:5173
```

## Env (.env.local)
```
VITE_API_BASE=http://localhost:8081
VITE_WS_BASE=ws://localhost:8081/api/v1/stream   # optional; derived from API_BASE if omitted
VITE_API_KEY=your-api-key                        # optional; matches gateway API_KEY
```
- HTTP requests send `X-API-Key` when `VITE_API_KEY` is set.
- WebSocket appends `?api_key=...` for the same key (gateway accepts header or query param).

## Notes
- Result/worker payloads normalize camelCase/snake_case from protojson.
- If API_KEY is unset on the gateway, you can omit `VITE_API_KEY`.
