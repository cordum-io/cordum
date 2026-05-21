package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func waitMs(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

const (
	canonicalMCPGatewayEndpoint  = "https://localhost:8081/api/v1/mcp/gateway/upstream"
	legacyBareMCPGatewayEndpoint = "https://localhost:8081/api/v1/mcp/gateway"
)

// gatewayRef is the canonical cordum-gateway UpstreamServerRef used
// across the attach test matrix. Mirrors the EDGE-101 UpstreamServer
// minimum-for-attach shape (Name + Transport + Endpoint; Command empty
// for HTTP).
func gatewayRef() UpstreamServerRef {
	return UpstreamServerRef{
		Name:      "cordum-gateway",
		Transport: "http",
		Endpoint:  canonicalMCPGatewayEndpoint,
	}
}

func codexGatewayRef(transport string) UpstreamServerRef {
	ref := gatewayRef()
	ref.Transport = transport
	return ref
}

func codexStdioGatewayRef() UpstreamServerRef {
	return UpstreamServerRef{
		Name:      "cordum-gateway",
		Transport: "stdio",
		Command:   []string{"cordum-mcp-local", "--stdio"},
	}
}

func attachGatewayRefForClient(client string) UpstreamServerRef {
	if client == "codex" {
		return codexStdioGatewayRef()
	}
	return gatewayRef()
}

// adapterFor returns a fresh adapter for the named client pointing at a
// freshly-isolated config path under t.TempDir(). Tests reuse this so
// each scenario gets an unshared filesystem slot.
func adapterFor(t *testing.T, client string) AttachAdapter {
	t.Helper()
	dir := t.TempDir()
	switch client {
	case "claude_code":
		return newClaudeCodeAdapter(filepath.Join(dir, ".claude.json"))
	case "codex":
		return newCodexAdapter(filepath.Join(dir, ".codex", "config.toml"))
	case "cursor":
		return newCursorAdapter(filepath.Join(dir, ".cursor", "mcp.json"))
	}
	t.Fatalf("unknown client %q", client)
	return nil
}

// allClients lists the 3 supported attach targets. Tests t.Run across
// this slice so a per-client behavior regression surfaces with the
// client name in the failure message.
func allClients() []string { return []string{"claude_code", "codex", "cursor"} }

// fixtureExistingValid returns a valid pre-existing config payload for
// the given client containing one unrelated MCP server (used to assert
// the merge preserves the prior entry alongside cordum-gateway).
func fixtureExistingValid(client string) []byte {
	switch client {
	case "claude_code":
		return []byte(`{
  "mcpServers": {
    "other-server": {
      "type": "http",
      "url": "https://other.example.com/mcp"
    }
  }
}
`)
	case "codex":
		return []byte(`[mcp_servers.other_server]
command = "npx"
args = ["-y", "other-server"]
`)
	case "cursor":
		return []byte(`{
  "mcpServers": {
    "other-server": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "other-server"]
    }
  }
}
`)
	}
	return nil
}

// fixtureMalformed returns a syntactically-broken payload for the given
// client. Preview must report a parse error and refuse to merge; apply
// must abort before writing so the broken file is left untouched.
func fixtureMalformed(client string) []byte {
	switch client {
	case "claude_code", "cursor":
		return []byte(`{"mcpServers": { broken json `)
	case "codex":
		return []byte(`[mcp_servers.broken
command = "this never closes`)
	}
	return nil
}

// fixtureWithSecret returns a config payload containing an OpenAI-style
// `sk-`-prefixed secret in the env block. Preview output must redact
// the raw value so operators don't accidentally paste tokens into chat.
func fixtureWithSecret(client string) []byte {
	switch client {
	case "claude_code":
		return []byte(`{
  "mcpServers": {
    "other-server": {
      "command": "node",
      "args": ["server.js"],
      "env": {"OPENAI_API_KEY": "sk-leaked-12345"}
    }
  }
}
`)
	case "codex":
		return []byte(`[mcp_servers.other_server]
command = "node"
args = ["server.js"]
env = { OPENAI_API_KEY = "sk-leaked-12345" }
`)
	case "cursor":
		return []byte(`{
  "mcpServers": {
    "other-server": {
      "type": "stdio",
      "command": "node",
      "env": {"OPENAI_API_KEY": "sk-leaked-12345"}
    }
  }
}
`)
	}
	return nil
}

// writeFixture seeds the adapter's config path with the given payload
// and creates any parent dirs. Tests use this to set up the "existing
// config" branch before calling PreviewAttach or ApplyAttach.
func writeFixture(t *testing.T, adapter AttachAdapter, payload []byte) {
	t.Helper()
	path := adapter.ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func assertCodexUnsupported(t *testing.T, out string, transport string) {
	t.Helper()
	want := []string{
		"codex: transport " + transport + " unsupported",
		"stdio-only",
		"cordumctl mcp proxy not implemented",
	}
	for _, fragment := range want {
		if !strings.Contains(out, fragment) {
			t.Fatalf("unsupported output missing %q:\n%s", fragment, out)
		}
	}
	if strings.Contains(out, `args = ["mcp", "proxy"`) {
		t.Fatalf("unsupported output must not render proxy args:\n%s", out)
	}
}

func assertNoCodexProxyConfig(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	rendered := string(data)
	if strings.Contains(rendered, `args = ["mcp", "proxy"`) ||
		strings.Contains(rendered, `command = "cordumctl"`) {
		t.Fatalf("config rendered unimplemented proxy command:\n%s", rendered)
	}
}

func assertNoBackups(t *testing.T, path string) {
	t.Helper()
	backups, err := filepath.Glob(path + ".bak.*")
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(backups) != 0 {
		t.Fatalf("backup created on reject: %v", backups)
	}
}

func assertCodexConfigUnchanged(t *testing.T, path string, before []byte) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config after reject: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("config changed on reject\nafter:\n%s\nbefore:\n%s", after, before)
	}
}

