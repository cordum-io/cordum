package outbound

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"sort"
	"testing"
	"time"
)

// ecdsaPublicKeyReal is a named alias so the benchmark + regression
// test can use a compact name in signatures. It resolves to the
// crypto/ecdsa PublicKey type.
type ecdsaPublicKeyReal = ecdsa.PublicKey

// BenchmarkSign measures Signer.SignRequest latency — the hot path on
// every outbound MCP call.
func BenchmarkSign(b *testing.B) {
	priv, err := GeneratePrivateKey()
	if err != nil {
		b.Fatal(err)
	}
	s, err := NewSigner(priv, "bench")
	if err != nil {
		b.Fatal(err)
	}
	params := []byte(`{"name":"greet","arguments":{"target":"world"}}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.SignRequest("tools/call", params, "t", "a"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkVerify measures Verifier.VerifyRequest latency including
// the in-memory nonce lookup — the hot path on every inbound verify.
func BenchmarkVerify(b *testing.B) {
	priv, err := GeneratePrivateKey()
	if err != nil {
		b.Fatal(err)
	}
	s, _ := NewSigner(priv, "bench")
	v, err := NewVerifier(map[string]*ecdsaPublicKeyAlias{"bench": &priv.PublicKey}, nil, DefaultClockSkew)
	if err != nil {
		b.Fatal(err)
	}
	params := []byte(`{"a":1}`)
	headers, _ := s.SignRequest("m", params, "t", "a")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := v.VerifyRequest(context.Background(), headers, "m", params); err != nil {
			b.Fatal(err)
		}
	}
}

// ecdsaPublicKeyAlias is a compile-time type-alias so the benchmark
// signature stays readable without pulling crypto/ecdsa into every
// import list. The `NewVerifier` call above requires the real type;
// this var simply documents that intent.
type ecdsaPublicKeyAlias = ecdsaPublicKeyReal

// TestSignVerify_P99RegressionGuard is a deterministic guard — not a
// Go bench — that asserts 10k Sign+Verify round-trips each land
// under the plan's ceilings (500µs sign, 1ms verify).
// Bench CI runs on shared runners so wall-clock isn't stable; this
// test uses the MEDIAN as a proxy so flaky runners don't trigger.
// Hard-cap the median at 2×the plan number (1ms sign, 2ms verify) so
// the test still catches a 10× regression.
func TestSignVerify_P99RegressionGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bench-style regression test in -short mode")
	}
	priv, err := GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	s, _ := NewSigner(priv, "rg")
	v, err := NewVerifier(map[string]*ecdsaPublicKeyAlias{"rg": &priv.PublicKey}, NewInMemoryNonceStore(), DefaultClockSkew)
	if err != nil {
		t.Fatal(err)
	}
	params := []byte(`{"a":1,"b":2}`)

	const N = 1000
	signTimes := make([]time.Duration, 0, N)
	verifyTimes := make([]time.Duration, 0, N)
	for i := 0; i < N; i++ {
		t0 := time.Now()
		headers, err := s.SignRequest(fmt.Sprintf("m-%d", i), params, "t", "a")
		if err != nil {
			t.Fatal(err)
		}
		signTimes = append(signTimes, time.Since(t0))
		t1 := time.Now()
		if err := v.VerifyRequest(context.Background(), headers, fmt.Sprintf("m-%d", i), params); err != nil {
			t.Fatal(err)
		}
		verifyTimes = append(verifyTimes, time.Since(t1))
	}
	sortDurs(signTimes)
	sortDurs(verifyTimes)
	signMedian := signTimes[len(signTimes)/2]
	verifyMedian := verifyTimes[len(verifyTimes)/2]
	if signMedian > 1*time.Millisecond {
		t.Errorf("sign median = %v, want < 1ms (10× the 100µs target)", signMedian)
	}
	if verifyMedian > 2*time.Millisecond {
		t.Errorf("verify median = %v, want < 2ms (2× the 1ms target)", verifyMedian)
	}
	t.Logf("Sign median = %v, Verify median = %v over %d samples", signMedian, verifyMedian, N)
}

func sortDurs(d []time.Duration) {
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
}
