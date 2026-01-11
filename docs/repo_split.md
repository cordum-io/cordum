# Cordum Repo Layout (Core, Enterprise, Tools)

Cordum is split into focused repositories so the core stays clean and the
enterprise layer can evolve without forking core.

## Core repo (this repo)

- Control plane binaries (gateway, scheduler, safety kernel, workflow engine).
- Optional dashboard UI.
- CAP protocol integration and public SDK (`sdk/`).
- Pack format spec and public docs.
- BUSL-1.1 licensed core (free for self-hosted/internal use; no competing hosted/managed offering).

## Enterprise repo (`cordum-enterprise`)

- Enterprise-only binaries and modules (enterprise auth provider).
- License enforcement at startup for enterprise binaries.
- Enterprise deployment notes and ops docs.
- Commercial EULA.

## Tools repo (`cordum-tools`)

- License generation and signing tools.
- Internal scripts and operational runbooks.
- Apache-2.0 licensed tooling.

## Rules

- Do not commit private keys, customer data, or secrets to any repo.
- Keep enterprise features out of the OSS build; ship as separate binaries.
- Use signed licenses to unlock enterprise-only binaries.

## Release flow

- OSS: public tags/releases from the core repo.
- Enterprise: tags/releases from `cordum-enterprise`.
- Maintain a compatibility matrix (enterprise build <-> OSS core version).

## No duplication

Enterprise binaries depend on the public core as a module and use `replace` in
`go.mod` for local development. Shared changes live in the core repo.