func assertCodexPreviewRejects(t *testing.T, transport string, existing bool) {
	t.Helper()
	adapter := adapterFor(t, "codex")
	var before []byte
	if existing {
		before = fixtureExistingValid("codex")
		writeFixture(t, adapter, before)
	}

	var buf strings.Builder
	code := PreviewAttach(adapter, codexGatewayRef(transport), &buf)
	if code != 2 {
		t.Fatalf("exit=%d want 2\nout=%s", code, buf.String())
	}
	assertCodexUnsupported(t, buf.String(), transport)
	if existing {
		assertCodexConfigUnchanged(t, adapter.ConfigPath(), before)
	} else if _, err := os.Stat(adapter.ConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("preview reject must not create config; stat err=%v", err)
	}
	assertNoBackups(t, adapter.ConfigPath())
}

func assertCodexApplyRejects(t *testing.T, transport string, existing bool) {
	t.Helper()
	adapter := adapterFor(t, "codex")
	var before []byte
	if existing {
		before = fixtureExistingValid("codex")
		writeFixture(t, adapter, before)
	}

	var buf strings.Builder
	code := ApplyAttach(adapter, codexGatewayRef(transport), &buf)
	if code != 2 {
		t.Fatalf("exit=%d want 2\nout=%s", code, buf.String())
	}
	assertCodexUnsupported(t, buf.String(), transport)
	if existing {
		assertCodexConfigUnchanged(t, adapter.ConfigPath(), before)
		assertNoCodexProxyConfig(t, adapter.ConfigPath())
	} else if _, err := os.Stat(adapter.ConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("apply reject must not create config; stat err=%v", err)
	}
	assertNoBackups(t, adapter.ConfigPath())
}

func TestCodexAttachRejectsHTTPAndSSETransports(t *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		t.Run(transport+"_preview_missing", func(t *testing.T) {
			assertCodexPreviewRejects(t, transport, false)
		})
		t.Run(transport+"_preview_existing", func(t *testing.T) {
			assertCodexPreviewRejects(t, transport, true)
		})
		t.Run(transport+"_apply_missing", func(t *testing.T) {
			assertCodexApplyRejects(t, transport, false)
		})
		t.Run(transport+"_apply_existing", func(t *testing.T) {
			assertCodexApplyRejects(t, transport, true)
		})
	}
}

