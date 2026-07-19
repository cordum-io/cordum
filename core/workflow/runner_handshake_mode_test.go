package workflow

import (
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
)

func TestRunRejectsUnknownHandshakeModesBeforeDependencies(t *testing.T) {
	for _, value := range []string{"enforse", "enforce-mode", "true", "1", ""} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CORDUM_SDK_HANDSHAKE", value)
			err := Run(&config.Config{RedisURL: "://invalid", NatsURL: "://invalid"})
			if err == nil || !strings.Contains(err.Error(), "CORDUM_SDK_HANDSHAKE") {
				t.Fatalf("Run error = %v, want CORDUM_SDK_HANDSHAKE validation", err)
			}
		})
	}
}
