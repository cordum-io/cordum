package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeManifestTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func expectedManifest(t *testing.T, paths ...string) string {
	t.Helper()
	entries := make([]string, 0, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		sum := sha256.Sum256(body)
		entries = append(entries, fmt.Sprintf(
			"%s  %s\n",
			hex.EncodeToString(sum[:]),
			filepath.ToSlash(path),
		))
	}
	sort.Strings(entries)
	return strings.Join(entries, "")
}

type errReader struct {
	err error
}

func (r errReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

type errWriter struct {
	err error
}

func (w errWriter) Write(_ []byte) (int, error) {
	return 0, w.err
}

func TestRunArgvInputWritesSortedManifest(t *testing.T) {
	dir := t.TempDir()
	b := writeManifestTestFile(t, dir, "b.bin", "bravo")
	a := writeManifestTestFile(t, dir, "a.bin", "alpha")
	var out bytes.Buffer

	err := run([]string{b, a}, errReader{err: errors.New("stdin read")}, &out)
	if err != nil {
		t.Fatalf("run argv: %v", err)
	}
	if got, want := out.String(), expectedManifest(t, a, b); got != want {
		t.Fatalf("manifest mismatch\ngot:\n%swant:\n%s", got, want)
	}
}

func TestRunStdinInputWritesManifestAndIgnoresBlankLines(t *testing.T) {
	dir := t.TempDir()
	b := writeManifestTestFile(t, dir, "b.bin", "bravo")
	a := writeManifestTestFile(t, dir, "a.bin", "alpha")
	in := strings.NewReader("\n" + b + "\n\n" + a + "\n")
	var out bytes.Buffer

	err := run(nil, in, &out)
	if err != nil {
		t.Fatalf("run stdin: %v", err)
	}
	if got, want := out.String(), expectedManifest(t, a, b); got != want {
		t.Fatalf("manifest mismatch\ngot:\n%swant:\n%s", got, want)
	}
}

func TestRunNoInputErrors(t *testing.T) {
	var out bytes.Buffer
	err := run(nil, strings.NewReader("\n\n"), &out)
	if err == nil {
		t.Fatal("expected no-input error")
	}
	if !strings.Contains(err.Error(), "no input paths") {
		t.Fatalf("err = %v, want no input paths", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
}

func TestRunBuildManifestErrorPropagates(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.bin")
	var out bytes.Buffer
	err := run([]string{missing}, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("expected missing-file error")
	}
	if !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "sign: open") {
		t.Fatalf("err = %v, want sign open error containing %q", err, missing)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
}

func TestRunStdinReadErrorWraps(t *testing.T) {
	boom := errors.New("stdin boom")
	var out bytes.Buffer
	err := run(nil, errReader{err: boom}, &out)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped stdin error", err)
	}
	if !strings.Contains(err.Error(), "manifest-cli: read stdin") {
		t.Fatalf("err = %v, want read stdin wrapper", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
}

func TestRunStdoutWriteErrorWraps(t *testing.T) {
	path := writeManifestTestFile(t, t.TempDir(), "artifact.bin", "contents")
	err := run([]string{path}, strings.NewReader(""), errWriter{err: io.ErrClosedPipe})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("err = %v, want wrapped closed pipe", err)
	}
	if !strings.Contains(err.Error(), "manifest-cli: write stdout") {
		t.Fatalf("err = %v, want write stdout wrapper", err)
	}
}
