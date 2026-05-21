package model

import (
	"errors"
	"strings"
	"testing"
)

// TestBinaryVerifyEventValidate enforces the schema invariants — drift here
// breaks downstream SIEM mappings per docs/security/binary-signing.md §8.
func TestBinaryVerifyEventValidate(t *testing.T) {
	t.Parallel()
	validOK := BinaryVerifyEvent{
		Event:       BinaryVerifyEventOK,
		Hash:        "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90",
		Path:        "cordum-gateway",
		SigScheme:   BinaryVerifySigSchemeGPG,
		Fingerprint: "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
		Reason:      "",
		ExitCode:    0,
	}

	cases := []struct {
		name    string
		mutate  func(*BinaryVerifyEvent)
		wantErr string // substring; empty means expect Validate to pass
	}{
		{name: "valid ok+gpg+fingerprint", mutate: func(e *BinaryVerifyEvent) {}, wantErr: ""},
		{
			name: "valid fail+gpg+fingerprint",
			mutate: func(e *BinaryVerifyEvent) {
				e.Event = BinaryVerifyEventFail
				e.Reason = "hash mismatch foo"
				e.ExitCode = 1
			},
			wantErr: "",
		},
		{
			name: "valid ok+codesign+empty fingerprint",
			mutate: func(e *BinaryVerifyEvent) {
				e.SigScheme = BinaryVerifySigSchemeCodesign
				e.Fingerprint = ""
			},
			wantErr: "",
		},
		{
			name:    "unknown event",
			mutate:  func(e *BinaryVerifyEvent) { e.Event = "binary-verify-maybe" },
			wantErr: "event must be",
		},
		{
			name:    "hash too short",
			mutate:  func(e *BinaryVerifyEvent) { e.Hash = "deadbeef" },
			wantErr: "hash must match",
		},
		{
			name:    "hash uppercase rejected",
			mutate:  func(e *BinaryVerifyEvent) { e.Hash = strings.ToUpper(e.Hash) },
			wantErr: "hash must match",
		},
		{
			name:    "unknown sig_scheme",
			mutate:  func(e *BinaryVerifyEvent) { e.SigScheme = "rsa" },
			wantErr: "sig_scheme must be",
		},
		{
			name:    "gpg requires fingerprint",
			mutate:  func(e *BinaryVerifyEvent) { e.Fingerprint = "" },
			wantErr: "fingerprint required when sig_scheme is gpg",
		},
		{
			name:    "fingerprint lowercase rejected",
			mutate:  func(e *BinaryVerifyEvent) { e.Fingerprint = strings.ToLower(e.Fingerprint) },
			wantErr: "fingerprint must be empty",
		},
		{
			name:    "fingerprint short rejected",
			mutate:  func(e *BinaryVerifyEvent) { e.Fingerprint = "ABCD" },
			wantErr: "fingerprint must be empty",
		},
		{
			name:    "path absolute unix rejected",
			mutate:  func(e *BinaryVerifyEvent) { e.Path = "/etc/cordum-gateway" },
			wantErr: "must not be absolute",
		},
		{
			name:    "path absolute windows rejected",
			mutate:  func(e *BinaryVerifyEvent) { e.Path = `\Program Files\cordum-gateway` },
			wantErr: "must not be absolute",
		},
		{
			name:    "path drive-rooted rejected",
			mutate:  func(e *BinaryVerifyEvent) { e.Path = `C:\bin\cordum-gateway` },
			wantErr: "must not be drive-rooted",
		},
		{
			name:    "path parent-traversal rejected",
			mutate:  func(e *BinaryVerifyEvent) { e.Path = "../../etc/cordum-gateway" },
			wantErr: "must not contain parent-traversal",
		},
		{
			name:    "path empty rejected",
			mutate:  func(e *BinaryVerifyEvent) { e.Path = "" },
			wantErr: "must be non-empty",
		},
		{
			name: "reason too long rejected",
			mutate: func(e *BinaryVerifyEvent) {
				e.Event = BinaryVerifyEventFail
				e.ExitCode = 1
				e.Reason = strings.Repeat("x", MaxBinaryVerifyReasonLen+1)
			},
			wantErr: "reason exceeds",
		},
		{
			name:    "ok with non-zero exit_code rejected",
			mutate:  func(e *BinaryVerifyEvent) { e.ExitCode = 1 },
			wantErr: "exit_code must be 0 when event is binary-verify-ok",
		},
		{
			name: "fail with zero exit_code rejected",
			mutate: func(e *BinaryVerifyEvent) {
				e.Event = BinaryVerifyEventFail
				e.ExitCode = 0
				e.Reason = "hash mismatch foo"
			},
			wantErr: "exit_code must be non-zero when event is binary-verify-fail",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev := validOK
			tc.mutate(&ev)
			err := ev.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() unexpected err: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() expected error containing %q, got nil", tc.wantErr)
			}
			if !errors.Is(err, ErrInvalidBinaryVerify) {
				t.Errorf("Validate() error should wrap ErrInvalidBinaryVerify; got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Validate() err = %v; want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestBinaryVerifyEventIsFailure(t *testing.T) {
	t.Parallel()
	if (BinaryVerifyEvent{Event: BinaryVerifyEventOK}).IsFailure() {
		t.Error("IsFailure() = true on ok event")
	}
	if !(BinaryVerifyEvent{Event: BinaryVerifyEventFail}).IsFailure() {
		t.Error("IsFailure() = false on fail event")
	}
}
