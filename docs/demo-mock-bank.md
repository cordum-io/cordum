# Mock Bank Demo (5 minutes)

This demo shows:
- A mock bank UI alongside the Cordum dashboard
- Policy enforcement for low, medium, and high transfer amounts
- Human approval gating before funds move

## 1) Start the stack

```bash
./bin/cordumctl up
```

## 2) Install the demo pack

```bash
./bin/cordumctl pack install --upgrade ./demo/mock-bank/pack
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

## 5) Run the flow

1. Send a $40 transfer request in the chat. It auto-executes.
2. Send a $500 transfer request. It pauses for approval.
3. Approve the request in the dashboard (Policy tab).
4. Send a $5,000 transfer request. It is blocked by policy.

## Notes

- The dashboard defaults to `http://localhost:8082`.
- The demo UI uses the Cordum API at `http://localhost:8081` with `[REDACTED]` by default.
