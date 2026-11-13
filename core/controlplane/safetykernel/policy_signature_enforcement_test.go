package safetykernel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policysign"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const pseKeyID = "PSE"

// pseEnv pins one profile. It clears every inherited signing setting, trusts
// exactly one key and, for production, sets CORDUM_ENV=production.
func pseEnv(t *testing.T, production bool) ed25519.PrivateKey {
	t.Helper()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, policysign.EnvPublicKeyPrefix) || strings.HasPrefix(name, "SAFETY_POLICY_") {
			t.Setenv(name, "")
		}
	}
	for _, name := range []string{"CORDUM_ENV", "CORDUM_PRODUCTION", policysign.EnvStrictMode, policysign.EnvDevSigningSeed} {
		t.Setenv(name, "")
	}
	if production {
		t.Setenv("CORDUM_ENV", "production")
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	t.Setenv(policysign.EnvPublicKeyPrefix+pseKeyID, base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
	return key
}

func pseContent(decision string) string {
	return fmt.Sprintf("default_tenant: default\ndefault_decision: deny\nrules:\n  - id: pse-probe\n    match:\n      topics: [job.pse]\n    decision: %s\n", decision)
}

// pseSignature signs payload and returns the signature in its stored map
// form. edit, when set, changes the signature before it is stored.
func pseSignature(t *testing.T, key ed25519.PrivateKey, keyID, payload string, edit func(*policysign.Signature)) map[string]any {
	t.Helper()
	sig, err := policysign.Sign(key, keyID, []byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(&sig)
	}
	data, err := json.Marshal(sig)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

// pseStore holds a kernel and its config-store policy loader.
type pseStore struct {
	srv    *server
	loader *policyLoader
	doc    configsvc.Document // kept so each write carries the CAS revision
}

func newPSEStore(t *testing.T) *pseStore {
	t.Helper()
	srv, mr := newTestServerWithVelocity(t, nil, "")
	svc, err := configsvc.New("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	h := &pseStore{srv: srv, loader: &policyLoader{configSvc: svc, configScope: configsvc.ScopeSystem, configID: "policy", configKey: "bundles"}}
	h.doc.Scope, h.doc.ScopeID = configsvc.ScopeSystem, "policy"
	return h
}

func (h *pseStore) publish(t *testing.T, bundles map[string]any) {
	t.Helper()
	h.doc.Data = map[string]any{"bundles": bundles}
	if err := h.loader.configSvc.Set(context.Background(), &h.doc); err != nil {
		t.Fatal(err)
	}
}

// reload runs the kernel's policy reload, the path its watch loop takes.
func (h *pseStore) reload() error {
	return h.srv.reloadPolicy(context.Background(), h.loader, "notification")
}

type pseIdentity struct {
	snapshot string
	policy   *config.SafetyPolicy
	history  int64
	decision pb.DecisionType
}

func (h *pseStore) identity(t *testing.T) pseIdentity {
	t.Helper()
	h.srv.mu.RLock()
	id := pseIdentity{snapshot: h.srv.snapshot, policy: h.srv.policy}
	h.srv.mu.RUnlock()
	n, err := h.srv.resultClient.LLen(context.Background(), snapshotHistoryKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	id.history = n
	resp, err := h.srv.Check(context.Background(), &pb.PolicyCheckRequest{JobId: "pse-probe", Tenant: "default", Topic: "job.pse"})
	if err != nil {
		t.Fatal(err)
	}
	id.decision = resp.GetDecision()
	return id
}

// activateSigned makes a signed allow bundle the active store policy and
// returns its identity.
func (h *pseStore) activateSigned(t *testing.T, key ed25519.PrivateKey) pseIdentity {
	t.Helper()
	content := pseContent("allow")
	h.publish(t, map[string]any{"pse": map[string]any{"content": content, "_signature": pseSignature(t, key, pseKeyID, content, nil)}})
	if err := h.reload(); err != nil {
		t.Fatalf("signed store bundle refused: %v", err)
	}
	id := h.identity(t)
	if id.snapshot == "" || id.policy == nil || id.history != 1 || id.decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("signed store bundle did not activate: %+v", id)
	}
	return id
}

var pseOtherKey = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, ed25519.SeedSize))

func TestPolicySignatureMode_EverySourceSharesOneRule(t *testing.T) {
	for _, tc := range []struct {
		name, env, production, strict, legacy string
		want                                  policysign.Mode
	}{
		{"production unset", "production", "", "", "", policysign.ModeEnforce},
		{"production flag unset", "", "true", "", "", policysign.ModeEnforce},
		{"production blank", "production", "", "  ", "", policysign.ModeEnforce},
		{"production explicit warn", "production", "", "warn", "", policysign.ModeWarn},
		{"production explicit off", "production", "", "off", "", policysign.ModeOff},
		{"production invalid", "production", "", "wobble", "", policysign.ModeEnforce},
		{"development unset", "", "", "", "", policysign.ModeWarn},
		{"development explicit enforce", "", "", "enforce", "", policysign.ModeEnforce},
		{"development invalid", "", "", "wobble", "", policysign.ModeWarn},
		{"development legacy required", "", "", "", "true", policysign.ModeEnforce},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pseEnv(t, false)
			t.Setenv("CORDUM_ENV", tc.env)
			t.Setenv("CORDUM_PRODUCTION", tc.production)
			t.Setenv(policysign.EnvStrictMode, tc.strict)
			t.Setenv(policysign.EnvLegacyRequireSig, tc.legacy)
			shared, _ := policysign.ResolveMode()
			for source, mode := range map[string]policysign.Mode{
				"policysign": shared, "file and url": resolveFileLoaderMode(),
				"store": resolveStoreBundleMode(), "store verifier": newBundleVerifier().mode,
			} {
				if mode != tc.want {
					t.Errorf("%s mode = %v, want %v", source, mode, tc.want)
				}
			}
		})
	}
}

