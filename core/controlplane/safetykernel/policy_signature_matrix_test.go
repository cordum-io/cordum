package safetykernel

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policysign"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/require"
)

var policySignatureSources = map[string]struct{ parser, schemes, exclusion string }{
	"file":   {"loadPolicyBundle", "file", ""},
	"url":    {"loadPolicyBundle", "http://,https://", ""},
	"store":  {"policyLoader.loadFragments", "", ""},
	"shadow": {"ShadowLoader.refreshOnce", "", "shadow policies are compared, never activated"},
}

type signatureCell struct {
	source, state, profile string
	want                   error
}

var policySignatureCells = []signatureCell{
	{"file", "unsigned", "production", ErrBundleUnsigned},
	{"file", "untrusted", "production", ErrUntrustedKeyID},
	{"file", "other-key", "production", policysign.ErrVerifyFailed},
	{"file", "different-content", "production", policysign.ErrHashMismatch},
	{"file", "signed", "production", nil},
	{"file", "empty-trust", "production", ErrNoTrustStoreConfigured},
	{"url", "unsigned", "production", ErrBundleUnsigned},
	{"url", "untrusted", "production", ErrUntrustedKeyID},
	{"url", "other-key", "production", policysign.ErrVerifyFailed},
	{"url", "different-content", "production", policysign.ErrHashMismatch},
	{"url", "signed", "production", nil},
	{"url", "empty-trust", "production", ErrNoTrustStoreConfigured},
	{"store", "signed", "production", nil},
	{"file", "unsigned", "development", nil},
	{"file", "untrusted", "development", nil},
	{"file", "other-key", "development", nil},
	{"file", "different-content", "development", nil},
	{"file", "no-algorithm", "development", nil},
	{"file", "no-value", "development", nil},
	{"file", "invalid-value", "development", nil},
	{"file", "signed", "development", nil},
	{"url", "unsigned", "development", nil},
	{"url", "untrusted", "development", nil},
	{"url", "other-key", "development", nil},
	{"url", "different-content", "development", nil},
	{"url", "no-algorithm", "development", nil},
	{"url", "no-value", "development", nil},
	{"url", "invalid-value", "development", nil},
	{"url", "signed", "development", nil},
	{"store", "unsigned", "development", nil},
	{"store", "untrusted", "development", nil},
	{"store", "other-key", "development", nil},
	{"store", "different-content", "development", nil},
	{"store", "no-algorithm", "development", nil},
	{"store", "no-value", "development", nil},
	{"store", "invalid-value", "development", nil},
	{"store", "signed", "development", nil},
}

func TestPolicySignatureMatrix(t *testing.T) {
	for _, cell := range policySignatureCells {
		t.Run(cell.source+"/"+cell.profile+"/"+cell.state, func(t *testing.T) {
			require.Contains(t, policySignatureSources, cell.source)
			h := newSignatureHarness(t, cell)
			h.publish(t, "allow", "signed")
			require.NoError(t, h.server.reloadPolicy(t.Context(), h.loader, "notification"))
			before := h.identity(t)
			require.NotEmpty(t, before.snapshot)
			require.NotNil(t, before.policy)
			require.Equal(t, int64(1), before.history)
			require.Equal(t, pb.DecisionType_DECISION_TYPE_ALLOW, before.decision)
			h.publish(t, "deny", cell.state)
			if cell.state == "empty-trust" {
				t.Setenv("CORDUM_POLICY_PUBLIC_KEY_MATRIX", "")
			}
			err := h.server.reloadPolicy(t.Context(), h.loader, "poll")
			after := h.identity(t)
			if cell.want != nil {
				if !errors.Is(err, cell.want) {
					t.Errorf("reload error = %v, want %v", err, cell.want)
				}
				if after != before {
					t.Errorf("active identity changed: before=%+v after=%+v", before, after)
				}
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, after.snapshot)
			require.NotEqual(t, before.snapshot, after.snapshot)
			require.NotSame(t, before.policy, after.policy)
			require.Equal(t, before.history+1, after.history)
			require.Equal(t, pb.DecisionType_DECISION_TYPE_DENY, after.decision)
		})
	}
}

