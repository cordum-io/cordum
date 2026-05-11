package legacyshim_test

import (
	"testing"

	"github.com/cordum/cordum/core/policy/legacyshim"
)

func TestRecordCallIncrementsCounter(t *testing.T) {
	endpoint := "/api/v1/policy/test-record-call"
	shape := legacyshim.ShapeRequestOldResponseOld
	before := legacyshim.CallCount(endpoint, shape)
	legacyshim.RecordCall(endpoint, shape)
	legacyshim.RecordCall(endpoint, shape)
	after := legacyshim.CallCount(endpoint, shape)
	if got, want := after-before, 2.0; got != want {
		t.Errorf("counter delta after 2 RecordCall = %v, want %v", got, want)
	}
}

func TestRecordCallIgnoresEmptyLabels(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		shape    string
	}{
		{"empty endpoint", "", legacyshim.ShapeRequestOldResponseOld},
		{"empty shape", "/api/v1/policy/test-empty-shape", ""},
		{"both empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := legacyshim.CallCount(tc.endpoint, tc.shape)
			legacyshim.RecordCall(tc.endpoint, tc.shape)
			after := legacyshim.CallCount(tc.endpoint, tc.shape)
			if got := after - before; got != 0 {
				t.Errorf("RecordCall(%q,%q) incremented counter by %v, want 0", tc.endpoint, tc.shape, got)
			}
		})
	}
}

func TestCallCountIsolatesByLabels(t *testing.T) {
	shape := legacyshim.ShapeRequestOldResponseOld
	a := "/api/v1/policy/test-isolation-a"
	b := "/api/v1/policy/test-isolation-b"
	beforeA := legacyshim.CallCount(a, shape)
	beforeB := legacyshim.CallCount(b, shape)
	legacyshim.RecordCall(a, shape)
	if got := legacyshim.CallCount(a, shape) - beforeA; got != 1 {
		t.Errorf("endpoint A delta = %v, want 1", got)
	}
	if got := legacyshim.CallCount(b, shape) - beforeB; got != 0 {
		t.Errorf("endpoint B delta = %v after recording A only, want 0", got)
	}
}
