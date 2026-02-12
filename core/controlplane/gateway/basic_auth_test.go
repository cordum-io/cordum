package gateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeKeyStore struct {
	validateFn func(ctx context.Context, rawKey string) (*ManagedKey, error)
	recordFn   func(ctx context.Context, id string) error
}

func (f *fakeKeyStore) List(context.Context, string) ([]*ManagedKey, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeKeyStore) Create(context.Context, *ManagedKey, string) error {
	return errors.New("not implemented")
}

func (f *fakeKeyStore) Revoke(context.Context, string, string) error {
	return errors.New("not implemented")
}

func (f *fakeKeyStore) ValidateKey(ctx context.Context, rawKey string) (*ManagedKey, error) {
	if f.validateFn == nil {
		return nil, errors.New("not implemented")
	}
	return f.validateFn(ctx, rawKey)
}

func (f *fakeKeyStore) RecordUsage(ctx context.Context, id string) error {
	if f.recordFn == nil {
		return errors.New("not implemented")
	}
	return f.recordFn(ctx, id)
}

func TestUsageRecordingDrain(t *testing.T) {
	const calls = 3
	startCh := make(chan struct{}, calls)
	releaseCh := make(chan struct{})
	var recorded int32

	ks := &fakeKeyStore{
		validateFn: func(context.Context, string) (*ManagedKey, error) {
			return &ManagedKey{ID: "managed-key", Tenant: "default"}, nil
		},
		recordFn: func(ctx context.Context, id string) error {
			startCh <- struct{}{}
			defer atomic.AddInt32(&recorded, 1)
			select {
			case <-releaseCh:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}

	b := &BasicAuthProvider{
		defaultTenant: "default",
		keyStore:      ks,
	}

	for i := 0; i < calls; i++ {
		if _, err := b.authenticate(context.Background(), "managed-key", ""); err != nil {
			t.Fatalf("authenticate() error = %v", err)
		}
	}

	for i := 0; i < calls; i++ {
		select {
		case <-startCh:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for RecordUsage start (%d/%d)", i+1, calls)
		}
	}

	done := make(chan struct{})
	go func() {
		b.DrainUsage()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("DrainUsage returned before usage goroutines completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("DrainUsage timed out waiting for usage goroutines")
	}

	if got := atomic.LoadInt32(&recorded); got != calls {
		t.Fatalf("recorded usage count = %d, want %d", got, calls)
	}
}

func TestUsageRecordingErrorLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	ks := &fakeKeyStore{
		validateFn: func(context.Context, string) (*ManagedKey, error) {
			return &ManagedKey{ID: "managed-key", Tenant: "default"}, nil
		},
		recordFn: func(context.Context, string) error {
			return errors.New("record failed")
		},
	}

	b := &BasicAuthProvider{
		defaultTenant: "default",
		keyStore:      ks,
	}

	if _, err := b.authenticate(context.Background(), "managed-key", ""); err != nil {
		t.Fatalf("authenticate() error = %v", err)
	}

	b.DrainUsage()

	logOutput := buf.String()
	if !strings.Contains(logOutput, "failed to record api key usage") {
		t.Fatalf("expected warning log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "key_id=managed-key") {
		t.Fatalf("expected key_id in log, got %q", logOutput)
	}
}
