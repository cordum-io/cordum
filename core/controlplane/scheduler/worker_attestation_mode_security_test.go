package scheduler

import "testing"

func TestParseWorkerAttestationModeStrict(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		raw  string
		want WorkerAttestationMode
	}{
		"empty defaults off": {"", WorkerAttestationOff},
		"off":                {" OFF ", WorkerAttestationOff},
		"warn":               {"Warn", WorkerAttestationWarn},
		"enforce":            {"enforce", WorkerAttestationEnforce},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := ParseWorkerAttestationModeStrict(test.raw)
			if err != nil || got != test.want {
				t.Fatalf("ParseWorkerAttestationModeStrict(%q) = %q, %v; want %q", test.raw, got, err, test.want)
			}
		})
	}
}

func TestParseWorkerAttestationModeStrictRejectsTypo(t *testing.T) {
	t.Parallel()
	if mode, err := ParseWorkerAttestationModeStrict("enfore"); err == nil || mode != "" {
		t.Fatalf("invalid mode = %q, %v; want empty mode and error", mode, err)
	}
}

func TestNewEngineDoesNotReadWorkerAttestationEnvironment(t *testing.T) {
	t.Setenv(EnvWorkerAttestation, "enforce")
	engine := NewEngine(nil, nil, nil, nil, nil, nil)
	if engine.workerAttestation != WorkerAttestationOff {
		t.Fatalf("NewEngine worker attestation = %q; boot must pass parsed mode explicitly", engine.workerAttestation)
	}
}
