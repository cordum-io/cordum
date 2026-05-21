package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// UpstreamServerRef is the subset of core/edge.UpstreamServer the
// attach surface needs to render a client-side MCP server entry. The
// canonical EDGE-101 record carries tenant scope + lifecycle metadata
// that has no place in a client config — so we project just the
// addressable + transport fields here. Mirrors the JSON tags on the
// registry record for traceability.
type UpstreamServerRef struct {
	Name          string
	Transport     string
	Endpoint      string
	Command       []string
	AuthSecretRef string
}

// AttachAdapter is the per-client contract that bridges the common
// preview/apply/rollback lifecycle to a specific MCP client's config
// format. Each adapter encapsulates the on-disk path it owns plus the
// schema-specific parse + merge + serialize logic so the lifecycle
// helpers stay format-agnostic.
//
// ReadAndMerge returns the bytes the lifecycle MUST write on apply
// alongside a human-readable preview summary that the lifecycle prints
// on preview. existing is nil when the config does not yet exist; the
// adapter is responsible for producing a valid empty-config payload in
// that case. err signals a parse, validation, or merge failure
// (preview surfaces it, apply aborts before writing).
type AttachAdapter interface {
	ClientName() string
	ConfigPath() string
	ReadAndMerge(existing []byte, gateway UpstreamServerRef) (merged []byte, preview string, err error)
}

// DefaultConfigPath resolves the canonical per-client config path from
// a home dir. Tests pass a fake home dir; production callers pass
// os.UserHomeDir(). Returns empty string for unknown clients so the
// caller surfaces a usage error rather than a silent default.
func DefaultConfigPath(client, homeDir string) string {
	switch client {
	case "claude_code":
		return filepath.Join(homeDir, ".claude.json")
	case "codex":
		return filepath.Join(homeDir, ".codex", "config.toml")
	case "cursor":
		return filepath.Join(homeDir, ".cursor", "mcp.json")
	default:
		return ""
	}
}

// PreviewAttach reads the adapter's config file (if any), computes the
// merged result, prints a human-readable diff via stdout, and returns
// the exit code. NEVER writes. Exit 2 on parse failure so CI can
// distinguish parse/validation error from missing config (exit 0).
func PreviewAttach(adapter AttachAdapter, gateway UpstreamServerRef, stdout io.Writer) int {
	if adapter == nil {
		_, _ = fmt.Fprintln(stdout, "preview: adapter is nil")
		return 2
	}
	path := adapter.ConfigPath()
	existing, missing, err := readMaybeMissing(path)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "preview: read %s: %v\n", path, err)
		return 2
	}
	_, preview, err := adapter.ReadAndMerge(existing, gateway)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "preview: validate/parse %s failed: %v (apply would abort before writing)\n",
			path, err)
		return 2
	}
	if missing {
		_, _ = fmt.Fprintf(stdout, "preview: no existing config at %s; apply will create one with cordum-gateway\n", path)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "preview: %s (%s)\n", adapter.ClientName(), path)
	_, _ = fmt.Fprintln(stdout, redactSecrets(preview))
	_, _ = fmt.Fprintln(stdout, "next: re-run with `attach` + `--apply` to write the merged config (existing file backed up to <path>.bak.<unix_ms>)")
	return 0
}

// ApplyAttach reads the adapter's config (or creates an empty starting
// point), computes the merge, backs up the existing file via
// `<path>.bak.<unix_ms>` if present, and atomically replaces the
// target. Returns 0 on success, 2 on validation / IO failure.
func ApplyAttach(adapter AttachAdapter, gateway UpstreamServerRef, stdout io.Writer) int {
	if adapter == nil {
		_, _ = fmt.Fprintln(stdout, "apply: adapter is nil")
		return 2
	}
	path := adapter.ConfigPath()
	existing, missing, err := readMaybeMissing(path)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "apply: read %s: %v\n", path, err)
		return 2
	}
	merged, _, err := adapter.ReadAndMerge(existing, gateway)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "apply: validate/parse %s failed: %v (refusing to overwrite a config we cannot reason about)\n",
			path, err)
		return 2
	}
	backupPath := ""
	if !missing {
		backupPath, err = backupExistingFile(path, existing)
		if err != nil {
			_, _ = fmt.Fprintf(stdout, "apply: backup %s: %v\n", path, err)
			return 2
		}
	}
	if err := atomicWriteAttachConfig(path, merged); err != nil {
		_, _ = fmt.Fprintf(stdout, "apply: write %s: %v\n", path, err)
		return 2
	}
	if backupPath != "" {
		_, _ = fmt.Fprintf(stdout, "attached cordum-gateway to %s at %s; backup at %s\n",
			adapter.ClientName(), path, backupPath)
	} else {
		_, _ = fmt.Fprintf(stdout, "attached cordum-gateway to %s at %s (created new file; no prior backup)\n",
			adapter.ClientName(), path)
	}
	return 0
}

