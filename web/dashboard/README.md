# coretexOS Studio (Dashboard UI)

React + Vite + TypeScript + Tailwind UI for the coretexOS API gateway.

## Local dev

```bash
cd web/dashboard
npm install
npm run dev
```

If your gateway enforces an API key (`CORETEX_API_KEY` / `CORETEX_SUPER_SECRET_API_TOKEN` / `API_KEY`), set it either:
- in the UI at `Settings → API Key`, or
- via `web/dashboard/.env.local`:
  ```bash
  VITE_API_BASE=http://localhost:8081
  VITE_WS_BASE=ws://localhost:8081/api/v1/stream
  VITE_API_KEY=super-secret-key
  ```

## Docker compose

Use `docker compose up -d` and open `http://localhost:3000/`. The `coretex-dashboard` container injects the key via `CORETEX_DASHBOARD_API_KEY` into `runtime-config.js`.