func TestStorePolicySignature_ProductionRefusesUnverifiedBundle(t *testing.T) {
	for _, tc := range []struct {
		name       string
		signature  func(t *testing.T, key ed25519.PrivateKey, content string) map[string]any
		emptyTrust bool
		want       error
	}{
		{"unsigned", func(*testing.T, ed25519.PrivateKey, string) map[string]any { return nil }, false, ErrBundleUnsigned},
		{"untrusted key id", func(t *testing.T, key ed25519.PrivateKey, content string) map[string]any {
			return pseSignature(t, key, "UNKNOWN", content, nil)
		}, false, ErrUntrustedKeyID},
		{"signed by another key", func(t *testing.T, _ ed25519.PrivateKey, content string) map[string]any {
			return pseSignature(t, pseOtherKey, pseKeyID, content, nil)
		}, false, policysign.ErrVerifyFailed},
		{"signature over other content", func(t *testing.T, key ed25519.PrivateKey, content string) map[string]any {
			return pseSignature(t, key, pseKeyID, content+"\n", nil)
		}, false, policysign.ErrHashMismatch},
		{"no trusted keys", func(t *testing.T, key ed25519.PrivateKey, content string) map[string]any {
			return pseSignature(t, key, pseKeyID, content, nil)
		}, true, ErrNoTrustStoreConfigured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := pseEnv(t, true)
			h := newPSEStore(t)
			before := h.activateSigned(t, key)
			bundle := map[string]any{"content": pseContent("deny")}
			if sig := tc.signature(t, key, pseContent("deny")); sig != nil {
				bundle["_signature"] = sig
			}
			h.publish(t, map[string]any{"pse": bundle})
			if tc.emptyTrust {
				t.Setenv(policysign.EnvPublicKeyPrefix+pseKeyID, "")
			}
			err := h.reload()
			if after := h.identity(t); after != before {
				t.Fatalf("active policy changed: before=%+v after=%+v", before, after)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("reload error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestStorePolicySignature_ProductionLoadsVerifiedPackFragment(t *testing.T) {
	key := pseEnv(t, true)
	if mode := resolveStoreBundleMode(); mode != policysign.ModeEnforce {
		t.Fatalf("store mode = %v, want enforce", mode)
	}
	h := newPSEStore(t)
	base := "default_tenant: default\ndefault_decision: deny\nrules:\n  - id: pse-base\n    match:\n      topics: [job.pse-other]\n    decision: allow\n"
	baseBundle := map[string]any{"content": base, "_signature": pseSignature(t, key, pseKeyID, base, nil)}
	h.publish(t, map[string]any{"pse-base": baseBundle})
	if err := h.reload(); err != nil {
		t.Fatalf("signed base bundle refused: %v", err)
	}
	before := h.identity(t)
	if before.snapshot == "" || before.history != 1 || before.decision != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("signed base bundle did not activate: %+v", before)
	}
	fragment := "rules:\n  - id: pse-pack-probe\n    match:\n      topics: [job.pse]\n    decision: allow\n"
	sum := sha256.Sum256([]byte(fragment))
	packFragment := func(signature map[string]any) map[string]any {
		bundle := map[string]any{"content": fragment, "version": "1.0.0", "sha256": hex.EncodeToString(sum[:]), "installed_at": "2026-11-13T00:00:00Z"}
		if signature != nil {
			bundle["_signature"] = signature
		}
		return bundle
	}
	// A refused fragment refuses the whole document: the updated, correctly
	// signed base bundle next to it must not become active either.
	base2 := strings.Replace(base, "job.pse-other", "job.pse-other-2", 1)
	base2Bundle := map[string]any{"content": base2, "_signature": pseSignature(t, key, pseKeyID, base2, nil)}
	for _, tc := range []struct {
		name      string
		signature map[string]any
		want      error
	}{
		{"fragment without signature", nil, ErrBundleUnsigned},
		{"fragment signed by another key", pseSignature(t, pseOtherKey, pseKeyID, fragment, nil), policysign.ErrVerifyFailed},
	} {
		h.publish(t, map[string]any{"pse-base": base2Bundle, "pse-pack/probe": packFragment(tc.signature)})
		err := h.reload()
		if after := h.identity(t); after != before {
			t.Fatalf("%s: active policy changed: before=%+v after=%+v", tc.name, before, after)
		}
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: reload error = %v, want %v", tc.name, err, tc.want)
		}
	}
	h.publish(t, map[string]any{"pse-base": baseBundle, "pse-pack/probe": packFragment(pseSignature(t, key, pseKeyID, fragment, nil))})
	if err := h.reload(); err != nil {
		t.Fatalf("verified pack fragment refused: %v", err)
	}
	after := h.identity(t)
	if after.snapshot == before.snapshot || after.policy == before.policy || after.history != before.history+1 || after.decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("verified pack fragment did not activate: before=%+v after=%+v", before, after)
	}
}

func TestStorePolicySignature_DevelopmentKeepsWarnMode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		signature func(t *testing.T, key ed25519.PrivateKey, content string) map[string]any
	}{
		{"unsigned", func(*testing.T, ed25519.PrivateKey, string) map[string]any { return nil }},
		{"signed by another key", func(t *testing.T, _ ed25519.PrivateKey, content string) map[string]any {
			return pseSignature(t, pseOtherKey, pseKeyID, content, nil)
		}},
		{"undecodable value", func(t *testing.T, key ed25519.PrivateKey, content string) map[string]any {
			return pseSignature(t, key, pseKeyID, content, func(s *policysign.Signature) { s.Value = "!" })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := pseEnv(t, false)
			h := newPSEStore(t)
			before := h.activateSigned(t, key)
			bundle := map[string]any{"content": pseContent("deny")}
			if sig := tc.signature(t, key, pseContent("deny")); sig != nil {
				bundle["_signature"] = sig
			}
			h.publish(t, map[string]any{"pse": bundle})
			if err := h.reload(); err != nil {
				t.Fatalf("development reload error = %v, want nil", err)
			}
			after := h.identity(t)
			if after.snapshot == before.snapshot || after.history != before.history+1 || after.decision != pb.DecisionType_DECISION_TYPE_DENY {
				t.Fatalf("development bundle did not activate: before=%+v after=%+v", before, after)
			}
		})
	}
}
