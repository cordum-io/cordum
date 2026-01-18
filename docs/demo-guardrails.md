# Guardrails Demo (2 minutes)

This demo shows:
- Safety Kernel blocking a dangerous request
- A remediation suggestion
- An approval gate for risky work

If you want a local `cordumctl` binary:

```bash
make build SERVICE=cordumctl
```

## 1) Start the stack

```bash
./bin/cordumctl up
```

## 2) Start the demo worker

```bash
cd examples/demo-guardrails/worker
go run .
```

## 3) Install the demo pack

```bash
./bin/cordumctl pack install --upgrade ./examples/demo-guardrails
```

## 4) Run the demo script

```bash
./tools/scripts/demo_guardrails.sh
```

## Record a GIF

Option A: VHS (terminal GIF recorder)

```bash
# macOS: brew install vhs
# Linux: https://github.com/charmbracelet/vhs
vhs ./tools/scripts/demo_guardrails.tape
```

Option B: GUI recorder
- macOS: Kap
- Linux: Peek
- Windows: ShareX

Save the output as `docs/assets/guardrails-demo.gif`.
