package scheduler

import (
	"testing"
)

func TestParseHeartbeatMode(t *testing.T) {
	t.Parallel()
	cases := map[string]HeartbeatMode{
		"":            HeartbeatModeAuthority,
		"authority":   HeartbeatModeAuthority,
		"AUTHORITY":   HeartbeatModeAuthority,
		"warn":        HeartbeatModeWarn,
		"  warn  ":    HeartbeatModeWarn,
		"telemetry":   HeartbeatModeTelemetry,
		"  TELEMETRY": HeartbeatModeTelemetry,
		"bogus":       HeartbeatModeAuthority,
	}
	for in, want := range cases {
		if got := ParseHeartbeatMode(in); got != want {
			t.Errorf("ParseHeartbeatMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHeartbeatMode_EnforcesSession(t *testing.T) {
	t.Parallel()
	cases := map[HeartbeatMode]bool{
		HeartbeatModeAuthority: false,
		HeartbeatModeWarn:      true,
		HeartbeatModeTelemetry: true,
		HeartbeatMode(""):      false,
	}
	for m, want := range cases {
		if got := m.EnforcesSession(); got != want {
			t.Errorf("%s.EnforcesSession() = %v, want %v", m, got, want)
		}
	}
}

func TestHeartbeatMode_ConsultsHeartbeat(t *testing.T) {
	t.Parallel()
	cases := map[HeartbeatMode]bool{
		HeartbeatModeAuthority: true,
		HeartbeatModeWarn:      true,
		HeartbeatModeTelemetry: false,
	}
	for m, want := range cases {
		if got := m.ConsultsHeartbeat(); got != want {
			t.Errorf("%s.ConsultsHeartbeat() = %v, want %v", m, got, want)
		}
	}
}

func TestHeartbeatMode_EmitsDisagreement(t *testing.T) {
	t.Parallel()
	cases := map[HeartbeatMode]bool{
		HeartbeatModeAuthority: false,
		HeartbeatModeWarn:      true,
		HeartbeatModeTelemetry: false,
	}
	for m, want := range cases {
		if got := m.EmitsDisagreement(); got != want {
			t.Errorf("%s.EmitsDisagreement() = %v, want %v", m, got, want)
		}
	}
}

func TestHeartbeatMode_StringDefaults(t *testing.T) {
	t.Parallel()
	if HeartbeatMode("").String() != string(HeartbeatModeAuthority) {
		t.Fatal("empty mode must canonicalise to authority for log/format")
	}
	if HeartbeatModeWarn.String() != "warn" {
		t.Fatalf("warn.String()=%q", HeartbeatModeWarn.String())
	}
}

func TestClassifyDisagreement_Agreement(t *testing.T) {
	t.Parallel()
	if d := ClassifyDisagreement("w", "t", "j", true, true); d != nil {
		t.Fatalf("expected nil when both alive, got %+v", d)
	}
	if d := ClassifyDisagreement("w", "t", "j", false, false); d != nil {
		t.Fatalf("expected nil when both dead, got %+v", d)
	}
}

func TestClassifyDisagreement_SessionAllowsHeartbeatBlocks(t *testing.T) {
	t.Parallel()
	d := ClassifyDisagreement("w", "t", "j", true, false)
	if d == nil {
		t.Fatal("expected non-nil")
	}
	if d.Direction != "session_allows_heartbeat_blocks" {
		t.Fatalf("direction=%q", d.Direction)
	}
	if !d.SessionAuthAlive || d.HeartbeatAlive {
		t.Fatalf("flags: %+v", d)
	}
}

func TestClassifyDisagreement_SessionBlocksHeartbeatAllows(t *testing.T) {
	t.Parallel()
	d := ClassifyDisagreement("w", "t", "j", false, true)
	if d == nil {
		t.Fatal("expected non-nil")
	}
	if d.Direction != "session_blocks_heartbeat_allows" {
		t.Fatalf("direction=%q", d.Direction)
	}
	if d.SessionAuthAlive || !d.HeartbeatAlive {
		t.Fatalf("flags: %+v", d)
	}
}
