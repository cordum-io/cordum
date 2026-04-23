# Cordum Repo Layout (Core, Tools, Packs)

Cordum lives in focused repositories so the control plane, the license signing
tooling, and the integration pack catalog can evolve independently. The former
`cordum-enterprise` repo was retired 2026-04-23 — all enterprise features now
ship from core behind license entitlements.

## Core repo (this repo)

- Control plane binaries (gateway, scheduler, safety kernel, workflow engine).
- Dashboard UI.
- CAP protocol integration and public SDK (`sdk/`).
- Pack format spec and public docs.
- Enterprise surface: SSO/SAML/SCIM, advanced RBAC, SIEM export, legal hold,
  velocity rules, agent identity — all compiled in, runtime-gated by license
  entitlements (`core/licensing/`).
- BUSL-1.1 licensed core (free for self-hosted/internal use; no competing
  hosted/managed offering).

## Tools repo (`cordum-tools`)

- License generation and signing tools (`cmd/licensegen/`).
- Enterprise license agreement generator.
- Pack deployment scripts.
- Internal/operational only — not published.

## Packs repo (`cordum-packs`)

- Official pack bundles and public catalog.
- Builds `catalog.json` + `.tgz` bundles published to `https://packs.cordum.io`.
- Keeps pack content decoupled from the core control-plane repo.

## Rules

- Do not commit private keys, customer data, or secrets to any repo.
- Enterprise features are gated at runtime by signed license — do not bypass
  entitlement checks, add shims, or keep compatibility aliases for retired
  surfaces.

## Release flow

- Public tags/releases from the core repo cover both the OSS surface and the
  licensed enterprise surface; entitlements determine which features unlock.

## Retired: `cordum-enterprise`

The separate enterprise repo was archived 2026-04-23 once every enterprise
feature lived in core behind a license entitlement. See
`docs/release-notes/unreleased.md` for the retirement note.
