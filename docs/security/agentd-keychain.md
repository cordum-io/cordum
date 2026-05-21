# cordum-agentd Keychain Bootstrap (EDGE-152)

Status: ACTIVE — replaces the EDGE-031 P0 plaintext-env bootstrap path.
Owners: edge platform.
Cross-references: EDGE-150 managed-settings invariants, EDGE-151 binary signing, EDGE-031 P0 tradeoff log.

## 1. Overview

Before EDGE-152, cordum-agentd read its boot-time credentials —
`CORDUM_AGENTD_NONCE` (per-host external nonce) and `CORDUM_API_KEY`
(gateway-issued bearer token) — from the developer shell environment.
That path is a known tradeoff documented in the EDGE-031 P0 design:

> Runtime hook nonce and local credentials can be exposed to same-user
> process inspection — `ps -E`, `/proc/<pid>/environ`, and shell history
> all surface the values.

EDGE-152 closes this tradeoff by sourcing both secrets from the host's
OS-native credential store at process startup. The bootstrap path is
keychain-first, with an explicit env fallback gated by an opt-in `dev`
mode. In `strict` mode (enterprise default), any keychain miss is fatal
and agentd refuses to start with a `BOOTSTRAP-FAIL:` diagnostic.

The change is additive: cordum-agentd's existing strict-mode + redaction
helpers (`redactForStderr`, `isSensitiveEnvKey`,
`redactHookBoundaryString`, and the Claude-launcher managed-settings
invariant set) are unchanged. The keychain bootstrap runs *before*
`LoadConfig` consumes the env map, so downstream validators see only
keychain-sourced values.

## 2. Architecture

```
[OS keychain]                       [Process env / launchd EnvironmentVariables /
  cordum_agentd_nonce                systemd Environment= / WinSW <env>]
  cordum_api_key                       CORDUM_AGENTD_STRICT={true|false}
        \                              CORDUM_EDGE_POLICY_MODE=…
         \                             CORDUM_GATEWAY / TENANT_ID / …
          \                                                |
           \                                               |
            v                                              v
         +------ cmd/cordum-agentd/bootstrap.go ------+
         |  resolveBootstrapMode(env) → ModeStrict |  |
         |          | ModeDev                       |  |
         |  loadBootstrapSecrets(ctx, kr, mode, env)|  |
         |    for binding in {nonce, api_key}:      |  |
         |      keychain.LoadSecret(kr, mode, …)    |  |
         |        keychain hit  → use value         |  |
         |        miss + strict → BOOTSTRAP-FAIL    |  |
         |        miss + dev    → env fallback +    |  |
         |                        WARN banner       |  |
         +------------------------------------------+
                                |
                                v
                core/edge/agentd/config.go LoadConfig(env)
                core/edge/agentd/app.go ValidateExternalNonce(nonce)
                                |
                                v
                    cordum-agentd Run loop
```

Bindings (from `cmd/cordum-agentd/bootstrap.go`):

| Env name (read by agentdcore) | Keychain key (operator-facing)  | Required? |
|-------------------------------|---------------------------------|-----------|
| `CORDUM_AGENTD_NONCE`         | `cordum_agentd_nonce`           | Optional (empty → auto-generate) |
| `CORDUM_API_KEY`              | `cordum_api_key`                | Required (LoadConfig fails if absent) |

The hook-side `CORDUM_AGENTD_HOOK_NONCE` is intentionally NOT in this
table: it is issued per-session by the Claude launcher
(`core/edge/claude/launcher_metadata.go`) and never persisted to the
keychain by agentd.

## 3. Per-Platform Mapping

| Platform | Backend              | Provision CLI                                                                  |
|----------|----------------------|--------------------------------------------------------------------------------|
| macOS    | Keychain (Security)  | `security add-generic-password -a cordum_agentd_nonce -s cordum-agentd -w '<value>' -T /usr/local/bin/cordum-agentd -U` |
| Linux    | Secret Service / libsecret | `printf '%s' '<value>' \| secret-tool store --label='cordum-agentd nonce' service cordum-agentd username cordum_agentd_nonce` |
| Windows  | Credential Manager   | `cmdkey /generic:cordum-agentd:cordum_agentd_nonce /user:cordum_agentd_nonce /pass:<value>` |

