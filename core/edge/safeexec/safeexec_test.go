package safeexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCommandContextTreatsShellMetacharactersAsLiteralArgs(t *testing.T) {
	ctx := context.Background()
	args := helperArgs("echoargs", "literal; rm -rf /", "$(id)", "`whoami`")
	cmd, err := CommandContext(ctx, os.Args[0], args, Options{
		Env: []string{"CORDUM_SAFEEXEC_HELPER=echoargs"},
	})
	if err != nil {
		t.Fatalf("CommandContext returned error: %v", err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper output failed: %v", err)
	}
	got := string(out)
	for _, want := range []string{"literal; rm -rf /", "$(id)", "`whoami`"} {
		if !strings.Contains(got, want) {
			t.Fatalf("helper output %q missing literal arg %q", got, want)
		}
	}
}

func TestSanitizeEnvStripsDangerousAndUnknownKeys(t *testing.T) {
	env, err := SanitizeEnv([]string{
		"PATH=/safe/bin",
		"CORDUM_ALLOWED=yes",
		"LANG=C",
		"LD_PRELOAD=/tmp/evil.so",
		"DYLD_INSERT_LIBRARIES=/tmp/evil.dylib",
		"NODE_OPTIONS=--require=/tmp/evil.js",
		"BASH_ENV=/tmp/evil.sh",
		"_=/tmp/hidden",
		"UNRELATED_SECRET=secret",
	}, nil)
	if err != nil {
		t.Fatalf("SanitizeEnv returned error: %v", err)
	}
	got := envMap(env)
	for _, key := range []string{"PATH", "CORDUM_ALLOWED", "LANG"} {
		if got[key] == "" {
			t.Fatalf("sanitized env missing %s: %#v", key, got)
		}
	}
	for _, key := range []string{"LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "NODE_OPTIONS", "BASH_ENV", "_", "UNRELATED_SECRET"} {
		if _, ok := got[key]; ok {
			t.Fatalf("sanitized env kept %s: %#v", key, got)
		}
	}
}

func TestSanitizeEnvExtraAllowlistPreservesNamedVars(t *testing.T) {
	env, err := SanitizeEnv([]string{"SAFE_TOKEN=ok", "OTHER=no"}, []string{"SAFE_TOKEN"})
	if err != nil {
		t.Fatalf("SanitizeEnv returned error: %v", err)
	}
	got := envMap(env)
	if got["SAFE_TOKEN"] != "ok" {
		t.Fatalf("SAFE_TOKEN = %q, want ok in %#v", got["SAFE_TOKEN"], got)
	}
	if _, ok := got["OTHER"]; ok {
		t.Fatalf("OTHER should be stripped: %#v", got)
	}
}

func TestNormalizeExecutablePathMakesRelativePathAbsolute(t *testing.T) {
	name := "." + string(filepath.Separator) + "cordum-safeexec-helper"
	got, err := NormalizeExecutablePath(name, nil)
	if err != nil {
		t.Fatalf("NormalizeExecutablePath returned error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("normalized path %q is not absolute", got)
	}
	if strings.Contains(got, "."+string(filepath.Separator)) {
		t.Fatalf("normalized path was not cleaned: %q", got)
	}
}

func TestNormalizeExecutablePathRejectsTraversal(t *testing.T) {
	_, err := NormalizeExecutablePath(".."+string(filepath.Separator)+"evil", nil)
	if err == nil {
		t.Fatalf("NormalizeExecutablePath accepted traversal")
	}
	if !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("error %q missing traversal", err)
	}
}

func TestNormalizeExecutablePathRejectsAbsoluteTraversal(t *testing.T) {
	tmp := t.TempDir()
	argv0 := tmp + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(tmp) + string(filepath.Separator) + "tool"
	_, err := NormalizeExecutablePath(argv0, nil)
	if err == nil {
		t.Fatalf("NormalizeExecutablePath accepted absolute traversal")
	}
}

func TestValidateArgPathsRejectsTraversalAndOutsidePrefix(t *testing.T) {
	base := t.TempDir()
	if err := ValidateArgPaths([]string{"--settings", filepath.Join(base, "settings.json")}, base, []string{base}); err != nil {
		t.Fatalf("ValidateArgPaths rejected allowed settings path: %v", err)
	}
	if err := ValidateArgPaths([]string{"--settings=..\\evil.json"}, base, []string{base}); err == nil {
		t.Fatalf("ValidateArgPaths accepted traversal")
	}
	outside := filepath.Join(filepath.Dir(base), "outside.json")
	if err := ValidateArgPaths([]string{outside}, base, []string{base}); err == nil {
		t.Fatalf("ValidateArgPaths accepted path outside prefix")
	}
}

func TestCommandContextPropagatesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cmd, err := CommandContext(ctx, os.Args[0], helperArgs("sleep"), Options{
		Env: []string{"CORDUM_SAFEEXEC_HELPER=sleep"},
	})
	if err != nil {
		t.Fatalf("CommandContext returned error: %v", err)
	}
	started := time.Now()
	if err := cmd.Run(); err == nil {
		t.Fatalf("sleep helper unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("context cancellation took %s", elapsed)
	}
}

func TestSanitizeEnvRejectsNULBytes(t *testing.T) {
	_, err := SanitizeEnv([]string{"CORDUM_BAD=value\x00tail"}, nil)
	if err == nil {
		t.Fatalf("SanitizeEnv accepted NUL byte")
	}
}

func TestRunCaptureRejectsOversizeStdin(t *testing.T) {
	_, err := RunCapture(context.Background(), os.Args[0], helperArgs("readstdin"), strings.NewReader("123456"), Options{
		Env:           []string{"CORDUM_SAFEEXEC_HELPER=readstdin"},
		MaxStdinBytes: 4,
	})
	if !errors.Is(err, ErrIOLimitExceeded) {
		t.Fatalf("RunCapture error=%v, want ErrIOLimitExceeded", err)
	}
}

func TestRunCaptureRejectsOversizeStdout(t *testing.T) {
	_, err := RunCapture(context.Background(), os.Args[0], helperArgs("stdout"), nil, Options{
		Env:            []string{"CORDUM_SAFEEXEC_HELPER=stdout", "CORDUM_SAFEEXEC_WRITE_BYTES=32"},
		MaxStdoutBytes: 8,
	})
	if !errors.Is(err, ErrIOLimitExceeded) {
		t.Fatalf("RunCapture error=%v, want ErrIOLimitExceeded", err)
	}
}

func TestRunCaptureRejectsOversizeStderr(t *testing.T) {
	_, err := RunCapture(context.Background(), os.Args[0], helperArgs("stderr"), nil, Options{
		Env:            []string{"CORDUM_SAFEEXEC_HELPER=stderr", "CORDUM_SAFEEXEC_WRITE_BYTES=32"},
		MaxStderrBytes: 8,
	})
	if !errors.Is(err, ErrIOLimitExceeded) {
		t.Fatalf("RunCapture error=%v, want ErrIOLimitExceeded", err)
	}
}

func TestSafeExecHelperProcess(t *testing.T) {
	mode := os.Getenv("CORDUM_SAFEEXEC_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "echoargs":
		_, _ = os.Stdout.WriteString(strings.Join(os.Args, "\n"))
	case "sleep":
		time.Sleep(5 * time.Second)
	case "readstdin":
		data, _ := os.ReadFile(os.DevNull)
		_ = data
		_, _ = os.Stdout.WriteString("read")
	case "stdout":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", helperWriteBytes()))
	case "stderr":
		_, _ = os.Stderr.WriteString(strings.Repeat("x", helperWriteBytes()))
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func helperArgs(mode string, extra ...string) []string {
	args := []string{"-test.run=TestSafeExecHelperProcess", "--"}
	args = append(args, extra...)
	_ = mode
	return args
}

func helperWriteBytes() int {
	n, err := strconv.Atoi(os.Getenv("CORDUM_SAFEEXEC_WRITE_BYTES"))
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

func TestNormalizeExecutablePathRejectsOutsideAllowedPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("filepath.Rel across Windows volumes is environment-specific")
	}
	tmp := t.TempDir()
	outside := filepath.Join(filepath.Dir(tmp), "outside-tool")
	_, err := NormalizeExecutablePath(outside, []string{filepath.Join(tmp, "allowed")})
	if err == nil {
		t.Fatalf("NormalizeExecutablePath accepted path outside allowed prefix")
	}
}

func TestNormalizeExecutablePathRejectsSymlinkToShellOutsideAllowedPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege varies on Windows")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh unavailable")
	}
	allowed := t.TempDir()
	link := filepath.Join(allowed, "safe-looking")
	if err := os.Symlink("/bin/sh", link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := NormalizeExecutablePath(link, []string{allowed}); err == nil {
		t.Fatalf("NormalizeExecutablePath accepted symlink escaping allowed prefix")
	}
}
