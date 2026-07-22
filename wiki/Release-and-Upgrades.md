# Release and Upgrades

## Versioning

Cordum follows semantic versioning. In `v0.x`, breaking changes may still
occur, so review release notes before upgrading.

## Upgrade checklist

- Review release notes for config changes.
- Backup Redis and NATS JetStream state.
- Update image tags (Compose/Helm) to the new version.
- Run the platform smoke test after deploy (`bash ./tools/scripts/platform_smoke.sh`).

## Downgrade safety

If you need to roll back, restore backups for Redis and JetStream and pin the
previous image tags.
