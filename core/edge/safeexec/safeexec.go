package safeexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Options struct {
	Env                    []string
	AllowEnv               []string
	AllowedPathPrefixes    []string
	AllowedArgPathPrefixes []string
	Dir                    string
	Stdin                  io.Reader
	Stdout                 io.Writer
	Stderr                 io.Writer
	MaxStdinBytes          int64
	MaxStdoutBytes         int64
	MaxStderrBytes         int64
}

func CommandContext(ctx context.Context, argv0 string, args []string, opts Options) (*exec.Cmd, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	name, err := NormalizeExecutablePath(argv0, opts.AllowedPathPrefixes)
	if err != nil {
		return nil, err
	}
	if err := validateArgs(args); err != nil {
		return nil, err
	}
	if err := ValidateArgPaths(args, opts.Dir, opts.AllowedArgPathPrefixes); err != nil {
		return nil, err
	}
	env, err := SanitizeEnv(opts.Env, opts.AllowEnv)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- hardened wrapper: NormalizeExecutablePath resolves name to an absolute path (bare names via LookPath, traversal rejected, optional allow-prefix gate) so exec does no implicit PATH lookup; args/arg-paths/env validated above
	cmd.Env = env
	if strings.TrimSpace(opts.Dir) != "" {
		dir, err := NormalizeDir(opts.Dir, nil)
		if err != nil {
			return nil, err
		}
		cmd.Dir = dir
	}
	cmd.Stdin = opts.Stdin
	if opts.Stdout != nil {
		cmd.Stdout = LimitWriter(opts.Stdout, opts.MaxStdoutBytes)
	}
	if opts.Stderr != nil {
		cmd.Stderr = LimitWriter(opts.Stderr, opts.MaxStderrBytes)
	}
	return cmd, nil
}

func NormalizeExecutablePath(argv0 string, allowedPrefixes []string) (string, error) {
	raw := strings.TrimSpace(argv0)
	if raw == "" {
		return "", errors.New("safeexec: argv0 required")
	}
	if strings.ContainsRune(raw, '\x00') {
		return "", errors.New("safeexec: argv0 contains NUL byte")
	}
	hasPath := filepath.IsAbs(raw) || strings.ContainsAny(raw, `/\`)
	if containsTraversal(raw) {
		return "", fmt.Errorf("safeexec: argv0 path traversal rejected: %s", raw)
	}
	candidate := raw
	if !hasPath {
		// A bare name (no path separator) would otherwise be resolved by
		// exec.Command against $PATH at spawn time, which an attacker who
		// controls PATH can influence. Resolve it explicitly now so the
		// caller always execs a fully-qualified path and the result goes
		// through the same allow-prefix gate as a path-qualified argv0.
		resolved, err := exec.LookPath(raw)
		if err != nil {
			return "", fmt.Errorf("safeexec: resolve argv0 %q: %w", raw, err)
		}
		candidate = resolved
	}
	abs, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", fmt.Errorf("safeexec: normalize argv0: %w", err)
	}
	abs = evalPathIfPossible(abs)
	if err := requireAllowedPrefix(abs, allowedPrefixes); err != nil {
		return "", err
	}
	return abs, nil
}

func SanitizeEnv(base []string, extraAllow []string) ([]string, error) {
	if base == nil {
		base = os.Environ()
	}
	prodLocked := envBool(base, "CORDUM_HOOK_PROD_LOCK")
	devAllow := ""
	if !prodLocked {
		devAllow = envValue(base, "CORDUM_DEV_ALLOW_ENV")
	}
	env := make(map[string]string, len(base))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		if err := validateEnvKV(key, value); err != nil {
			return nil, err
		}
		if prodLocked && strings.EqualFold(key, "PATH") {
			continue
		}
		if envKeyAllowed(key, extraAllow, devAllow) {
			env[key] = value
		}
	}
	return sortedEnv(env), nil
}

func validateArgs(args []string) error {
	for _, arg := range args {
		if strings.ContainsRune(arg, '\x00') {
			return errors.New("safeexec: argv contains NUL byte")
		}
	}
	return nil
}

func NormalizeDir(dir string, allowedPrefixes []string) (string, error) {
	if strings.ContainsRune(dir, '\x00') {
		return "", errors.New("safeexec: dir contains NUL byte")
	}
	if containsTraversal(dir) {
		return "", fmt.Errorf("safeexec: dir path traversal rejected: %s", dir)
	}
	abs, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", fmt.Errorf("safeexec: normalize dir: %w", err)
	}
	abs = evalPathIfPossible(abs)
	if err := requireAllowedPrefix(abs, allowedPrefixes); err != nil {
		return "", err
	}
	return abs, nil
}

func containsTraversal(path string) bool {
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	for _, part := range parts {
		if part == ".." {
			return true
		}
	}
	return false
}

func requireAllowedPrefix(path string, prefixes []string) error {
	if len(prefixes) == 0 {
		return nil
	}
	for _, prefix := range prefixes {
		normalized, err := filepath.Abs(filepath.Clean(prefix))
		if err != nil {
			return fmt.Errorf("safeexec: normalize allowed prefix: %w", err)
		}
		normalized = evalPathIfPossible(normalized)
		if pathWithin(path, normalized) {
			return nil
		}
	}
	return fmt.Errorf("safeexec: argv0 outside allowed prefixes: %s", path)
}

func pathWithin(path, prefix string) bool {
	rel, err := filepath.Rel(prefix, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func evalPathIfPossible(path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err == nil {
		return real
	}
	return path
}

func validateEnvKV(key, value string) error {
	if strings.ContainsRune(key, '\x00') || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("safeexec: env %s contains NUL byte", redactKey(key))
	}
	return nil
}

func envKeyAllowed(key string, extraAllow []string, devAllow string) bool {
	if deniedEnvKey(key) {
		return false
	}
	if defaultEnvKeyAllowed(key) || listedEnvKeyAllowed(key, extraAllow) {
		return true
	}
	return listedEnvKeyAllowed(key, splitEnvAllow(devAllow))
}

func deniedEnvKey(key string) bool {
	trimmed := strings.TrimSpace(key)
	upper := strings.ToUpper(trimmed)
	return strings.HasPrefix(trimmed, "_") ||
		upper == "LD_PRELOAD" ||
		upper == "NODE_OPTIONS" ||
		upper == "BASH_ENV" ||
		strings.HasPrefix(upper, "DYLD_")
}

func defaultEnvKeyAllowed(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	switch upper {
	case "PATH", "HOME", "USERPROFILE", "TEMP", "TMP", "TMPDIR", "LANG", "TZ",
		"SYSTEMROOT", "WINDIR", "PATHEXT":
		return true
	default:
		return strings.HasPrefix(upper, "CORDUM_") || strings.HasPrefix(upper, "LC_")
	}
}

func listedEnvKeyAllowed(key string, allowed []string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	for _, raw := range allowed {
		pattern := strings.ToUpper(strings.TrimSpace(raw))
		if pattern == "*" || pattern == upper {
			return true
		}
		if strings.HasSuffix(pattern, "*") && strings.HasPrefix(upper, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

func splitEnvAllow(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
}

func envBool(env []string, key string) bool {
	value := strings.ToLower(strings.TrimSpace(envValue(env, key)))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func envValue(env []string, key string) string {
	for _, entry := range env {
		got, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(got, key) {
			return value
		}
	}
	return ""
}

func sortedEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func redactKey(key string) string {
	if strings.TrimSpace(key) == "" {
		return "[empty]"
	}
	return key
}