func TestCodexAttachDispatchRejectsDefaultHTTPProxyCommand(t *testing.T) {
	adapter := adapterFor(t, "codex")

	var stdout, stderr strings.Builder
	code := runMCPAttachCmd([]string{
		"attach",
		"--config-path", adapter.ConfigPath(),
		"--client", "codex",
		"--apply",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}
	assertCodexUnsupported(t, stdout.String()+stderr.String(), "http")
	if _, err := os.Stat(adapter.ConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("dispatch reject must not create config; stat err=%v", err)
	}
	assertNoBackups(t, adapter.ConfigPath())
}

func TestCodexAttachStdioLocalSuccessDoesNotUseProxy(t *testing.T) {
	adapter := adapterFor(t, "codex")
	original := fixtureExistingValid("codex")
	writeFixture(t, adapter, original)

	var preview strings.Builder
	if code := PreviewAttach(adapter, codexStdioGatewayRef(), &preview); code != 0 {
		t.Fatalf("preview exit=%d want 0\nout=%s", code, preview.String())
	}
	if !strings.Contains(preview.String(), "cordum-gateway") ||
		!strings.Contains(preview.String(), "other_server") {
		t.Fatalf("preview missing expected merge summary:\n%s", preview.String())
	}
	assertCodexConfigUnchanged(t, adapter.ConfigPath(), original)

	var apply strings.Builder
	if code := ApplyAttach(adapter, codexStdioGatewayRef(), &apply); code != 0 {
		t.Fatalf("apply exit=%d want 0\nout=%s", code, apply.String())
	}
	renderedBytes, err := os.ReadFile(adapter.ConfigPath())
	if err != nil {
		t.Fatalf("read rendered codex config: %v", err)
	}
	rendered := string(renderedBytes)
	for _, fragment := range []string{
		`[mcp_servers.other_server]`,
		`[mcp_servers.cordum-gateway]`,
		`command = "cordum-mcp-local"`,
		`args = ["--stdio"]`,
	} {
		if !strings.Contains(rendered, fragment) {
			t.Fatalf("rendered config missing %q:\n%s", fragment, rendered)
		}
	}
	if strings.Contains(rendered, "mcp proxy") ||
		strings.Contains(rendered, canonicalMCPGatewayEndpoint) ||
		strings.Contains(rendered, `command = "cordumctl"`) {
		t.Fatalf("stdio codex config must not use proxy/http command:\n%s", rendered)
	}
}

// (A) Preview against a missing config reports the create path and
// exits 0 without writing anything.
func TestAttachPreviewMissingConfig(t *testing.T) {
	for _, client := range allClients() {
		t.Run(client, func(t *testing.T) {
			adapter := adapterFor(t, client)
			var buf strings.Builder
			code := PreviewAttach(adapter, attachGatewayRefForClient(client), &buf)
			if code != 0 {
				t.Fatalf("exit=%d want 0\nout=%s", code, buf.String())
			}
			out := buf.String()
			if !strings.Contains(out, "no existing config") {
				t.Fatalf("missing 'no existing config' signal:\n%s", out)
			}
			if _, err := os.Stat(adapter.ConfigPath()); !os.IsNotExist(err) {
				t.Fatalf("preview must not create file; stat err=%v", err)
			}
		})
	}
}

// (B) Preview against a valid existing config reports the planned merge
// (existing servers + cordum-gateway) and writes nothing.
func TestAttachPreviewExistingConfig(t *testing.T) {
	for _, client := range allClients() {
		t.Run(client, func(t *testing.T) {
			adapter := adapterFor(t, client)
			writeFixture(t, adapter, fixtureExistingValid(client))
			before, _ := os.ReadFile(adapter.ConfigPath())

			var buf strings.Builder
			code := PreviewAttach(adapter, attachGatewayRefForClient(client), &buf)
			if code != 0 {
				t.Fatalf("exit=%d want 0\nout=%s", code, buf.String())
			}
			out := buf.String()
			if !strings.Contains(out, "cordum-gateway") {
				t.Fatalf("preview missing cordum-gateway mention:\n%s", out)
			}
			if !strings.Contains(out, "other") {
				t.Fatalf("preview must mention preserved 'other-server':\n%s", out)
			}
			after, _ := os.ReadFile(adapter.ConfigPath())
			if string(before) != string(after) {
				t.Fatalf("preview must not modify file")
			}
		})
	}
}

// (C) Preview against a malformed config exits 2 and writes nothing.
// The error message must surface the parse failure so operators can fix
// their config rather than guess.
func TestAttachPreviewMalformedConfig(t *testing.T) {
	for _, client := range allClients() {
		t.Run(client, func(t *testing.T) {
			adapter := adapterFor(t, client)
			writeFixture(t, adapter, fixtureMalformed(client))
			before, _ := os.ReadFile(adapter.ConfigPath())

			var buf strings.Builder
			code := PreviewAttach(adapter, attachGatewayRefForClient(client), &buf)
			if code != 2 {
				t.Fatalf("exit=%d want 2 (parse error)\nout=%s", code, buf.String())
			}
			if !strings.Contains(buf.String(), "parse") {
				t.Fatalf("preview output missing 'parse' signal:\n%s", buf.String())
			}
			after, _ := os.ReadFile(adapter.ConfigPath())
			if string(before) != string(after) {
				t.Fatalf("preview must not modify malformed file")
			}
		})
	}
}

// (D) Apply when no prior config exists creates the file with mode 0600
// and does NOT create a backup (nothing to back up).
func TestAttachApplyCreatesNew(t *testing.T) {
	for _, client := range allClients() {
		t.Run(client, func(t *testing.T) {
			adapter := adapterFor(t, client)
			var buf strings.Builder
			code := ApplyAttach(adapter, attachGatewayRefForClient(client), &buf)
			if code != 0 {
				t.Fatalf("exit=%d want 0\nout=%s", code, buf.String())
			}
			info, err := os.Stat(adapter.ConfigPath())
			if err != nil {
				t.Fatalf("config file not created: %v", err)
			}
			// Skip mode check on Windows: NTFS doesn't honor Unix bits.
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Fatalf("file mode = %o, want 0600", info.Mode().Perm())
			}
			data, _ := os.ReadFile(adapter.ConfigPath())
			if !strings.Contains(string(data), "cordum-gateway") {
				t.Fatalf("written file missing cordum-gateway:\n%s", data)
			}
			backups, _ := filepath.Glob(adapter.ConfigPath() + ".bak.*")
			if len(backups) != 0 {
				t.Fatalf("backup created despite no prior file: %v", backups)
			}
		})
	}
}