type signatureHarness struct {
	server       *server
	loader       *policyLoader
	key          ed25519.PrivateKey
	source, path string
	document     configsvc.Document
}

func newSignatureHarness(t *testing.T, cell signatureCell) *signatureHarness {
	t.Helper()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "CORDUM_POLICY_PUBLIC_KEY_") || strings.HasPrefix(name, "SAFETY_POLICY_") {
			t.Setenv(name, "")
		}
	}
	for _, name := range []string{"CORDUM_ENV", "CORDUM_PRODUCTION", "CORDUM_POLICY_STRICT", "CORDUM_POLICY_DEV_SIGNING_SEED", "SAFETY_POLICY_SIGNATURE_REQUIRED", "CORDUM_GATEWAY_PACK_STRICT", "SAFETY_POLICY_PUBLIC_KEY", "SAFETY_POLICY_SIGNATURE", "SAFETY_POLICY_SIGNATURE_PATH", "SAFETY_POLICY_URL"} {
		t.Setenv(name, "")
	}
	if cell.profile == "production" {
		t.Setenv("CORDUM_ENV", "production")
	}
	h := &signatureHarness{source: cell.source, path: filepath.Join(t.TempDir(), "policy.yaml"), key: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, ed25519.SeedSize))}
	t.Setenv("CORDUM_POLICY_PUBLIC_KEY_MATRIX", base64.StdEncoding.EncodeToString(h.key.Public().(ed25519.PublicKey)))
	s, mr := newTestServerWithVelocity(t, nil, "")
	require.False(t, mr.Exists("cfg:packs:strict_mode"))
	svc, err := configsvc.New("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	h.server = s
	if cell.source != "store" {
		t.Setenv("SAFETY_POLICY_PATH", h.path)
	}
	if cell.source == "url" {
		h.serveTLS(t)
	}
	h.loader = &policyLoader{source: policySourceFromEnv(os.Getenv("SAFETY_POLICY_PATH")), configSvc: svc, configScope: configsvc.ScopeSystem, configID: "policy", configKey: "bundles"}
	return h
}

func (h *signatureHarness) serveTLS(t *testing.T) {
	t.Helper()
	srv := httptest.NewTLSServer(http.FileServer(http.Dir(filepath.Dir(h.path))))
	previous := policyURLRootCAs
	policyURLRootCAs = x509.NewCertPool()
	policyURLRootCAs.AddCert(srv.Certificate())
	t.Cleanup(func() { srv.Close(); policyURLRootCAs = previous })
	t.Setenv("SAFETY_POLICY_URL", srv.URL+"/policy.yaml")
	t.Setenv("SAFETY_POLICY_URL_ALLOW_PRIVATE", "1")
	t.Setenv("SAFETY_POLICY_SIGNATURE_PATH", h.path+".sig")
}

func (h *signatureHarness) signature(t *testing.T, payload []byte, state string) *policysign.Signature {
	t.Helper()
	key, id := h.key, "MATRIX"
	switch state {
	case "unsigned":
		return nil
	case "untrusted":
		id = "OTHER"
	case "other-key":
		key = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, ed25519.SeedSize))
	case "different-content":
		payload = append(bytes.Clone(payload), '\n')
	}
	sig, err := policysign.Sign(key, id, payload)
	require.NoError(t, err)
	switch state {
	case "no-algorithm":
		sig.Algorithm = ""
	case "no-value":
		sig.Value = ""
	case "invalid-value":
		sig.Value = "!"
	}
	return &sig
}

func (h *signatureHarness) publish(t *testing.T, decision, state string) {
	t.Helper()
	payload := []byte(fmt.Sprintf("default_tenant: default\ndefault_decision: deny\nrules:\n  - id: matrix\n    match:\n      topics: [job.matrix]\n    decision: %s\n", decision))
	sig := h.signature(t, payload, state)
	if h.source == "store" {
		fragment := map[string]any{"content": string(payload)}
		if sig != nil {
			data, err := json.Marshal(sig)
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(data, &fields))
			fragment["_signature"] = fields
		}
		h.document.Scope, h.document.ScopeID = configsvc.ScopeSystem, "policy"
		h.document.Data = map[string]any{"bundles": map[string]any{"matrix": fragment}}
		require.NoError(t, h.loader.configSvc.Set(t.Context(), &h.document))
		return
	}
	require.NoError(t, os.WriteFile(h.path, payload, 0600))
	if sig == nil {
		require.NoError(t, os.Remove(h.path+".sig"))
		return
	}
	data, err := json.Marshal(sig)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(h.path+".sig", data, 0600))
}