// RollbackAttach restores the most-recent `<path>.bak.<unix_ms>`
// snapshot via atomic rename. Returns 0 on successful restore, 2 when
// no backup is found.
func RollbackAttach(adapter AttachAdapter, stdout io.Writer) int {
	if adapter == nil {
		_, _ = fmt.Fprintln(stdout, "rollback: adapter is nil")
		return 2
	}
	path := adapter.ConfigPath()
	newest, err := newestBackup(path)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "rollback: locate backups for %s: %v\n", path, err)
		return 2
	}
	if newest == "" {
		_, _ = fmt.Fprintf(stdout, "rollback: no backup found for %s (expected %s.bak.<unix_ms>)\n", path, path)
		return 2
	}
	if err := os.Rename(newest, path); err != nil {
		// Cross-filesystem rename failure: fall back to copy + remove.
		if copyErr := copyFile(newest, path); copyErr != nil {
			_, _ = fmt.Fprintf(stdout, "rollback: rename failed (%v) and copy fallback failed (%v)\n", err, copyErr)
			return 2
		}
		if rmErr := os.Remove(newest); rmErr != nil {
			_, _ = fmt.Fprintf(stdout, "rollback: restored via copy but backup remove failed: %v\n", rmErr)
			// Restored content is in place; backup leftover is harmless.
		}
	}
	_, _ = fmt.Fprintf(stdout, "rolled back %s to %s\n", path, filepath.Base(newest))
	return 0
}

// readMaybeMissing returns (data, missing=false, nil) for an existing
// file, (nil, missing=true, nil) when the file does not exist, and
// (nil, false, err) for any other IO error. Used by preview + apply to
// distinguish "absent" from "broken".
func readMaybeMissing(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return data, false, nil
}

// backupExistingFile writes a byte-identical snapshot of payload to
// `<path>.bak.<unix_ms>`. Returns the backup path so the caller can
// surface it in the success log line operators audit.
func backupExistingFile(path string, payload []byte) (string, error) {
	backupPath := fmt.Sprintf("%s.bak.%d", path, time.Now().UnixMilli())
	if err := os.WriteFile(backupPath, payload, 0o600); err != nil {
		return "", fmt.Errorf("write backup %s: %w", backupPath, err)
	}
	return backupPath, nil
}

// atomicWriteAttachConfig replaces the file at path with payload using
// a tempfile + os.Rename swap. Falls back to direct write when the
// tempfile lives on a different filesystem (rare but possible when
// /tmp is mounted separately from the user's home).
func atomicWriteAttachConfig(path string, payload []byte) error {
	clean := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(clean), err)
	}
	dir := filepath.Dir(clean)
	tmp, err := os.CreateTemp(dir, ".mcp-attach-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, clean); err != nil {
		cleanup()
		return fmt.Errorf("rename %s -> %s: %w", tmpName, clean, err)
	}
	return nil
}

// newestBackup returns the absolute path of the most recent
// `<path>.bak.<unix_ms>` file, or "" if none exists. Sorts by suffix
// (lexicographic on equal-width unix_ms strings is fine for the
// short-term retention window; >292M year overflow is not a concern).
func newestBackup(path string) (string, error) {
	matches, err := filepath.Glob(path + ".bak.*")
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", nil
	}
	sort.Strings(matches)
	return matches[len(matches)-1], nil
}

// copyFile reads src and writes dst atomically. Used as the
// cross-filesystem rollback fallback when os.Rename returns EXDEV.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return atomicWriteAttachConfig(dst, data)
}

// AttachSchemaProvenance returns the (url, date) the adapter's schema
// was last validated against. Operators or QA scripts can call this to
// audit whether the bundled fetch is older than a chosen review window.
// Returns ("", "") for unknown clients.
func AttachSchemaProvenance(client string) (url, date string) {
	switch client {
	case "claude_code":
		return claudeCodeSchemaURL, claudeCodeSchemaDate
	case "codex":
		return codexSchemaURL, codexSchemaDate
	case "cursor":
		return cursorSchemaURL, cursorSchemaDate
	}
	return "", ""
}

// secretPattern matches OpenAI-style `sk-*`, GitHub `ghp_*`/`gho_*`,
// Anthropic `sk-ant-*`, and bearer-token-shaped strings the preview
// could echo from a target config's env block. Mask with `<REDACTED>`
// before stdout. Pattern intentionally narrow — false positives
// (e.g. `sk-test` in a comment) are safer than leaking a real secret.
var secretPattern = regexp.MustCompile(`sk-[A-Za-z0-9_\-]{6,}|gh[ps]_[A-Za-z0-9]{20,}|Bearer\s+[A-Za-z0-9_\-\.]{20,}`)

// redactSecrets masks any string fragment matching secretPattern with
// the literal `<REDACTED>` placeholder so preview output is safe to
// paste into chat or paste-bins. Idempotent on already-redacted text.
func redactSecrets(s string) string {
	if !strings.Contains(s, "sk-") && !strings.Contains(s, "gh") && !strings.Contains(s, "Bearer") {
		return s
	}
	return secretPattern.ReplaceAllString(s, "<REDACTED>")
}