// (E) Apply against an existing config creates a .bak.<unix_ms> snapshot
// identical to the original, then writes the merged content. Backup is
// idempotent evidence operators can audit / restore from.
func TestAttachApplyBacksUpExisting(t *testing.T) {
	for _, client := range allClients() {
		t.Run(client, func(t *testing.T) {
			adapter := adapterFor(t, client)
			original := fixtureExistingValid(client)
			writeFixture(t, adapter, original)

			var buf strings.Builder
			code := ApplyAttach(adapter, attachGatewayRefForClient(client), &buf)
			if code != 0 {
				t.Fatalf("exit=%d want 0\nout=%s", code, buf.String())
			}
			backups, _ := filepath.Glob(adapter.ConfigPath() + ".bak.*")
			if len(backups) != 1 {
				t.Fatalf("expected exactly 1 backup, got %d: %v", len(backups), backups)
			}
			snap, err := os.ReadFile(backups[0])
			if err != nil {
				t.Fatalf("read backup: %v", err)
			}
			if string(snap) != string(original) {
				t.Fatalf("backup != original\nbackup:\n%s\noriginal:\n%s", snap, original)
			}
			merged, _ := os.ReadFile(adapter.ConfigPath())
			if !strings.Contains(string(merged), "cordum-gateway") {
				t.Fatalf("merged config missing cordum-gateway:\n%s", merged)
			}
			if !strings.Contains(string(merged), "other") {
				t.Fatalf("merged config dropped other-server:\n%s", merged)
			}
		})
	}
}

