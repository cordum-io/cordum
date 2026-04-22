# Dependabot/Snyk dependency scanning

Repo: cordum

## Problem
`SECURITY.md` claims Dependabot and Snyk scanning, but no config exists in this repo.

## Proposed
- Add Dependabot config for Go and npm.
- Add (or document) Snyk CI integration gated by secrets.

## Acceptance
- Dependabot config present under .github.
- Snyk workflow or docs added with required secrets.

## References
- SECURITY.md
- .github/workflows
