package main

import (
	"strings"
	"testing"
)

func TestParseScheme(t *testing.T) {
	tests := map[string]string{
		"tls://localhost:4222":               "tls",
		"nats://localhost:4222":              "nats",
		"rediss://:secret@localhost:6379/0":  "rediss",
		"redis://:secret@localhost:6379/0":   "redis",
		"localhost:4222":                     "unknown",
		"://missing":                         "unknown",
		"  tls://nats:4222?token=redacted  ": "tls",
	}
	for raw, want := range tests {
		if got := parseScheme(raw); got != want {
			t.Fatalf("parseScheme(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestNATSConnectOptionsReadTLSConfigFromEnv(t *testing.T) {
	t.Setenv("NATS_TLS_CA", "does-not-exist-ca.crt")
	t.Setenv("NATS_TLS_CERT", "")
	t.Setenv("NATS_TLS_KEY", "")
	t.Setenv("NATS_TLS_INSECURE", "")
	t.Setenv("NATS_TLS_SERVER_NAME", "")

	_, err := natsConnectOptions("hello-worker")
	if err == nil {
		t.Fatal("expected NATS TLS CA read error")
	}
	if !strings.Contains(err.Error(), "nats tls ca read") {
		t.Fatalf("error = %q, want nats tls ca read", err.Error())
	}
}
