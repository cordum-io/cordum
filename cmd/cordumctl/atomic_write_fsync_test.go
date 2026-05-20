package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAtomicWriteManagedSettings_SyncBeforeRename(t *testing.T) {
	assertAtomicWriterSyncBeforeRename(t, "edge_managed_settings.go", "atomicWriteManagedSettings")
}

func TestAtomicWriteAttachConfig_SyncBeforeRename(t *testing.T) {
	assertAtomicWriterSyncBeforeRename(t, "mcp_attach_common.go", "atomicWriteAttachConfig")
}

func assertAtomicWriterSyncBeforeRename(t *testing.T, fileName, funcName string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	body := atomicWriterBody(t, filepath.Join(filepath.Dir(thisFile), fileName), funcName)
	syncIdx := strings.Index(body, "tmp.Sync()")
	renameIdx := strings.Index(body, "os.Rename(")
	if syncIdx < 0 || renameIdx < 0 {
		t.Fatalf("%s missing tmp.Sync/tmp.Close/os.Rename sequence", funcName)
	}
	closeIdx := strings.LastIndex(body[:renameIdx], "tmp.Close()")
	if closeIdx < 0 {
		t.Fatalf("%s missing success-path tmp.Close before os.Rename", funcName)
	}
	if syncIdx >= closeIdx || closeIdx >= renameIdx {
		t.Fatalf("%s order: Sync=%d Close=%d Rename=%d; want Sync before Close before Rename", funcName, syncIdx, closeIdx, renameIdx)
	}
}

func atomicWriterBody(t *testing.T, path, funcName string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(raw)
	start := strings.Index(src, "func "+funcName+"(")
	if start < 0 {
		t.Fatalf("function %s not found in %s", funcName, path)
	}
	open := strings.Index(src[start:], "{")
	if open < 0 {
		t.Fatalf("function %s body terminator not found", funcName)
	}
	bodyStart := start + open
	depth := 0
	for i := bodyStart; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : i+1]
			}
		}
	}
	t.Fatalf("function %s body terminator not found", funcName)
	return ""
}
