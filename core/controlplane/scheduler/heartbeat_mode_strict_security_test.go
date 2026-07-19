package scheduler

import "testing"

func TestParseHeartbeatModeStrictRejectsFailOpenTypo(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"warnn", "telemetery", "disabled"} {
		if _, err := ParseHeartbeatModeStrict(raw); err == nil {
			t.Fatalf("ParseHeartbeatModeStrict(%q) silently degraded to authority", raw)
		}
	}
}

func TestParseHeartbeatModeStrictPreservesDocumentedDefault(t *testing.T) {
	t.Parallel()
	mode, err := ParseHeartbeatModeStrict("")
	if err != nil || mode != HeartbeatModeAuthority {
		t.Fatalf("empty mode = %q, %v; want documented authority default", mode, err)
	}
}