Notes:

- **macOS** `-T <path>` scopes the Keychain ACL to the cordum-agentd
  binary so Console.app does not prompt on first read. `-U` updates the
  entry if already present (idempotent rotation).
- **Linux** the secret value is passed via stdin to `secret-tool`; the
  schema fields are positional after `service` and `username`. libsecret
  + a running D-Bus session are required; CI without these falls back to
  dev mode + the env passthrough.
- **Windows** `cmdkey` has no stdin mode — the only API path is
  `/pass:<value>` on the command line. The supplied PowerShell installer
  (`tools/scripts/agentd-install/install.ps1`) reads the value via
  `Read-Host -AsSecureString` and unwraps it to a discrete process
  argument exactly once; the in-process string copy is zeroed
  immediately after the call.

For all three backends, cordum-agentd calls
`go-keyring.Get("cordum-agentd", <key>)`, where `<key>` is one of
`cordum_agentd_nonce` / `cordum_api_key`. That maps to macOS
`service=cordum-agentd, account=<key>`, Linux Secret Service attributes
`service=cordum-agentd, username=<key>`, and Windows Credential Manager
target `cordum-agentd:<key>`.

## 4. Service-Manager Integration

cordum-agentd ships three service-manager templates under
`tools/scripts/`:

| OS      | File                                                        | Service manager |
|---------|-------------------------------------------------------------|-----------------|
| macOS   | `tools/scripts/launchd/com.cordum.agentd.plist`             | launchd (user-mode) |
| Linux   | `tools/scripts/systemd/cordum-agentd.service`               | systemd (--user)    |
| Windows | `tools/scripts/windows/cordum-agentd-service.xml`           | WinSW               |

All three templates carry `CORDUM_AGENTD_STRICT=true` as their *only*
operational env and contain **no secret-bearing env entries**. Secrets
flow from the keychain via the EDGE-152 bootstrap path.

`tools/scripts/agentd-install/install.sh` (POSIX) and `install.ps1`
(Windows) glue together credential provisioning + template installation
+ service registration. `tools/scripts/agentd-install/synthetic-test/run.sh`
is the adversarial-leak fixture: it provisions synthetic values, starts
agentd, and `grep -F`s stdout / stderr / journald / committed unit
files for verbatim secret bytes. CI failure on any non-zero exit.

## 5. Strict vs Dev Mode

Mode is resolved in `resolveBootstrapMode(env)`:

```go
if parseBoolEnv(envValue(env, "CORDUM_AGENTD_STRICT")) {
    return keychain.ModeStrict
}
if envValue(env, "CORDUM_EDGE_POLICY_MODE") == "enterprise-strict" {
    return keychain.ModeStrict
}
return keychain.ModeDev
```

| Condition                          | ModeStrict outcome                                     | ModeDev outcome                          |
|------------------------------------|--------------------------------------------------------|------------------------------------------|
| Keychain hit                       | use keychain value                                     | use keychain value                       |
| Keychain miss + env fallback set   | `BOOTSTRAP-FAIL` (env IGNORED)                         | use env fallback + structured WARN       |
| Keychain miss + env fallback empty | `BOOTSTRAP-FAIL` (required) / pass empty (optional)    | pass empty → downstream validators       |
| Keychain unavailable + env set     | `BOOTSTRAP-FAIL: keychain unavailable in strict mode`  | use env fallback + structured WARN       |
| Keychain unavailable + env empty   | `BOOTSTRAP-FAIL`                                       | `BOOTSTRAP-FAIL` (no source available)   |

All `BOOTSTRAP-FAIL` diagnostics include the keychain key + mode +
failure category. No diagnostic ever contains the secret value, even
when a populated env variable was ignored.

Default is ModeDev so an unprovisioned local checkout still boots with
a banner-warn. Production deploys (and the three service-manager
templates) opt into ModeStrict explicitly.

