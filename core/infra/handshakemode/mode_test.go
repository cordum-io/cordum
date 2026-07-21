package handshakemode

import "testing"

func TestParseRequiresExplicitKnownMode(t *testing.T) {
	tests := []struct {
		raw  string
		want Mode
	}{
		{raw: "off", want: Off},
		{raw: " WARN ", want: Warn},
		{raw: "Enforce", want: Enforce},
	}
	for _, test := range tests {
		got, err := Parse(test.raw)
		if err != nil || got != test.want {
			t.Errorf("Parse(%q) = (%q, %v), want (%q, nil)", test.raw, got, err, test.want)
		}
	}
	for _, raw := range []string{"", " ", "bogus", "enforse", "true"} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) accepted invalid mode", raw)
		}
	}
}
