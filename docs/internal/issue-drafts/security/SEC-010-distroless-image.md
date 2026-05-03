# Distroless runtime image

Repo: cordum

## Problem
`SECURITY.md` claims minimal distroless images, but Dockerfile uses Alpine.

## Proposed
- Switch runtime stage to distroless static (nonroot) and keep CA certs.

## Acceptance
- Built binaries run with distroless runtime image.
- Docs updated to reflect base image.

## References
- Dockerfile
- SECURITY.md
