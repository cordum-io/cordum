package scheduler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

var errRevocationWrite = errors.New("injected revocation write failure")

type rejectRevocationSetHook struct{}

func (rejectRevocationSetHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

func (rejectRevocationSetHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		if cmd.Name() == "set" && len(args) > 1 && strings.HasPrefix(fmt.Sprint(args[1]), sessionRevokedKeyPrefix) {
			return errRevocationWrite
		}
		return next(ctx, cmd)
	}
}

func (rejectRevocationSetHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		return next(ctx, cmds)
	}
}

func TestSessionTokenIssueBound_PriorRevocationFailureKeepsCurrentAuthority(t *testing.T) {
	t.Parallel()
	issuer, _, client, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	binding := boundTestSession("tenant-a")
	current, currentClaims, err := issuer.IssueBound(ctx, binding)
	if err != nil {
		t.Fatalf("issue current: %v", err)
	}
	client.(*redis.Client).AddHook(rejectRevocationSetHook{})

	token, claims, err := issuer.IssueBound(ctx, binding)
	if !errors.Is(err, errRevocationWrite) {
		t.Fatalf("replacement error = %v, want revocation write failure", err)
	}
	if token != "" || claims != (SessionTokenClaims{}) {
		t.Fatalf("failed replacement leaked authority: token=%q claims=%+v", token, claims)
	}
	verified, err := issuer.VerifyBound(ctx, current, true)
	if err != nil {
		t.Fatalf("current authority changed after failed replacement: %v", err)
	}
	if verified.JTI != currentClaims.JTI {
		t.Fatalf("active JTI = %q, want %q", verified.JTI, currentClaims.JTI)
	}
}

func TestSessionTokenIssueBound_CorruptPriorBindingFailsClosed(t *testing.T) {
	t.Parallel()
	issuer, mr, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	binding := boundTestSession("tenant-a")
	if _, _, err := issuer.IssueBound(ctx, binding); err != nil {
		t.Fatalf("issue current: %v", err)
	}
	key := boundWorkerKey(binding.Tenant, binding.WorkerID)
	record := readActiveRecordForTest(t, mr, key)
	record.Tenant = "tenant-corrupt"
	writeActiveRecordForTest(t, mr, key, record)

	token, claims, err := issuer.IssueBound(ctx, binding)
	if !errors.Is(err, ErrSessionTokenBindingMismatch) {
		t.Fatalf("replacement error = %v, want binding mismatch", err)
	}
	if token != "" || claims != (SessionTokenClaims{}) {
		t.Fatalf("corrupt store minted authority: token=%q claims=%+v", token, claims)
	}
}

func TestSessionStoreKeysCannotCollideAcrossTenantSeparators(t *testing.T) {
	left := revokedKey("a:b", "j")
	right := revokedKey("a", "b:j")
	if left == right {
		t.Fatalf("revocation keys collide: %q", left)
	}
}

func TestLegacyWorkerKeyCannotEnterBoundNamespace(t *testing.T) {
	legacy := workerKey("v2:tenant:worker")
	bound := boundWorkerKey("tenant", "worker")
	if legacy == bound || strings.HasPrefix(legacy, sessionWorkerKeyPrefix+"v2:") {
		t.Fatalf("legacy worker key %q entered bound namespace %q", legacy, bound)
	}
}
