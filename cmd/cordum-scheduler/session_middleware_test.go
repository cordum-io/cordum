package main

import (
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/policysign"
)

func clearSigningKeyEnv(t *testing.T) {
	t.Helper()
	t.Setenv(policysign.EnvSigningKey, "")
	t.Setenv(policysign.EnvSigningKeyPath, "")
	t.Setenv(policysign.EnvSigningKeyID, "")
	t.Setenv(policysign.EnvDevSigningSeed, "")
}

func TestHandshakeSecurityBundleWarnAndEnforceRequireSessionSigningAuthority(t *testing.T) {
	for _, mode := range []string{"warn", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			setValidHandshakeSecurityEnv(t, mode)
			clearSigningKeyEnv(t)
			cfg, err := loadHandshakeSecurityConfig()
			if err != nil {
				t.Fatal(err)
			}
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			deps := handshakeSecurityDependencies{
				redis: rdb, config: configsvc.NewFromClient(rdb), audit: &handshakeAuditRecorder{},
			}
			bundle, err := newHandshakeSecurityBundle(cfg, deps)
			if err == nil || !strings.Contains(err.Error(), "session-token authority unavailable") {
				t.Fatalf("%s without session signer must refuse boot; bundle=%#v err=%v", mode, bundle, err)
			}
		})
	}
}