// (F) Apply-then-rollback restores the original content from the
// newest backup. Asserts byte-identity to catch silent re-encoding.
func TestAttachRollbackRestores(t *testing.T) {
	for _, client := range allClients() {
		t.Run(client, func(t *testing.T) {
			adapter := adapterFor(t, client)
			original := fixtureExistingValid(client)
			writeFixture(t, adapter, original)

			var buf strings.Builder
			if code := ApplyAttach(adapter, attachGatewayRefForClient(client), &buf); code != 0 {
				t.Fatalf("apply exit=%d", code)
			}
			buf.Reset()
			if code := RollbackAttach(adapter, &buf); code != 0 {
				t.Fatalf("rollback exit=%d\nout=%s", code, buf.String())
			}
			restored, _ := os.ReadFile(adapter.ConfigPath())
			if string(restored) != string(original) {
				t.Fatalf("rollback restored != original\nrestored:\n%s\noriginal:\n%s", restored, original)
			}
		})
	}
}

// (G) Rollback with no backups present exits 2 with a clear error so
// operators don't silently overwrite their config with a no-op restore.
func TestAttachRollbackMissingBackupFails(t *testing.T) {
	for _, client := range allClients() {
		t.Run(client, func(t *testing.T) {
			adapter := adapterFor(t, client)
			var buf strings.Builder
			code := RollbackAttach(adapter, &buf)
			if code != 2 {
				t.Fatalf("exit=%d want 2 (no backup)\nout=%s", code, buf.String())
			}
			if !strings.Contains(buf.String(), "no backup") {
				t.Fatalf("rollback output missing 'no backup' signal:\n%s", buf.String())
			}
		})
	}
}

// (H) Preview must NEVER print raw `sk-*` secrets that exist in the
// target config. The redaction is a hard contract per task DoD #3.
func TestAttachNeverPrintsSecrets(t *testing.T) {
	for _, client := range allClients() {
		t.Run(client, func(t *testing.T) {
			adapter := adapterFor(t, client)
			writeFixture(t, adapter, fixtureWithSecret(client))

			var buf strings.Builder
			code := PreviewAttach(adapter, attachGatewayRefForClient(client), &buf)
			if code != 0 {
				t.Fatalf("exit=%d want 0\nout=%s", code, buf.String())
			}
			if strings.Contains(buf.String(), "sk-leaked-12345") {
				t.Fatalf("RAW SECRET leaked in preview output:\n%s", buf.String())
			}
		})
	}
}

// (I) Per-platform path resolution: each adapter's
// DefaultConfigPath(homeDir) helper must compose the correct cross-
// platform path so production callers using os.UserHomeDir end up at
// the documented location.
func TestAttachDefaultPathPerPlatform(t *testing.T) {
	cases := []struct {
		client string
		home   string
		want   string
	}{
		{"claude_code", "/home/x", filepath.Join("/home/x", ".claude.json")},
		{"claude_code", `C:\Users\X`, filepath.Join(`C:\Users\X`, ".claude.json")},
		{"codex", "/home/x", filepath.Join("/home/x", ".codex", "config.toml")},
		{"codex", `C:\Users\X`, filepath.Join(`C:\Users\X`, ".codex", "config.toml")},
		{"cursor", "/home/x", filepath.Join("/home/x", ".cursor", "mcp.json")},
		{"cursor", `C:\Users\X`, filepath.Join(`C:\Users\X`, ".cursor", "mcp.json")},
	}
	for _, tc := range cases {
		t.Run(tc.client+"_"+filepath.Base(tc.home), func(t *testing.T) {
			got := DefaultConfigPath(tc.client, tc.home)
			if got != tc.want {
				t.Fatalf("DefaultConfigPath(%q, %q) = %q; want %q",
					tc.client, tc.home, got, tc.want)
			}
		})
	}
}

