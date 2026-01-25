# Mock Bank Demo (5 minutes)

This demo shows:
- A mock bank UI alongside the Cordum dashboard
- Policy enforcement for low, medium, and high transfer amounts
- Human approval gating before funds move

## Prereqs

- Docker + Docker Compose
- Go (for the demo worker)
- Python 3 (or another static file server)

## 1) Start the stack

```bash
export CORDUM_API_KEY=<your-api-key>
export CORDUM_TENANT_ID=default
go run ./cmd/cordumctl up
# or:
./bin/cordumctl up
# or:
docker compose up -d
```

## 2) Install the demo pack

```bash
./bin/cordumctl pack install --upgrade ./demo/mock-bank/pack
```

If you didn't build the binary, use:

```bash
go run ./cmd/cordumctl pack install --upgrade ./demo/mock-bank/pack
```

## 3) Start the mock bank worker

```bash
cd demo/mock-bank/worker
go run .
```

## 4) Serve the demo UI

```bash
cd demo/mock-bank
python3 -m http.server 8099
```

On Windows, use:

```bash
py -m http.server 8099
```

Open `http://localhost:8099` in your browser.

One-command runner (installs pack + starts worker + serves UI):

```bash
./tools/scripts/demo_mock_bank.sh
```

## 5) Run the flow

1. Send a $40 transfer request in the chat. It auto-executes.
2. Send a $500 transfer request. It pauses for approval.
3. Approve the request in the dashboard (Policy tab).
4. Send a $5,000 transfer request. It is blocked by policy.

## Notes

- The dashboard defaults to `http://localhost:8082`.
- The demo UI uses the Cordum API at `http://localhost:8081` and requires an API key.
- Open `http://localhost:8099/?apiKey=YOUR_KEY&tenantId=default` (add `apiBaseUrl=http://localhost:8081` if needed).
- Query parameters (`apiBaseUrl`, `tenantId`, `principalId`, `principalRole`, `orgId`) persist to localStorage after the first load (API keys stay in-memory).
