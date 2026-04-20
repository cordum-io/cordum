package policysign

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

func mustKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func TestSignVerify_RoundTrip(t *testing.T) {
	pub, priv := mustKeys(t)
	payload := []byte("policy: rules")
	sig, err := Sign(priv, "key-1", payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if sig.Algorithm != AlgorithmEd25519 {
		t.Errorf("algorithm = %q want %q", sig.Algorithm, AlgorithmEd25519)
	}
	if sig.KeyID != "key-1" {
		t.Errorf("key_id = %q want key-1", sig.KeyID)
	}
	if sig.SignedBytes != len(payload) {
		t.Errorf("signed_bytes = %d want %d", sig.SignedBytes, len(payload))
	}
	if len(sig.Hash) != 64 {
		t.Errorf("hash length = %d want 64", len(sig.Hash))
	}
	if err := Verify(pub, payload, sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestSign_RejectsEmptyPayload(t *testing.T) {
	_, priv := mustKeys(t)
	if _, err := Sign(priv, "key-1", nil); !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("expected ErrEmptyPayload, got %v", err)
	}
	if _, err := Sign(priv, "key-1", []byte{}); !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("expected ErrEmptyPayload, got %v", err)
	}
}

func TestSign_RejectsEmptyKeyID(t *testing.T) {
	_, priv := mustKeys(t)
	if _, err := Sign(priv, "   ", []byte("x")); !errors.Is(err, ErrEmptyKeyID) {
		t.Fatalf("expected ErrEmptyKeyID, got %v", err)
	}
}

func TestSign_RejectsBadPrivateKey(t *testing.T) {
	bad := ed25519.PrivateKey(make([]byte, 3))
	if _, err := Sign(bad, "k", []byte("x")); !errors.Is(err, ErrInvalidPrivateKey) {
		t.Fatalf("expected ErrInvalidPrivateKey, got %v", err)
	}
}

func TestVerify_RejectsTamperedPayload(t *testing.T) {
	pub, priv := mustKeys(t)
	payload := []byte("policy: a")
	sig, err := Sign(priv, "k", payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Flip a bit.
	tampered := append([]byte{}, payload...)
	tampered[0] ^= 0x01
	err = Verify(pub, tampered, sig)
	if err == nil {
		t.Fatal("expected verification to fail on tampered payload")
	}
	// Should be hash mismatch, since we embed the hash.
	if !errors.Is(err, ErrHashMismatch) && !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerify_RejectsWrongKey(t *testing.T) {
	_, priv := mustKeys(t)
	otherPub, _ := mustKeys(t)
	payload := []byte("policy: a")
	sig, err := Sign(priv, "k", payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := Verify(otherPub, payload, sig); !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("expected ErrVerifyFailed, got %v", err)
	}
}

func TestVerify_RejectsBadAlgorithm(t *testing.T) {
	pub, priv := mustKeys(t)
	payload := []byte("x")
	sig, _ := Sign(priv, "k", payload)
	sig.Algorithm = "hmac"
	if err := Verify(pub, payload, sig); !errors.Is(err, ErrUnsupportedAlgo) {
		t.Fatalf("expected ErrUnsupportedAlgo, got %v", err)
	}
}

func TestVerify_RejectsEmptySignature(t *testing.T) {
	pub, _ := mustKeys(t)
	if err := Verify(pub, []byte("x"), Signature{Algorithm: AlgorithmEd25519}); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestVerify_RejectsBadPublicKey(t *testing.T) {
	_, priv := mustKeys(t)
	sig, _ := Sign(priv, "k", []byte("x"))
	if err := Verify(ed25519.PublicKey(make([]byte, 3)), []byte("x"), sig); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("expected ErrInvalidPublicKey, got %v", err)
	}
}

func TestVerify_RejectsSignedBytesMismatch(t *testing.T) {
	pub, priv := mustKeys(t)
	payload := []byte("hello")
	sig, _ := Sign(priv, "k", payload)
	if err := Verify(pub, []byte("hello world"), sig); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("expected ErrHashMismatch, got %v", err)
	}
}

func TestHashPayload_IsStable(t *testing.T) {
	h1 := HashPayload([]byte("abc"))
	h2 := HashPayload([]byte("abc"))
	if h1 != h2 {
		t.Errorf("hash mismatch: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("unexpected hash length %d", len(h1))
	}
}

func TestSignature_IsZero(t *testing.T) {
	var zero Signature
	if !zero.IsZero() {
		t.Error("zero Signature should report IsZero")
	}
	_, priv := mustKeys(t)
	sig, _ := Sign(priv, "k", []byte("x"))
	if sig.IsZero() {
		t.Error("populated Signature should not report IsZero")
	}
}

func TestVerify_EmptyPayload(t *testing.T) {
	pub, _ := mustKeys(t)
	if err := Verify(pub, nil, Signature{Algorithm: AlgorithmEd25519, Value: "AA=="}); !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("expected ErrEmptyPayload, got %v", err)
	}
}

func TestVerify_GarbledValue(t *testing.T) {
	pub, priv := mustKeys(t)
	payload := []byte("x")
	sig, _ := Sign(priv, "k", payload)
	sig.Value = "!!!not-base64!!!"
	err := Verify(pub, payload, sig)
	if err == nil || !strings.Contains(err.Error(), "invalid signature") {
		t.Fatalf("expected invalid signature error, got %v", err)
	}
}
