# demo-quickstart

A 30-second, three-verdict Cordum governance demo.

Three topics, three outcomes, one hello-world agent:

| Topic | Rule | Verdict |
|---|---|---|
| `job.demo.greet` | `demo-quickstart-greet-allow` | `ALLOW` |
| `job.demo.delete-all` | `demo-quickstart-delete-deny` | `DENY` |
| `job.demo.admin` | `demo-quickstart-admin-approve` | `REQUIRE_APPROVAL` |

The demo proves that Cordum can evaluate, gate, and escalate every
agent call — on a single workflow that a new operator can read in under
a minute.

## Run it

```bash
# 1. Bring up the stack (from the repo root).
./tools/scripts/quickstart.sh

# 2. Install the pack and run the demo.
cordumctl pack install ./demo/quickstart/pack
cordumctl demo run quickstart
```

You'll see a three-row verdict table and the approval command for the
`REQUIRE_APPROVAL` job. Total wall-clock < 30 s.

## What's inside

```
demo/quickstart/
├── pack/
│   ├── pack.yaml                       # manifest: 3 topics + 1 workflow
│   ├── overlays/
│   │   └── policy.fragment.yaml        # 3 safety rules (allow/deny/approve)
│   └── workflows/
│       └── hello.yaml                  # 4-step run that fans out to all 3 topics
├── worker/
│   ├── main.go                         # greets — subscribes only to job.demo.greet
│   └── main_test.go
├── Dockerfile                          # multi-stage Go build, non-root runtime
├── test-job.json                       # input fixture
├── expected-output.json                # golden output for integration test
├── pack_test.go                        # validates manifest + workflow + policy
└── integration_test.sh                 # CORDUM_INTEGRATION=1 end-to-end test
```

## What each rule means

| Rule | Why |
|---|---|
| `demo-quickstart-greet-allow` | Greet is a read-only, zero-risk operation. Cordum proves the ALLOW path with no friction — this is what "governance" should feel like in the common case. |
| `demo-quickstart-delete-deny` | "Delete-all" is the archetypal destructive action. Cordum blocks it at the kernel BEFORE the worker ever sees it. The agent pool cannot misbehave. |
| `demo-quickstart-admin-approve` | Admin operations must be escalated. Cordum suspends the run and hands control to a human — the job ID and approval command are printed for the operator. |

## Extending

Add your own topic and rule in three steps:

1. Add an entry under `topics:` in `pack/pack.yaml`.
2. Add a rule in `pack/overlays/policy.fragment.yaml`.
3. Add a step in `pack/workflows/hello.yaml` (or write a new workflow).

`cordumctl pack install ./demo/quickstart/pack --upgrade` applies the
changes without restarting the stack.

## Uninstalling

```bash
cordumctl pack uninstall demo-quickstart --purge
```

## Troubleshooting

| Symptom | Fix |
|---|---|
| `cordumctl pack install` fails with `pack not found` | Confirm the directory you passed contains `pack.yaml`. |
| Demo hangs past 30 s | Check the scheduler is up: `docker compose logs scheduler`. |
| `REQUIRE_APPROVAL` job never resolves | Approval is deliberately blocking — approve from a second shell: `cordumctl approval job <id> --approve`. |
| All verdicts show as `DENY` | The policy fragment did not install. Rerun `cordumctl pack install --upgrade`. |
