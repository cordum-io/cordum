package policysign

import (
	"errors"
	"testing"
)

func TestStrictModeResolution_ProductionDefaultsToEnforce(t *testing.T) {
	for _, tc := range []struct {
		name, env, production, strict, legacy string
		want                                  Mode
		invalid                               bool
	}{
		{"production unset", "production", "", "", "", ModeEnforce, false},
		{"production short name", "prod", "", "", "", ModeEnforce, false},
		{"production flag", "", "true", "", "", ModeEnforce, false},
		{"production blank", "production", "", " \t ", "", ModeEnforce, false},
		{"production explicit warn", "production", "", "warn", "", ModeWarn, false},
		{"production explicit off", "production", "", "off", "", ModeOff, false},
		{"production legacy false", "production", "", "", "false", ModeEnforce, false},
		{"production invalid", "production", "", "wobble", "", ModeEnforce, true},
		{"development unset", "", "", "", "", ModeWarn, false},
		{"development explicit enforce", "", "", "enforce", "", ModeEnforce, false},
		{"development legacy required", "", "", "", "true", ModeEnforce, false},
		{"development explicit warn beats legacy", "", "", "warn", "true", ModeWarn, false},
		{"development invalid", "", "", "wobble", "", ModeWarn, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CORDUM_ENV", tc.env)
			t.Setenv("CORDUM_PRODUCTION", tc.production)
			t.Setenv(EnvStrictMode, tc.strict)
			t.Setenv(EnvLegacyRequireSig, tc.legacy)
			got, err := ResolveMode()
			if got != tc.want {
				t.Errorf("mode = %v, want %v", got, tc.want)
			}
			if tc.invalid != errors.Is(err, ErrInvalidMode) || (!tc.invalid && err != nil) {
				t.Errorf("err = %v, want invalid=%v", err, tc.invalid)
			}
		})
	}
}
