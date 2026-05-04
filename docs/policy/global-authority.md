# Global Policy Authority

EDGE-052 unifies the policy authority that all four Cordum policy
evaluators consult into a single Global view. This document explains
the model, the bundle-to-section mapping, the Invariants security
floor, and the wire surface (`/api/v1/policy/global`).

## The four evaluators and what each section feeds

```
                ┌──────────────────────────────────────────────┐
                │  Global Policy Authority (single source)     │
                │   InputRules                                 │
                │   OutputRules                                │
                │   EdgeActionRules                            │
                │   MCPToolRules                               │
                │   Invariants  (DENY-uncrossable floor)       │
                └──────────────────────────────────────────────┘
                                     │
        ┌────────────────────────────┼────────────────────────────────┐
        ▼                ▼            ▼                                ▼
  Cordum job       Edge action   MCP tool call                    Output scan
  evaluator        evaluator     evaluator                        (post-job)
  (Safety Kernel   (Edge GW →    (Gateway MCP                     (Safety Kernel
   input rules)     Safety        gate)                            output rules)
                    Kernel)
```

| Section            | Read by evaluator     | Code path                                                                         |
|--------------------|-----------------------|-----------------------------------------------------------------------------------|
| `input_rules`      | Cordum job evaluator  | `core/controlplane/safetykernel/kernel.go evaluate()` iterates `s.policy.Rules`   |
| `output_rules`     | Output scanner        | `core/controlplane/safetykernel/output_policy.go EvaluateOutput / CheckOutput`    |
| `edge_action_rules`| Edge action evaluator | `core/controlplane/gateway/handlers_edge_evaluate.go evaluateEdgeSafety` → kernel |
| `mcp_tool_rules`   | MCP tool gate         | `core/controlplane/gateway/mcp_gate.go gatewayApprovalGate.Check`                 |
| `invariants`       | ALL FOUR              | `applyKernelInvariants` (kernel) + `MCPInvariantLookup` wired via `s.wireMCPApprovalGate` (gateway, see `handlers_mcp.go`) |

Implementation entry points:

- `safetykernel.GlobalPolicy` (`core/controlplane/safetykernel/global_policy.go`)
  is the typed cross-evaluator view. Its `RulesForInput` /
  `RulesForOutput` / `RulesForEdgeAction` / `RulesForMCPTool` accessors
  prepend Invariants so a first-match evaluator hits the security floor
  before any base ALLOW.
- `safetykernel.applyKernelInvariants`
  (`core/controlplane/safetykernel/kernel_invariants.go`) and
  `policybundles.ApplyInvariants`
  (`core/controlplane/gateway/policybundles/invariants.go`) are the two
  package-local mirrors that bake invariant DENYs at the front of
  `merged.Rules` and ALLOWs at the back.

## Invariants — the security floor

Invariants are rules authored ONCE in the dedicated
`secops/invariants` bundle and applied to ALL FOUR evaluators with
SECURITY-FLOOR precedence:

1. **Invariant DENY rules are uncrossable.** They are prepended to
   `merged.Rules` so a first-match evaluator returns DENY before
   reaching any pack-contributed ALLOW that targets the same input.
2. **Invariant ALLOW rules are a default fallback.** They are
   appended to `merged.Rules` so any explicit DENY (pack, studio
   global, file-loader) earlier in the slice still wins. Without this
   rule, pack-contributed safety DENYs authored before an invariant
   ALLOW default would become unenforceable.
3. **Invariant `output_rules` quarantine/redact at the front.** They
   are prepended to `merged.OutputRules` so an allow-mode pack output
   rule cannot silently override a content-pattern invariant.

Authoring example (a single Invariants bundle YAML):

