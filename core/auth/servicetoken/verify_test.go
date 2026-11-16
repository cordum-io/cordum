package servicetoken

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/policysign"
)

func verifierFixture(t *testing.T) (ed25519.PrivateKey, *policysign.TrustStore, Claims) {
	t.Helper()
	priv := testKey(t)
	trust := policysign.NewTrustStore()
	if err := trust.Add("primary", priv.Public().(ed25519.PublicKey)); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return priv, trust, Claims{
		Subject: IdentityScheduler, Tenant: ReservedTenant,
		SDKVersion: ServiceSDKVersion, JTI: "verify-1",
		IssuedAt: now, ExpiresAt: now.Add(DefaultServiceTTL),
	}
}

func signedToken(t *testing.T, priv ed25519.PrivateKey, kid string, claims Claims, typ string) string {
	t.Helper()
	token, err := Sign(priv, kid, claims, typ)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestVerifyServiceMintedToken(t *testing.T) {
	priv, trust, want := verifierFixture(t)
	token, err := MintService(priv, "primary", want.Subject, want.IssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyService(token, trust, want.IssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	want.JTI = got.JTI
	if got.JTI == "" || got != want {
		t.Fatalf("claims = %+v, want %+v with nonempty JTI", got, want)
	}
}

func TestVerifyServiceRefusals(t *testing.T) {
	priv, trust, claims := verifierFixture(t)
	now := claims.IssuedAt
	valid := signedToken(t, priv, "primary", claims, TypService)
	parts := strings.Split(valid, ".")
	sig := decodeSegment(t, parts[2])
	sig[0] ^= 0xff
	badSignature := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sig)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","kid":"primary","typ":"cordum-service"}`))
	unreserved := claims
	unreserved.Subject = "worker-1"
	cases := []struct {
		name, token string
		now         time.Time
		want        error
	}{
		{"Malformed", "not.a.token.extra", now, ErrMalformed},
		{"WrongAlgorithm", header + "." + parts[1] + "." + parts[2], now, ErrSignature},
		{"WrongType", signedToken(t, priv, "primary", claims, TypSession), now, ErrWrongType},
		{"UnknownKey", signedToken(t, priv, "unknown", claims, TypService), now, ErrUnknownKey},
		{"BadSignature", badSignature, now, ErrSignature},
		{"NotReserved", signedToken(t, priv, "primary", unreserved, TypService), now, ErrNotReserved},
		{"Expired", valid, claims.ExpiresAt.Add(DefaultSkew + time.Nanosecond), ErrExpired},
		{"NotYetValid", valid, now.Add(-DefaultSkew - time.Nanosecond), ErrNotYetValid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := VerifyService(tc.token, trust, tc.now)
			if !errors.Is(err, tc.want) || got != (Claims{}) {
				t.Fatalf("claims=%+v error=%v, want zero claims and %v", got, err, tc.want)
			}
		})
	}
}

func TestVerifyTokenTypes(t *testing.T) {
	priv, trust, want := verifierFixture(t)
	for _, typ := range []string{TypSession, TypService} {
		t.Run(typ, func(t *testing.T) {
			token := signedToken(t, priv, "primary", want, typ)
			got, gotTyp, err := Verify(token, trust.Lookup, want.IssuedAt)
			if err != nil || got != want || gotTyp != typ {
				t.Fatalf("claims=%+v typ=%q error=%v", got, gotTyp, err)
			}
		})
	}
}

func TestVerifyCallerBindsKeyIdentity(t *testing.T) {
	priv, trust, claims := verifierFixture(t)
	for _, subject := range []string{IdentityGateway, IdentityScheduler} {
		t.Run(subject, func(t *testing.T) {
			claims.Subject = subject
			token := signedToken(t, priv, "primary", claims, TypService)
			boundSubject := ""
			lookup := func(kid string) (ed25519.PublicKey, bool) {
				pub, ok := trust.Lookup(kid)
				if ok {
					boundSubject = IdentityGateway
				}
				return pub, ok
			}
			got, _, err := Verify(token, lookup, claims.IssuedAt)
			if err != nil {
				t.Fatal(err)
			}
			// The caller must bind the authenticated subject before its effect.
			effects := 0
			if got.Subject == boundSubject {
				effects++
			}
			wantEffects := 0
			if subject == IdentityGateway {
				wantEffects = 1
			}
			if effects != wantEffects {
				t.Fatalf("effects=%d, want %d", effects, wantEffects)
			}
		})
	}
}

func TestVerifyTimeBoundaries(t *testing.T) {
	priv, trust, claims := verifierFixture(t)
	token := signedToken(t, priv, "primary", claims, TypService)
	for _, now := range []time.Time{claims.IssuedAt.Add(-DefaultSkew), claims.ExpiresAt.Add(DefaultSkew)} {
		got, typ, err := Verify(token, trust.Lookup, now)
		if err != nil || got != claims || typ != TypService {
			t.Fatalf("boundary %v: claims=%+v typ=%q error=%v", now, got, typ, err)
		}
	}
}

func TestVerifySignatureLeavesClaimsPolicyToCaller(t *testing.T) {
	priv, trust, claims := verifierFixture(t)
	claims.Subject = ""
	token := signedToken(t, priv, "primary", claims, TypSession)
	got, typ, err := VerifySignature(token, trust.Lookup)
	if err != nil || got != claims || typ != TypSession {
		t.Fatalf("signature-only claims=%+v typ=%q error=%v", got, typ, err)
	}
	got, typ, err = Verify(token, trust.Lookup, claims.IssuedAt)
	if !errors.Is(err, ErrMalformed) || got != (Claims{}) || typ != "" {
		t.Fatalf("validated claims=%+v typ=%q error=%v", got, typ, err)
	}
}

func TestVerifySignatureDecodeRefusals(t *testing.T) {
	priv, trust, claims := verifierFixture(t)
	valid := signedToken(t, priv, "primary", claims, TypService)
	parts := strings.Split(valid, ".")
	cases := []struct {
		name, header, body, signature string
		want                          error
	}{
		{"HeaderEncoding", "!", parts[1], parts[2], ErrMalformed},
		{"HeaderJSON", base64.RawURLEncoding.EncodeToString([]byte("{")), parts[1], parts[2], ErrMalformed},
		{"UnknownType", `{"alg":"Ed25519","kid":"primary","typ":"other"}`, parts[1], "", ErrMalformed},
		{"EmptyKey", `{"alg":"Ed25519","kid":" ","typ":"cordum-service"}`, parts[1], "", ErrSignature},
		{"SignatureEncoding", parts[0], parts[1], "!", ErrMalformed},
		{"SignatureLength", parts[0], parts[1], "AA", ErrMalformed},
		{"ClaimsEncoding", parts[0], "!", "", ErrMalformed},
		{"ClaimsJSON", parts[0], base64.RawURLEncoding.EncodeToString([]byte("{")), "", ErrMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if json.Valid([]byte(tc.header)) {
				tc.header = base64.RawURLEncoding.EncodeToString([]byte(tc.header))
			}
			input := tc.header + "." + tc.body
			if tc.signature == "" {
				tc.signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(input)))
			}
			got, typ, err := VerifySignature(input+"."+tc.signature, trust.Lookup)
			if !errors.Is(err, tc.want) || got != (Claims{}) || typ != "" {
				t.Fatalf("claims=%+v typ=%q error=%v, want %v", got, typ, err, tc.want)
			}
		})
	}
}

