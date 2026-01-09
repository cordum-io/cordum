package buildinfo

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestInfoAndLog(t *testing.T) {
	origVersion := Version
	origCommit := Commit
	origDate := Date
	t.Cleanup(func() {
		Version = origVersion
		Commit = origCommit
		Date = origDate
	})

	Version = "1.2.3"
	Commit = "abc123"
	Date = "2024-01-02"

	info := Info()
	if info != "version=1.2.3 commit=abc123 date=2024-01-02" {
		t.Fatalf("unexpected info: %s", info)
	}

	var buf bytes.Buffer
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(origOutput)
		log.SetFlags(origFlags)
	})

	Log("svc")
	got := strings.TrimSpace(buf.String())
	if !strings.Contains(got, "svc") || !strings.Contains(got, info) {
		t.Fatalf("unexpected log output: %s", got)
	}
}