```yaml
# secops/invariants — SecOps security floor
rules:
  - id: inv-deny-secret-paths
    match:
      labels:
        path.class: secret
    decision: deny
    reason: Secret-path access is uniformly forbidden across all surfaces
  - id: inv-deny-mcp-fs-read
    match:
      mcp:
        deny_tools: [fs.read]
    decision: deny
output_rules:
  - id: inv-quarantine-secret-output
    severity: critical
    decision: quarantine
    match:
      content_patterns:
        - 'OPENAI_API_KEY=[A-Za-z0-9]+'
        - 'AKIA[A-Z0-9]{16}'
```

The cross-evaluator integration test
(`core/controlplane/safetykernel/global_invariants_integration_test.go`,
build-tagged `//go:build integration`) pins this guarantee with four
subtests sharing ONE SnapshotHash.

## Bundle layout

The unified Global authority is expressed via the existing policy
bundle map (configsvc scope=`system`, id=`policy`, key=`bundles`):

| Bundle key                  | Authored by   | Section it feeds            | Pack-uninstall safe |
|-----------------------------|---------------|-----------------------------|---------------------|
| `secops/global-input`       | Studio (PUT)  | `input_rules`               | Yes                 |
| `secops/global-output`      | Studio (PUT)  | `output_rules`              | Yes                 |
| `secops/global-edge-action` | Studio (PUT)  | `edge_action_rules`         | Yes                 |
| `secops/global-mcp-tool`    | Studio (PUT)  | `mcp_tool_rules`            | Yes                 |
| `secops/invariants`         | Studio (PUT)  | `invariants` (security floor) | Yes               |
| `pack/<pack-id>/<fragment>` | Pack install  | merges into all sections    | Removed on uninstall|

**Pack uninstall is deterministic.** `handlers_packs.go
removePolicyOverlay` deletes by the pack overlay's FragmentID. The
invariants bundle is keyed under `secops/invariants` and is NEVER a
pack overlay's FragmentID, so total pack uninstall preserves the
security floor. The unit test
`pack_uninstall_removes_pack_rules_test.go
TestPackUninstallDoesNotTouchInvariantsBundleKey` pins this.

## Snapshot / cache invalidation

Every change to any bundle (studio + pack + invariants) bumps the
`cfg:<sha256>` snapshot identifier produced by
`policybundles.BuildPolicyFromBundles`:

- The kernel records `cfg:<sha>` on `s.snapshot` at
  `setPolicyWithInvariants` time, propagates it as
  `PolicyCheckResponse.PolicySnapshot` on every `Evaluate` /
  `EvaluateOutput`.
- The agentd safe-allow cache (EDGE-018) keys on this snapshot —
  changing invariants invalidates every cache entry uniformly.
- The dashboard `/api/v1/policy/global` GET response surfaces both
  `snapshot_version` and `snapshot_hash` (currently identical) so
  dashboard editors can pass it back on PUT for optimistic
  concurrency.

`TestInvariantSnapshotChangeBumpsCfgHash` (in
`policybundles/invariants_test.go`) pins that adding/removing the
invariants bundle changes the snapshot, and identical bundles produce
identical snapshots.

## Wire surface — `/api/v1/policy/global`

GET returns the unified five-section view:

```json
{
  "snapshot_version": "cfg:<sha>",
  "snapshot_hash":    "cfg:<sha>",
  "updated_at":       "2026-05-03T20:00:00Z",
  "sections": {
    "input_rules":       { "bundle_id": "secops/global-input",       "content": "...", "sha256": "...", "enabled": true },
    "output_rules":      { "bundle_id": "secops/global-output",      "content": "...", "sha256": "...", "enabled": true },
    "edge_action_rules": { "bundle_id": "secops/global-edge-action", "content": "...", "sha256": "...", "enabled": true },
    "mcp_tool_rules":    { "bundle_id": "secops/global-mcp-tool",    "content": "...", "sha256": "...", "enabled": true },
    "invariants":        { "bundle_id": "secops/invariants",         "content": "...", "sha256": "...", "enabled": true }
  }
}
```

PUT atomically writes any subset of sections:

```json
{
  "snapshot_version": "cfg:<sha>",   // optional optimistic concurrency token
  "author":           "alice@cordum",
  "message":          "Add deny-secret-paths invariant",
  "sections": {
    "invariants":   { "content": "rules:\n  - id: inv-deny-secret-paths\n    ..." },
    "input_rules":  { "content": "rules:\n  - id: studio-allow-test\n    ..." }
  }
}
```

- Sections not present in the request body are left untouched.
- A section with empty `content` deletes its bundle (writers can clear
  a section by PUTting `"content": ""`).
- `snapshot_version` non-empty AND != current snapshot returns 409
  Conflict.
- Unknown section names return 400 with the bad name in the message.
- Malformed YAML returns 400 with the bad section name in the message.

### Backward compatibility

The legacy `/api/v1/policy/bundles` GET / `/api/v1/policy/bundles/{id}`
GET / PUT / DELETE endpoints continue to work as backward-compat
aliases — they read/write individual bundle entries directly from the
same `system/policy/bundles` configsvc doc. Existing dashboard wiring
that targets the legacy surface is unaffected. Operators authoring
through the legacy `secops/global` key (a SafetyPolicy YAML containing
`rules` + `output_rules` + `output_policy` + `default_decision`) keep
that bundle entry; the unified GET projects it as part of the
`input_rules` section by convention. The new five-section bundle keys
(`secops/global-input` etc.) are an addition, not a replacement.

## Known limitations

- **Concurrent writer race on PUT /global.** The handler does
  read-modify-write of the configsvc doc; the snapshot_version check
  is best-effort optimistic concurrency, not a CAS. A pack install
  landing between the snapshot check and the `configsvc.Set` could be
  silently overwritten. Both code paths are admin-only; collisions are
  rare. A future pass can plumb a configsvc CAS API.
- **Section split is by primary topic.** A `PolicyRule` whose
  `Match.Topics` includes both `job.edge.action` and a Cordum-job topic
  (e.g. `job.deploy`) lands in the `edge_action_rules` bucket per
  `isEdgeActionRule` (any-topic-matches). The Cordum-job aspect is
  preserved in the kernel's first-match evaluation against
  `s.policy.Rules`, but the dashboard's typed view shows the rule in
  one bucket only. Authoring a rule with multiple topic intents is
  uncommon; if needed, duplicate the rule per intended bucket.
- **MCP server-scope matching uses `tool.ApprovalScope`.** `mcp.Tool`
  has no `Server` field, so invariant `Match.MCP.DenyServers` matches
  against `tool.ApprovalScope` (a tag the tool advertises). For
  fine-grained server-scope invariants, ensure tools advertise a
  consistent ApprovalScope per logical server.
- **Decision string typos silently no-op.** Invariant rules with
  unknown `decision` values (e.g. `dney` instead of `deny`) skip the
  DENY-uncrossable enforcement — they fall into the ALLOW bucket per
  `isInvariantDeny` semantics. Authors should validate via
  `config.ParseSafetyPolicy` errors at upload time.

## Cross-references

- `core/controlplane/safetykernel/global_policy.go` — typed view +
  constructor + section accessors
- `core/controlplane/safetykernel/kernel_invariants.go` — kernel-side
  precedence helpers + `currentGlobalPolicy()` accessor
- `core/controlplane/gateway/policybundles/invariants.go` — gateway-side
  precedence helpers + `SplitInvariantsFromBundles`
- `core/controlplane/gateway/handlers_policy_global.go` — wire surface
  handlers
- `core/controlplane/gateway/mcp_gate_invariants.go` — MCP gate hook
  (`MCPInvariantLookup` + `ErrMCPInvariantDeny`)
- `dashboard/src/lib/policy-studio/globalPolicy.ts` — dashboard
  parser/serializer round-tripping all 5 sections
- `docs/api/openapi/cordum-api.yaml` — OpenAPI spec for the new
  endpoint