type signatureIdentity struct {
	snapshot string
	policy   *config.SafetyPolicy
	history  int64
	decision pb.DecisionType
}

func (h *signatureHarness) identity(t *testing.T) signatureIdentity {
	t.Helper()
	h.server.mu.RLock()
	id := signatureIdentity{snapshot: h.server.snapshot, policy: h.server.policy}
	h.server.mu.RUnlock()
	n, err := h.server.resultClient.LLen(t.Context(), "cordum:safety:snapshots").Result()
	require.NoError(t, err)
	id.history = n
	response, err := h.server.Check(t.Context(), &pb.PolicyCheckRequest{JobId: "matrix", Tenant: "default", Topic: "job.matrix"})
	require.NoError(t, err)
	id.decision = response.GetDecision()
	return id
}

func TestPolicySignatureSourceInventory(t *testing.T) {
	wantCalls, calls := map[string]int{}, map[string]int{}
	wantSchemes, schemes := map[string]int{}, map[string]int{}
	for id, source := range policySignatureSources {
		wantCalls[source.parser] = 1
		if source.exclusion != "" {
			continue
		}
		covered := false
		for _, cell := range policySignatureCells {
			covered = covered || cell.source == id
		}
		require.True(t, covered, "source %s needs matrix entries", id)
		for _, scheme := range strings.Split(source.schemes, ",") {
			if scheme != "" {
				wantSchemes[scheme] = 1
			}
		}
	}
	packages, err := parser.ParseDir(token.NewFileSet(), ".", func(f os.FileInfo) bool { return !strings.HasSuffix(f.Name(), "_test.go") }, 0)
	require.NoError(t, err)
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			imports := signatureInventoryImports(t, file)
			for _, decl := range file.Decls {
				name := "<package>"
				if fn, ok := decl.(*ast.FuncDecl); ok {
					name = fn.Name.Name
					if fn.Recv != nil {
						typ := fn.Recv.List[0].Type
						if ptr, ok := typ.(*ast.StarExpr); ok {
							typ = ptr.X
						}
						receiver, ok := typ.(*ast.Ident)
						require.True(t, ok, "receiver needs an inventory entry: %s", name)
						name = receiver.Name + "." + name
					}
				}
				ast.Inspect(decl, func(n ast.Node) bool { signatureInventoryCall(t, n, name, imports, calls, schemes); return true })
			}
		}
	}
	require.Equal(t, wantCalls, calls, "policy parser call inventory")
	require.Equal(t, wantSchemes, schemes, "policy source scheme inventory")
}

func signatureInventoryImports(t *testing.T, file *ast.File) map[string]string {
	t.Helper()
	imports := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		require.NoError(t, err)
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		require.NotEqual(t, ".", name, "dot import needs explicit source inventory support")
		imports[name] = path
	}
	return imports
}

func signatureInventoryCall(t *testing.T, n ast.Node, name string, imports map[string]string, calls, schemes map[string]int) {
	t.Helper()
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		owner, _ := sel.X.(*ast.Ident)
		if owner != nil && imports[owner.Name] == "github.com/cordum/cordum/core/infra/config" && sel.Sel.Name == "ParseSafetyPolicy" {
			calls[name]++
		}
		if name == "readPolicySource" && owner != nil && imports[owner.Name] == "strings" && sel.Sel.Name == "HasPrefix" {
			require.Len(t, call.Args, 2)
			literal, ok := call.Args[1].(*ast.BasicLit)
			require.True(t, ok, "source prefix must have an inventory entry")
			prefix, err := strconv.Unquote(literal.Value)
			require.NoError(t, err)
			schemes[prefix]++
		}
	}
	if fun, ok := call.Fun.(*ast.Ident); ok && name == "readPolicySource" && fun.Name == "readPolicyFile" {
		schemes["file"]++
	}
}
