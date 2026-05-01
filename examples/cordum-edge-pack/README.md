# Cordum Edge Policy Pack Example

This policy-only example pack contains templates and executable fixtures for Claude Code / Cordum Edge P0 demonstrations. It uses the normalized policy input contract from the Edge classifier and Gateway evaluate path:

- topic: `job.edge.action`
- capability: classifier-owned values such as `exec.shell`, `file.read`, `file.write`, and `edge.unknown`
- risk tags: classifier-owned values such as `test`, `build`, `secrets`, `destructive`, `write`, `git`, `network`, and `unknown`
- labels: normalized labels such as `hook.tool_name`, `command.class`, `command.family`, `path.class`, and `unknown.impact`

The synthetic `job.edge.action` topic is a Safety Kernel policy namespace only. It is not a Cordum Job dispatch topic and does not make Claude tool actions into Cordum Jobs.

The `pack.yaml` manifest is intentionally policy-only: it does not define pools, timeouts, schemas, workflows, or dispatch topics, and it does not dispatch Claude tool calls. Label-bearing Edge simulations live in `fixtures/policy-simulations.json` because the current pack `policySimulations` shape cannot carry Edge labels.

## Fragments

- `overlays/policy.fragment.yaml` is demo-oriented. It denies secret reads and destructive shell commands, requires approval for file edits, git push, and generic network egress, and allows safe local tests/builds.
- `overlays/policy.production.fragment.yaml` is narrower and production-oriented. It keeps deny-by-default behavior for secrets, destructive shell, and unknown high-risk actions; allows safe tests/builds with constraints; and requires approval only for source-code edits, git push, and generic network egress categories that the classifier labels as high impact.

## Enterprise boundary

The production-oriented fragment is not a complete enterprise enforcement boundary. Real enterprise deployment still requires managed Claude settings, cordum-agentd installation/enforcement, short-lived tokens, OS/tenant controls, audit retention, and tenant-specific policy review. Treat these files as reusable policy starting points and regression fixtures, not a substitute for endpoint hardening or local host controls.

## Fixtures

`fixtures/policy-simulations.json` contains synthetic, redacted Edge action cases. Do not paste real `.env` contents, credentials, tokens, raw hook payloads, transcripts, or tool results into fixtures.

See `../../docs/edge-policy.md` for the operator-facing mapping table covering
capability, `risk_tags`, Edge labels, demo vs production fragment behavior, and
the reminder that `job.edge.action` is a policy namespace rather than a Cordum
Job topic.
