package scheduler

import (
	"bytes"
	"context"
	"encoding/hex"
	"log/slog"
	"strings"
	"testing"
)

func TestSagaMalformedCompensationLogsDigestNotRawBytes(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	saga := newSagaRedisManager(t, &fakeBus{}, &allowSafety{})
	raw := append([]byte{0xff, 0x01}, []byte("secret-token-canary")...)
	if err := saga.redis.LPush(context.Background(), sagaStackKey("wf-malformed"), raw).Err(); err != nil {
		t.Fatalf("push malformed compensation: %v", err)
	}
	if err := saga.Rollback(context.Background(), "wf-malformed"); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	output := logs.String()
	if strings.Contains(output, hex.EncodeToString(raw)) || strings.Contains(output, "secret-token-canary") {
		t.Fatalf("malformed compensation log leaked raw payload: %s", output)
	}
	if !strings.Contains(output, "raw_sha256") {
		t.Fatalf("malformed compensation log lacks safe digest: %s", output)
	}
}
