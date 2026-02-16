package packs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// buildTarGz creates a gzipped tar archive from the given entries.
func buildTarGz(t *testing.T, entries []tar.Header, contents map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for _, hdr := range entries {
		h := hdr // copy
		if data, ok := contents[h.Name]; ok {
			h.Size = int64(len(data))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("write tar header %s: %v", h.Name, err)
		}
		if data, ok := contents[h.Name]; ok {
			if _, err := tw.Write([]byte(data)); err != nil {
				t.Fatalf("write tar data %s: %v", h.Name, err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return &buf
}

func TestExtractTarGzReader_RegularFilesAndDirs(t *testing.T) {
	archive := buildTarGz(t, []tar.Header{
		{Name: "mypack/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "mypack/pack.yaml", Typeflag: tar.TypeReg, Mode: 0o644},
	}, map[string]string{
		"mypack/pack.yaml": "name: test\n",
	})

	dest := t.TempDir()
	if err := ExtractTarGzReader(archive, dest); err != nil {
		t.Fatalf("expected success for regular files, got: %v", err)
	}
}

func TestExtractTarGzReader_RejectsSymlink(t *testing.T) {
	archive := buildTarGz(t, []tar.Header{
		{Name: "mypack/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "mypack/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777},
	}, nil)

	dest := t.TempDir()
	err := ExtractTarGzReader(archive, dest)
	if err == nil {
		t.Fatal("expected error for symlink, got nil")
	}
	if !strings.Contains(err.Error(), "disallowed entry type") {
		t.Fatalf("expected 'disallowed entry type' in error, got: %v", err)
	}
}

func TestExtractTarGzReader_RejectsHardlink(t *testing.T) {
	archive := buildTarGz(t, []tar.Header{
		{Name: "mypack/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "mypack/hardlink", Typeflag: tar.TypeLink, Linkname: "mypack/target", Mode: 0o644},
	}, nil)

	dest := t.TempDir()
	err := ExtractTarGzReader(archive, dest)
	if err == nil {
		t.Fatal("expected error for hardlink, got nil")
	}
	if !strings.Contains(err.Error(), "disallowed entry type") {
		t.Fatalf("expected 'disallowed entry type' in error, got: %v", err)
	}
}

func TestExtractTarGzReader_RejectsDeviceNode(t *testing.T) {
	archive := buildTarGz(t, []tar.Header{
		{Name: "mypack/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "mypack/dev", Typeflag: tar.TypeChar, Mode: 0o666},
	}, nil)

	dest := t.TempDir()
	err := ExtractTarGzReader(archive, dest)
	if err == nil {
		t.Fatal("expected error for device node, got nil")
	}
	if !strings.Contains(err.Error(), "disallowed entry type") {
		t.Fatalf("expected 'disallowed entry type' in error, got: %v", err)
	}
}

func TestExtractTarGzReader_RejectsFifo(t *testing.T) {
	archive := buildTarGz(t, []tar.Header{
		{Name: "mypack/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "mypack/pipe", Typeflag: tar.TypeFifo, Mode: 0o644},
	}, nil)

	dest := t.TempDir()
	err := ExtractTarGzReader(archive, dest)
	if err == nil {
		t.Fatal("expected error for FIFO, got nil")
	}
	if !strings.Contains(err.Error(), "disallowed entry type") {
		t.Fatalf("expected 'disallowed entry type' in error, got: %v", err)
	}
}
