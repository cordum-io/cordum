# CAP bus signature verification

Repo: cordum

## Problem
`SECURITY.md` claims workflow signature validation, but CAP bus signatures are not enforced.

## Proposed
- Add optional verification for BusPacket signatures when configured.
- Define canonical signing input (proto with signature cleared) and supported algorithms.

## Acceptance
- Packets without valid signatures are rejected when enforcement is enabled.
- Docs updated with signing guidance.
- Tests cover valid/invalid signatures.

## References
- docs/AGENT_PROTOCOL.md
- core/infra/bus
- SECURITY.md