// (J) Apply twice produces a SECOND backup snapshot. The cordum-gateway
// entry stays stable (idempotent merge), so consecutive applies are
// safe and inspectable in the .bak.* trail.
func TestAttachApplyIdempotent(t *testing.T) {
	for _, client := range allClients() {
		t.Run(client, func(t *testing.T) {
			adapter := adapterFor(t, client)
			writeFixture(t, adapter, fixtureExistingValid(client))

			var buf strings.Builder
			if code := ApplyAttach(adapter, attachGatewayRefForClient(client), &buf); code != 0 {
				t.Fatalf("apply#1 exit=%d", code)
			}
			firstMerged, _ := os.ReadFile(adapter.ConfigPath())

			// Sleep 2ms so the second backup gets a distinct unix_ms suffix
			// and Glob returns both entries.
			waitMs(2)

			buf.Reset()
			if code := ApplyAttach(adapter, attachGatewayRefForClient(client), &buf); code != 0 {
				t.Fatalf("apply#2 exit=%d", code)
			}
			backups, _ := filepath.Glob(adapter.ConfigPath() + ".bak.*")
			if len(backups) != 2 {
				t.Fatalf("expected 2 backups after 2 applies, got %d: %v", len(backups), backups)
			}
			secondMerged, _ := os.ReadFile(adapter.ConfigPath())
			if string(secondMerged) != string(firstMerged) {
				t.Fatalf("second apply changed merged content (non-idempotent):\nfirst:\n%s\nsecond:\n%s",
					firstMerged, secondMerged)
			}
		})
	}
}

// (K) ApplyAttach is the only function that writes; PreviewAttach must
// be a strict read. Asserts via dispatcher-level test that the CLI
// surface honors --apply gating.
func TestAttachDispatchRefusesWriteWithoutApplyFlag(t *testing.T) {
	for _, client := range allClients() {
		t.Run(client, func(t *testing.T) {
			adapter := adapterFor(t, client)
			writeFixture(t, adapter, fixtureExistingValid(client))
			before, _ := os.ReadFile(adapter.ConfigPath())

			var stdout, stderr strings.Builder
			// `attach <client>` without --apply must EITHER refuse with
			// exit code != 0 OR fall through to a preview-only path. Both
			// are acceptable; the contract is: no file write.
			code := runMCPAttachCmd([]string{"attach", "--config-path", adapter.ConfigPath(), "--client", client}, &stdout, &stderr)
			_ = code
			after, _ := os.ReadFile(adapter.ConfigPath())
			if string(before) != string(after) {
				t.Fatalf("attach without --apply modified file (forbidden per task rail #1):\n%s",
					stderr.String())
			}
		})
	}
}

func TestAttachDispatchDefaultEndpointMatchesGatewayRoute(t *testing.T) {
	for _, client := range []string{"claude_code", "cursor"} {
		t.Run(client, func(t *testing.T) {
			adapter := adapterFor(t, client)

			var stdout, stderr strings.Builder
			code := runMCPAttachCmd([]string{
				"attach",
				"--config-path", adapter.ConfigPath(),
				"--client", client,
				"--apply",
			}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("exit=%d want 0\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
			}

			data, err := os.ReadFile(adapter.ConfigPath())
			if err != nil {
				t.Fatalf("read rendered config: %v", err)
			}
			rendered := string(data)
			if !strings.Contains(rendered, canonicalMCPGatewayEndpoint) {
				t.Fatalf("default attach endpoint missing canonical gateway route %q:\n%s", canonicalMCPGatewayEndpoint, rendered)
			}
			if strings.Contains(rendered, legacyBareMCPGatewayEndpoint+`"`) {
				t.Fatalf("default attach endpoint rendered unregistered bare route %q:\n%s", legacyBareMCPGatewayEndpoint, rendered)
			}
		})
	}
}