func TestVerifyNormalizesKeyID(t *testing.T) {
	priv, trust, claims := verifierFixture(t)
	token := signedToken(t, priv, " PRIMARY ", claims, TypService)
	seen := ""
	lookup := func(kid string) (ed25519.PublicKey, bool) {
		seen = kid
		return trust.Lookup(kid)
	}
	got, typ, err := Verify(" \n"+token+"\t", lookup, claims.IssuedAt)
	if err != nil || got != claims || typ != TypService || seen != "primary" {
		t.Fatalf("claims=%+v typ=%q kid=%q error=%v", got, typ, seen, err)
	}
}

func TestVerifyUnusableLookup(t *testing.T) {
	priv, _, claims := verifierFixture(t)
	token := signedToken(t, priv, "primary", claims, TypService)
	cases := []struct {
		name   string
		lookup func(string) (ed25519.PublicKey, bool)
		want   error
	}{
		{"Nil", nil, ErrUnknownKey},
		{"Missing", func(string) (ed25519.PublicKey, bool) { return nil, false }, ErrUnknownKey},
		{"InvalidKey", func(string) (ed25519.PublicKey, bool) { return nil, true }, ErrSignature},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, typ, err := Verify(token, tc.lookup, claims.IssuedAt)
			if !errors.Is(err, tc.want) || got != (Claims{}) || typ != "" {
				t.Fatalf("claims=%+v typ=%q error=%v, want %v", got, typ, err, tc.want)
			}
		})
	}
	got, err := VerifyService(token, nil, claims.IssuedAt)
	if !errors.Is(err, ErrUnknownKey) || got != (Claims{}) {
		t.Fatalf("nil trust claims=%+v error=%v", got, err)
	}
}

func TestVerifySignaturePrecedesClaimsPolicy(t *testing.T) {
	priv, trust, claims := verifierFixture(t)
	claims.Subject = ""
	token := signedToken(t, priv, "primary", claims, TypService)
	parts := strings.Split(token, ".")
	parts[2] = base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	got, typ, err := Verify(strings.Join(parts, "."), trust.Lookup, claims.ExpiresAt.Add(time.Hour))
	if !errors.Is(err, ErrSignature) || got != (Claims{}) || typ != "" {
		t.Fatalf("claims=%+v typ=%q error=%v", got, typ, err)
	}
}
