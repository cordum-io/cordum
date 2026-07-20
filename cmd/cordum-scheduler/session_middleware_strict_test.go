package main

import "testing"

func TestLoadHandshakeSecurityConfigRejectsTypoedMode(t *testing.T) {
	for _, typo := range []string{"enforse", "enforce-mode", "true", "1"} {
		t.Run(typo, func(t *testing.T) {
			clearHandshakeSecurityEnv(t)
			t.Setenv("CORDUM_SDK_HANDSHAKE", typo)
			if _, err := loadHandshakeSecurityConfig(); err == nil {
				t.Fatalf("typo'd mode %q must refuse boot", typo)
			}
		})
	}
}
