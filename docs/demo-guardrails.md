# Guardrails Demo (2 minutes)

This demo shows:
- Safety Kernel blocking a dangerous request
- A remediation suggestion
- An approval gate for risky work

## 1) Start the stack

```bash
./cmd/cordumctl/cordumctl up
```

## 2) Start the demo worker

```bash
cd examples/demo-guardrails/worker
go run .
```

## 3) Install the demo pack

```bash
./cmd/cordumctl/cordumctl pack install ./examples/demo-guardrails --upgrade
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
