package policysign

import (
	"errors"
	"testing"
)

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{"", ModeWarn, false},
		{"off", ModeOff, false},
		{"OFF", ModeOff, false},
		{"disabled", ModeOff, false},
		{"none", ModeOff, false},
		{"false", ModeOff, false},
		{"0", ModeOff, false},
		{"warn", ModeWarn, false},
		{"WARN", ModeWarn, false},
		{" warn ", ModeWarn, false},
		{"warning", ModeWarn, false},
		{"enforce", ModeEnforce, false},
		{"ENFORCE", ModeEnforce, false},
		{"strict", ModeEnforce, false},
		{"required", ModeEnforce, false},
		{"true", ModeEnforce, false},
		{"1", ModeEnforce, false},
		{"medium", ModeWarn, true},
		{"??", ModeWarn, true},
	}
	for _, c := range cases {
		got, err := ParseMode(c.in)
		if c.wantErr && err == nil {
			t.Errorf("ParseMode(%q): expected error", c.in)
			continue
		}
		if !c.wantErr && err != nil {
			t.Errorf("ParseMode(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseMode(%q) = %v want %v", c.in, got, c.want)
		}
		if c.wantErr && !errors.Is(err, ErrInvalidMode) {
			t.Errorf("ParseMode(%q): err = %v want ErrInvalidMode", c.in, err)
		}
	}
}

func TestModeFromEnv(t *testing.T) {
	t.Setenv(EnvStrictMode, "")
	got, err := ModeFromEnv()
	if err != nil || got != ModeWarn {
		t.Errorf("default: got %v err=%v, want warn", got, err)
	}
	t.Setenv(EnvStrictMode, "enforce")
	got, err = ModeFromEnv()
	if err != nil || got != ModeEnforce {
		t.Errorf("enforce: got %v err=%v", got, err)
	}
	t.Setenv(EnvStrictMode, "huh")
	got, err = ModeFromEnv()
	if err == nil {
		t.Error("expected error for unknown env value")
	}
	if got != ModeWarn {
		t.Errorf("malformed env should fall back to warn, got %v", got)
	}
}

func TestMode_String(t *testing.T) {
	cases := map[Mode]string{
		ModeOff:     "off",
		ModeWarn:    "warn",
		ModeEnforce: "enforce",
		Mode(99):    "unknown(99)",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Errorf("Mode(%d).String() = %q want %q", int(m), got, want)
		}
	}
}

func TestMode_Predicates(t *testing.T) {
	if !ModeOff.SkipsVerification() {
		t.Error("ModeOff should skip")
	}
	if ModeWarn.SkipsVerification() || ModeEnforce.SkipsVerification() {
		t.Error("warn/enforce should not skip")
	}
	if !ModeEnforce.RejectsOnFailure() {
		t.Error("enforce should reject")
	}
	if ModeOff.RejectsOnFailure() || ModeWarn.RejectsOnFailure() {
		t.Error("off/warn should not reject")
	}
}
