# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]
- Initial public release structure and documentation refresh.

## [v0.1.4] - 2026-01-25
- Security: remove default API keys; deployments must supply `CORDUM_API_KEY`.
- Security: fail-closed API auth; enforce `X-Tenant-ID`; require policy signatures when enforcement is enabled.
- Dashboard: disable API key storage in localStorage (opt-in embed via `CORDUM_DASHBOARD_EMBED_API_KEY`).
- Breaking: clients must send `X-Tenant-ID` on all `/api/*` requests.