## 6. Trust Boundary

### Prevents

| # | Threat                                                                  | Why this design closes it |
|---|-------------------------------------------------------------------------|---------------------------|
| a | Same-user `ps -E` / `/proc/<pid>/environ` reading nonce                 | Secrets never enter the process env table; cordum-agentd's in-memory copy lives only in resolved local variables. |
| b | Shell-history persistence of `export CORDUM_API_KEY=…`                  | Provisioning happens via stdin or sealed prompt in `install.sh` / `install.ps1`. Operator never types the value on a command line that gets persisted. |
| c | Claude `settings.json` (or managed-settings JSON) carrying secrets      | EDGE-150 invariant #14 already rejects secret-bearing env entries in settings.json. EDGE-152 is the supported alternative provisioning path; settings.json is unaffected. |
| d | dotfile rc-file persistence in `~/.bashrc` / `~/.zshrc` / `$PROFILE`    | The same as (b): keychain CLI provisioning is the only documented path. |
| e | Backup tooling (`tar`, `restic`, Time Machine) snapshotting env files   | No env files exist for the keychain values. |

### Does NOT prevent

| # | Threat                                                                  | Why this design does not cover it |
|---|-------------------------------------------------------------------------|-----------------------------------|
| f | Root / admin user dumping the keychain                                  | Out of scope. macOS Keychain, libsecret, and Credential Manager all trust local administrative principals by design. Mitigation belongs to OS-level disk encryption + admin-account hygiene. |
| g | Memory dump of a running agentd process                                 | Out of scope. Any process that has root/admin and can read another process's memory can extract the in-memory secret. Mitigation belongs to OS process protections (SIP, ptrace_scope, Credential Guard). |
| h | Keychain-unlock phishing / social engineering                           | Out of scope. If an operator approves a malicious "agent wants to read your Keychain" prompt, no in-code defense reaches that surface. |
| i | Build-environment compromise (binary substitution)                      | Out of scope here; addressed by EDGE-151 (cordum-agentd binary signing + notarization). |
| j | OS kernel keylogger on the provisioning host                            | Out of scope. |
| k | Backups of the keychain itself (macOS Keychain Access export, journal'd Secret Service collection backups) | Operator policy — document a "no Keychain export to unencrypted media" rule. |

## 7. Key Rotation

Rotation is a three-step ritual:

```bash
# 1. Revoke the existing entry (idempotent — succeeds if missing).
security delete-generic-password -a cordum_agentd_nonce -s cordum-agentd  # macOS
secret-tool clear service cordum-agentd username cordum_agentd_nonce      # Linux
cmdkey /delete:cordum-agentd:cordum_agentd_nonce                          # Windows

# 2. Provision the new value (sealed prompt / pipe; never on argv where avoidable).
tools/scripts/agentd-install/install.sh --rotate                          # POSIX
.\tools\scripts\agentd-install\install.ps1 -Rotate                        # Windows

# 3. Restart cordum-agentd so the bootstrap path picks up the new value.
launchctl kickstart -k "gui/$(id -u)/com.cordum.agentd"                   # macOS
systemctl --user restart cordum-agentd.service                            # Linux
sc.exe stop cordum-agentd && sc.exe start cordum-agentd                   # Windows
```

The keychain is the only source-of-truth in strict mode. Operators do
NOT need to coordinate rotation across multiple env files, shell rc
files, or systemd drop-ins.

## 8. Audit Event Schema

cordum-agentd emits one structured-log line per bootstrap outcome.
Schema (slog text format):

```
time=… level=INFO  msg=keychain.load         secret_name=<key> source=keychain     mode=<strict|dev>
time=… level=WARN  msg=keychain.env_fallback secret_name=<key> source=env-fallback mode=dev reason=<keychain miss|keychain unavailable: …>
time=… level=ERROR msg=keychain.load.miss        secret_name=<key> mode=strict
time=… level=ERROR msg=keychain.load.unavailable secret_name=<key> mode=strict reason=<truncated backend diagnostic>
```

The `reason` field is bounded at 200 chars by `keychain.redactBackendError`
and is stripped of newline characters before emission. The schema NEVER
includes the secret value, even on the success path; the value flows
only through the function return path into `LoadConfig` / `ValidateExternalNonce`.

For downstream alerting:

- `msg=keychain.env_fallback` in production (where `mode=strict` is
  expected) indicates accidental dev mode and should page the
  operator on duty.
- `msg=keychain.load.unavailable` indicates the keychain backend
  itself is broken (D-Bus down, Credential Manager service stopped,
  Keychain locked headless) and is a hard fail.
- `msg=keychain.load.miss` with `mode=strict` indicates provisioning
  drift between operators and the running host.

## 9. Ops Runbook

### First-run provisioning

```bash
# macOS
brew install gnu-sed         # only required if your distro of sed differs
cordum-agentd --help         # sanity check the binary is on PATH
tools/scripts/agentd-install/install.sh

# Linux
sudo apt install libsecret-tools systemd-container   # debian/ubuntu
tools/scripts/agentd-install/install.sh

# Windows (PowerShell elevated)
.\tools\scripts\agentd-install\install.ps1
# (one-time) Download WinSW.NET4.exe → rename to cordum-agentd-service.exe
#            and drop into the install path printed by the installer.
```

### Verifying

```bash
# Are the entries in the keychain?
security find-generic-password -a cordum_agentd_nonce -s cordum-agentd   # macOS
secret-tool search service cordum-agentd username cordum_agentd_nonce    # Linux
cmdkey /list:cordum-agentd:cordum_agentd_nonce                           # Windows

# Is the service alive?
launchctl print "gui/$(id -u)/com.cordum.agentd" | head -20              # macOS
systemctl --user status cordum-agentd.service                            # Linux
sc.exe query cordum-agentd                                               # Windows

# Did the bootstrap path actually use the keychain? (Check structured logs.)
journalctl --user -u cordum-agentd.service -n 50 \
    | grep -E 'keychain\.(load|env_fallback)'
```

The values themselves never appear in any of these surfaces. If you see
a value verbatim in logs / `--diagnose` / ps output, treat it as a P0
incident and run the synthetic-test fixture
(`tools/scripts/agentd-install/synthetic-test/run.sh`) to reproduce
before filing.

### Common failures

| Symptom                                                              | Diagnosis                                                                          | Fix |
|----------------------------------------------------------------------|------------------------------------------------------------------------------------|-----|
| `BOOTSTRAP-FAIL: keychain unavailable in strict mode`                | Backend not running: macOS keychain locked headless, Linux D-Bus session missing, Windows Credential Manager service stopped. | macOS: unlock keychain (or run as the logged-in user). Linux: ensure `systemd --user` + `dbus-daemon --session` are up; CI may need `dbus-launch`. Windows: `sc.exe start vaultsvc`. |
| `BOOTSTRAP-FAIL: secret "cordum_api_key" not in keychain`            | Provisioning was never run, or entry was deleted out-of-band.                      | Re-run `install.sh` / `install.ps1` (without `--rotate` to add only the missing entry). |
| `keychain.env_fallback ... source=env-fallback mode=dev` in prod    | Service template is missing `CORDUM_AGENTD_STRICT=true`, or operator set it to false. | Restore the template default; restart the service. |
| `keychain.load.unavailable ... permission denied`                    | macOS Keychain ACL did not grant cordum-agentd binary access.                      | Re-run provisioning with `-T /usr/local/bin/cordum-agentd` (already the default in `install.sh`); operator must approve the keychain prompt once. |

## 10. Related Work

- EDGE-031 P0 plaintext-env tradeoff (the original constraint this
  task removes).
- EDGE-150 managed-settings invariant set (invariant #14 prohibits
  secret-bearing env entries in Claude `settings.json`; EDGE-152 is the
  alternative provisioning path that satisfies the invariant for the
  daemon's own bootstrap).
- EDGE-151 cordum-agentd binary signing + notarization (mitigates the
  build-environment substitution threat (i) in §6 above).
