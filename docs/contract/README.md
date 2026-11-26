# Security contract

This directory is the roadmap M0 security contract. It records what Cordum protects, where each
protection is enforced and what has been verified. It records requirements and current status;
it does not qualify a release or a deployment.

- Contract version: **1**, the `schema_version` shared by the YAML files below.
- Frozen at the integration/next commit recorded on task-e2f303cd. That is the commit the M0 exit
  gate ran against: the first parent of the merge that added this index.

## Artifacts

| Artifact | What it holds | Checked by |
|---|---|---|
| [threat-model.md](threat-model.md) | Platform threat model: assets, adversaries, trust boundaries, trusted-component inventory, reference-monitor properties, non-promises and the deployment boundary. | No `tests/contract` validator. `tools/docscheck` `TestDocsMatchProduct` checks its endpoint examples. |
| [protected-operation.md](protected-operation.md) | The first protected operation (applying an approved Kubernetes manifest), its reference deployment, alternate paths AP01-AP05 and success criteria SC01-SC06. Planned, not implemented. | No `tests/contract` validator. `TestDocsMatchProduct` checks its endpoint examples. |
| [claims.yaml](claims.yaml) | Claim register: each claim's statement, boundary, status, evidence, invariants, findings, enforcement points, tests and public sources. | `claims.go` (`TestClaimsRegister`); `crosscheck_test.go` against invariants.yaml. |
| [invariants.yaml](invariants.yaml) | Invariants I01-I10 with testable forms, entry points with code anchors, the R01-R15 finding map (ids only), linked tests and suites. | `invariants.go` (`TestInvariantsCatalog`); `crosscheck_test.go`. |
| [enforcement-matrix.yaml](enforcement-matrix.yaml) | One row per entry point, one column per control or `PolicyConstraints` field. Each cell is enforced, advisory, ignored or not_applicable with its code ref. `ref_tests` links every enforced ref to tests, or records a gap. | `matrix.go` (`TestMatrixRepoFileIsValid`, `TestMatrixColumnsTrackPolicyConstraints`). |
| [failure-behavior.md](failure-behavior.md) | Required and current behavior per dependency and action class when a dependency is unavailable, with gaps G01-G09 and their owners. | No `tests/contract` validator. Its Verification section runs every cited test; `TestDocsMatchProduct` checks its endpoint examples. |
| [license-behavior.md](license-behavior.md) | What each license state may change, per capability class. | `core/licensing` `TestLicenseBehaviorContract`. |
| [data-inventory.yaml](data-inventory.yaml) | Source-reviewed data classes: where each item is stored, its writers, retention, deletion and legal hold. | `data_inventory.go` (`TestDataInventoryFile`, `TestDataInventoryKeyCoverage`). |
| [public-claims-baseline.md](public-claims-baseline.md) | Method for the public-claims inventory: pins, generator, surfaces, entry format and rules. | No validator reads it; its Checks section lists the rules `public_claims.go` applies to public-claims/. `TestDocsMatchProduct` checks its endpoint examples. |
| [public-claims/](public-claims/) | Per-surface hit manifests (`.hits`) and their classifications (`.md`) against claims.yaml. | `public_claims.go` (`TestPublicClaimsBaseline`). |

## Exit gate

M0's exit gate: every protection claim has an enforcement point and a testable invariant, or is
marked with a status that does not claim one.

- The claim validator requires an `enforced` or `verified` claim to name invariants and an
  enforcement point, and a `verified` claim to name a test and no finding. At the freeze no claim is
  `enforced` or `verified`.
- Every invariant has a testable form. All ten are `violated` at the freeze, because each maps to
  an open finding.
- `crosscheck_test.go`: no `verified` claim names a `violated` invariant, and every invariant id a
  claim names exists in invariants.yaml.
- Every code ref of an enforced matrix cell lists tests in `ref_tests` or records a gap. Gaps at
  the freeze:
  - `core/controlplane/scheduler/engine.go:1696` (scheduler throttle requeue)
  - `core/controlplane/scheduler/engine.go:1823` (`budgets.max_concurrent_jobs` limit)

## Gate commands

Run from the repository root:

```bash
go test ./tests/contract/... -count=1
go test ./core/licensing -run '^TestLicenseBehaviorContract$' -count=1
go test ./tools/docscheck -run '^TestDocsMatchProduct$' -count=1
make test   # go test ./..., which includes the three commands above
```

The commands in failure-behavior.md's Verification section run the tests that document cites.
