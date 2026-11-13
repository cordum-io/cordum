package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/packs/signing"
	"github.com/cordum/cordum/core/policysign"
	"github.com/redis/go-redis/v9"
)

const matrixPackID = "matrix-pack"
const matrixFragmentPath = "overlays/policy.fragment.yaml"

type packSignatureCell struct {
	profile, source, state string
	want                   packSignatureWant
}

type packSignatureWant struct {
	status           int
	code, prefix     string
	signed, fragment bool
	allowedKeys      []string
}

type packSignatureBundle struct {
	dir, content string
	tgz          []byte
}

var packSignatureSources = map[string][]string{
	"upload":      {"handleInstallPack", "installPackFromDir", "updatePackRegistry", "savePackRegistry"},
	"marketplace": {"handleMarketplaceInstall", "installPackFromDir", "updatePackRegistry", "savePackRegistry"},
	"cordumctl":   {"handleSetConfig"},
}

func packMatrixRefusal(code, prefix string) packSignatureWant {
	// The rejection's own audit entry is the only permitted state change.
	return packSignatureWant{status: 400, code: code, prefix: prefix, allowedKeys: []string{"cfg:system:policy_audit"}}
}

func TestPackSignatureMatrix(t *testing.T) {
	cells := []packSignatureCell{
		{"production", "marketplace", "signed", packSignatureWant{status: 200, signed: true}},
		{"production", "marketplace", "unknown-kid", packMatrixRefusal("PACK_INSTALL_INVALID", "pack.unknown_kid: ")},
		{"production", "marketplace", "wrong-key", packMatrixRefusal("PACK_INVALID_SIGNATURE", "pack.bad_signature: ")},
		{"production", "marketplace", "tampered", packMatrixRefusal("PACK_INSTALL_INVALID", "pack.tampered: ")},
		{"production", "marketplace", "malformed", packMatrixRefusal("PACK_INVALID_SIGNATURE", "pack.malformed: ")},
		{"development", "marketplace", "signed", packSignatureWant{status: 200, signed: true}},
		{"development", "marketplace", "unsigned", packSignatureWant{status: 200}},
		{"development", "marketplace", "unknown-kid", packMatrixRefusal("PACK_INSTALL_INVALID", "pack.unknown_kid: ")},
		{"development", "marketplace", "wrong-key", packMatrixRefusal("PACK_INVALID_SIGNATURE", "pack.bad_signature: ")},
		{"development", "marketplace", "tampered", packMatrixRefusal("PACK_INSTALL_INVALID", "pack.tampered: ")},
		{"development", "marketplace", "malformed", packMatrixRefusal("PACK_INVALID_SIGNATURE", "pack.malformed: ")},
		{"production", "cordumctl", "signed", packSignatureWant{status: 204, signed: true}},
		{"development", "cordumctl", "signed", packSignatureWant{status: 204, signed: true}},
		{"development", "cordumctl", "unsigned", packSignatureWant{status: 204}},
		{"production", "upload", "signed", packSignatureWant{status: 200, signed: true}},
		{"production", "upload", "unknown-kid", packMatrixRefusal("PACK_INSTALL_INVALID", "pack.unknown_kid: ")},
		{"production", "upload", "wrong-key", packMatrixRefusal("PACK_INVALID_SIGNATURE", "pack.bad_signature: ")},
		{"production", "upload", "tampered", packMatrixRefusal("PACK_INSTALL_INVALID", "pack.tampered: ")},
		{"production", "upload", "malformed", packMatrixRefusal("PACK_INVALID_SIGNATURE", "pack.malformed: ")},
		{"development", "upload", "signed", packSignatureWant{status: 200, signed: true}},
		{"development", "upload", "unsigned", packSignatureWant{status: 200}},
		{"development", "upload", "unknown-kid", packMatrixRefusal("PACK_INSTALL_INVALID", "pack.unknown_kid: ")},
		{"development", "upload", "wrong-key", packMatrixRefusal("PACK_INVALID_SIGNATURE", "pack.bad_signature: ")},
		{"development", "upload", "tampered", packMatrixRefusal("PACK_INSTALL_INVALID", "pack.tampered: ")},
		{"development", "upload", "malformed", packMatrixRefusal("PACK_INVALID_SIGNATURE", "pack.malformed: ")},
	}
	assertPackMatrixSources(t, cells)
	for _, cell := range cells {
		t.Run(cell.profile+"/"+cell.source+"/"+cell.state, func(t *testing.T) { runPackSignatureCell(t, cell) })
	}
}

func assertPackMatrixSources(t *testing.T, cells []packSignatureCell) {
	t.Helper()
	covered := map[string]bool{}
	for _, cell := range cells {
		if _, ok := packSignatureSources[cell.source]; !ok {
			t.Errorf("unlisted source %s", cell.source)
		}
		covered[cell.source+"/"+cell.profile] = true
	}
	for source := range packSignatureSources {
		for _, profile := range []string{"production", "development"} {
			if !covered[source+"/"+profile] {
				t.Errorf("source %s has no %s case", source, profile)
			}
		}
	}
}

func runPackSignatureCell(t *testing.T, cell packSignatureCell) {
	key := setPackSignatureProfile(t, cell.profile)
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	strict, err := s.redisClient().Exists(ctx, "cfg:packs:strict_mode").Result()
	packMatrixOK(t, err)
	if strict != 0 {
		t.Fatal("strict override present")
	}
	packMatrixOK(t, s.configSvc.Set(ctx, &configsvc.Document{Scope: "system", ScopeID: "default", Data: map[string]any{"pools": map[string]any{}}}))
	bundle := buildPackSignatureBundle(t, cell.state, key)
	drive := func() *httptest.ResponseRecorder { return uploadPackSignatureBundle(t, s, bundle) }
	switch cell.source {
	case "upload":
	case "marketplace":
		drive = preparePackMarketplace(t, s, bundle)
	case "cordumctl":
		drive = func() *httptest.ResponseRecorder { return replayPackConfig(t, s, bundle, cell.state) }
	default:
		t.Fatalf("unknown source %q", cell.source)
	}
	before := snapshotPackKeyspace(t, s)
	resp := drive()
	if cell.want.status >= 400 {
		assertPackRefused(t, s, before, resp, cell.want)
	} else {
		assertPackAccepted(t, s, before, resp, cell.want, bundle.content, cell.source)
		if cell.want.fragment {
			assertPackFragmentSignatureVerifies(t, s, matrixPackID, "safety")
		}
	}
}

func setPackSignatureProfile(t *testing.T, profile string) ed25519.PrivateKey {
	t.Helper()
	resetGatewayPackStrictCache()
	t.Cleanup(resetGatewayPackStrictCache)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "CORDUM_GATEWAY_PACK_TRUSTED_KEY_") || strings.HasPrefix(name, "CORDUM_GATEWAY_PACK_PUBLISHER_") {
			t.Setenv(name, "")
			packMatrixOK(t, os.Unsetenv(name))
		} else if strings.HasPrefix(name, "CORDUM_POLICY_PUBLIC_KEY_") {
			t.Setenv(name, "")
		}
	}
	for _, name := range []string{"CORDUM_ENV", "CORDUM_PRODUCTION", "CORDUM_GATEWAY_PACK_STRICT", "CORDUM_POLICY_STRICT", "SAFETY_POLICY_SIGNATURE_REQUIRED", "CORDUM_PACK_STRICT", "CORDUM_POLICY_SIGNING_KEY_PATH", "CORDUM_POLICY_DEV_SIGNING_SEED", "SAFETY_POLICY_PUBLIC_KEY", "SAFETY_POLICY_PUBLIC_KEY_ID", "CORDUM_MARKETPLACE_ALLOW_HTTP"} {
		t.Setenv(name, "")
	}
	if profile == "production" {
		t.Setenv("CORDUM_ENV", "production")
	} else if profile != "development" {
		t.Fatalf("unknown profile %q", profile)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	packMatrixOK(t, err)
	t.Setenv("CORDUM_GATEWAY_PACK_TRUSTED_KEY_MATRIX_PUB", base64.StdEncoding.EncodeToString(pub))
	policyPub, policyKey, err := ed25519.GenerateKey(rand.Reader)
	packMatrixOK(t, err)
	t.Setenv("CORDUM_POLICY_SIGNING_KEY", base64.StdEncoding.EncodeToString(policyKey))
	t.Setenv("CORDUM_POLICY_SIGNING_KEY_ID", "matrix-policy")
	t.Setenv("CORDUM_POLICY_PUBLIC_KEY_MATRIX-POLICY", base64.StdEncoding.EncodeToString(policyPub))
	return key
}

func packMatrixFiles() map[string]string {
	return map[string]string{
		"pack.yaml": `apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: matrix-pack
  version: 0.1.0
compatibility:
  protocolVersion: 1
topics:
  - name: job.matrix-pack.collect
resources:
  schemas:
    - id: matrix-pack/Incident
      path: schemas/Incident.json
  workflows:
    - id: matrix-pack.triage
      path: workflows/triage.yaml
overlays:
  config:
    - name: pools
      scope: system
      key: pools
      strategy: json_merge_patch
      path: overlays/pools.patch.yaml
  policy:
    - name: safety
      strategy: bundle_fragment
      path: overlays/policy.fragment.yaml
`,
		"schemas/Incident.json":     `{"type":"object","properties":{"message":{"type":"string"}}}`,
		"workflows/triage.yaml":     "id: matrix-pack.triage\norg_id: default\nname: Triage\nsteps:\n  approve:\n    type: approval\n",
		"overlays/pools.patch.yaml": "topics:\n  job.matrix-pack.collect: [matrix-pack]\npools:\n  matrix-pack:\n    requires: []\n",
		matrixFragmentPath:          "tenants:\n  default:\n    allow_topics:\n      - job.matrix-pack.*\n",
	}
}

func buildPackSignatureBundle(t *testing.T, state string, key ed25519.PrivateKey) packSignatureBundle {
	t.Helper()
	dir, files := t.TempDir(), packMatrixFiles()
	for name, content := range files {
		writePackMatrixFile(t, dir, name, []byte(content))
	}
	switch state {
	case "unsigned":
	case "malformed":
		files["pack.yaml.sig"] = "[not: a valid envelope"
	case "signed", "signed-fragment", "unknown-kid", "wrong-key", "tampered":
		kid := "matrix-pub"
		if state == "unknown-kid" || state == "wrong-key" {
			_, other, err := ed25519.GenerateKey(rand.Reader)
			packMatrixOK(t, err)
			key = other
		}
		if state == "unknown-kid" {
			kid = "matrix-other"
		}
		manifest, err := signing.BuildManifest(dir)
		packMatrixOK(t, err)
		signed, err := signing.SignManifest(manifest, key, kid)
		packMatrixOK(t, err)
		envelope, err := signing.EncodeEnvelopeYAML(signed)
		packMatrixOK(t, err)
		files["pack.yaml.sig"] = string(envelope)
		if state == "tampered" {
			files[matrixFragmentPath] += "# changed after signing\n"
		}
	default:
		t.Fatalf("unknown state %q", state)
	}
	for name, content := range files {
		writePackMatrixFile(t, dir, name, []byte(content))
	}
	return packSignatureBundle{dir: dir, content: files[matrixFragmentPath], tgz: buildTarGz(t, files)}
}

func writePackMatrixFile(t *testing.T, dir, name string, content []byte) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	packMatrixOK(t, os.MkdirAll(filepath.Dir(path), 0700))
	packMatrixOK(t, os.WriteFile(path, content, 0600))
}

func uploadPackSignatureBundle(t *testing.T, s *server, bundle packSignatureBundle) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("bundle", "matrix-pack.tgz")
	packMatrixOK(t, err)
	_, err = part.Write(bundle.tgz)
	packMatrixOK(t, err)
	packMatrixOK(t, w.Close())
	req := httptest.NewRequest("POST", "/api/v1/packs/install", &body)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp := httptest.NewRecorder()
	s.handleInstallPack(resp, req)
	return resp
}

func preparePackMarketplace(t *testing.T, s *server, bundle packSignatureBundle) func() *httptest.ResponseRecorder {
	t.Helper()
	// Allow HTTP for the loopback catalog transport, unrelated to signatures.
	t.Setenv("CORDUM_MARKETPLACE_ALLOW_HTTP", "1")
	sum := sha256.Sum256(bundle.tgz)
	catalog := packs.MarketplaceCatalogFile{Packs: []packs.MarketplaceCatalogPack{{ID: matrixPackID, Version: "0.1.0", Sha256: hex.EncodeToString(sum[:])}}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog.json":
			if err := json.NewEncoder(w).Encode(catalog); err != nil {
				t.Error(err)
			}
		case "/pack.tgz":
			if _, err := w.Write(bundle.tgz); err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	catalog.Packs[0].URL = ts.URL + "/pack.tgz"
	packMatrixOK(t, s.configSvc.Set(context.Background(), &configsvc.Document{Scope: "system", ScopeID: packs.PackCatalogID, Data: map[string]any{
		"catalogs": []map[string]any{{"id": "matrix", "url": ts.URL + "/catalog.json", "enabled": true}},
	}}))
	_, err := s.marketplaceSnapshot(context.Background(), false)
	packMatrixOK(t, err)
	return func() *httptest.ResponseRecorder {
		return packMatrixRequest(s.handleMarketplaceInstall, "POST", "/api/v1/marketplace/install", []byte(`{"catalog_id":"matrix","pack_id":"matrix-pack"}`))
	}
}

func packMatrixRequest(handler http.HandlerFunc, method, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler(resp, req)
	return resp
}

// Replay cmd/cordumctl/pack.go applyPolicyOverlay, updatePackRegistry,
// restClient.getConfig and restClient.setConfig, without client rollback.
func replayPackConfig(t *testing.T, s *server, bundle packSignatureBundle, state string) *httptest.ResponseRecorder {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(bundle.dir, matrixFragmentPath))
	packMatrixOK(t, err)
	manifest, err := packs.LoadPackManifest(bundle.dir)
	packMatrixOK(t, err)
	sum := sha256.Sum256(content)
	record := packs.PackRecord{ID: manifest.Metadata.ID, Version: manifest.Metadata.Version, Status: "ACTIVE", InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Manifest: packs.PackRecordManifest{Metadata: manifest.Metadata, Compatibility: manifest.Compatibility, Topics: manifest.Topics}, Verification: packMatrixClientVerification(state)}
	documents := []struct {
		id, key, entry string
		value          json.RawMessage
	}{
		{"policy", "bundles", matrixPackID + "/safety", packMatrixJSON(t, map[string]string{"content": string(content), "version": record.Version, "sha256": hex.EncodeToString(sum[:]), "installed_at": record.InstalledAt})},
		{"packs", "installed", matrixPackID, packMatrixJSON(t, record)},
	}
	var resp *httptest.ResponseRecorder
	for _, item := range documents {
		resp = packMatrixRequest(s.handleGetConfig, "GET", "/api/v1/config?scope=system&scope_id="+item.id+"&envelope=true", nil)
		var doc struct {
			Data map[string]json.RawMessage `json:"data"`
			Meta map[string]string          `json:"meta"`
		}
		if resp.Code == 200 {
			packMatrixOK(t, json.Unmarshal(resp.Body.Bytes(), &doc))
		} else if resp.Code != 404 {
			return resp
		}
		if doc.Data == nil {
			doc.Data = map[string]json.RawMessage{}
		}
		entries := map[string]json.RawMessage{}
		if raw := doc.Data[item.key]; len(raw) != 0 {
			packMatrixOK(t, json.Unmarshal(raw, &entries))
		}
		entries[item.entry] = item.value
		doc.Data[item.key] = packMatrixJSON(t, entries)
		payload := struct {
			Scope string                     `json:"scope"`
			ID    string                     `json:"scope_id"`
			Data  map[string]json.RawMessage `json:"data"`
			Meta  map[string]string          `json:"meta"`
		}{"system", item.id, doc.Data, doc.Meta}
		resp = packMatrixRequest(s.handleSetConfig, "POST", "/api/v1/config", packMatrixJSON(t, payload))
		if resp.Code < 200 || resp.Code >= 300 {
			return resp
		}
	}
	return resp
}

// The PackRecord JSON tags match cordumctl's packRecord and packRecordVerification.
// Values mirror VerifyInstallBundle and verificationFromResult in that command.
func packMatrixClientVerification(state string) *packs.PackRecordVerification {
	v := &packs.PackRecordVerification{SignatureAlgorithm: "ed25519", PackSignatureVer: 1}
	switch state {
	case "signed", "signed-fragment":
		v.Signed, v.KID, v.VerifiedAt = true, "matrix-pub", time.Now().UTC().Format(time.RFC3339)
	case "unsigned":
		v.Warnings = []string{"pack installed unsigned in non-strict mode"}
	default:
		v.Warnings = []string{"verify skipped via --no-verify"}
	}
	return v
}

func snapshotPackKeyspace(t *testing.T, s *server) map[string]string {
	t.Helper()
	ctx, client, result := context.Background(), s.redisClient(), map[string]string{}
	iter := client.Scan(ctx, 0, "*", 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		kind, err := client.Type(ctx, key).Result()
		packMatrixOK(t, err)
		var encoded []byte
		switch kind {
		case "string":
			v, err := client.Get(ctx, key).Result()
			packMatrixOK(t, err)
			encoded = packMatrixJSON(t, v)
		case "hash":
			v, err := client.HGetAll(ctx, key).Result()
			packMatrixOK(t, err)
			encoded = packMatrixJSON(t, v)
		case "list":
			v, err := client.LRange(ctx, key, 0, -1).Result()
			packMatrixOK(t, err)
			encoded = packMatrixJSON(t, v)
		case "set":
			v, err := client.SMembers(ctx, key).Result()
			packMatrixOK(t, err)
			slices.Sort(v)
			encoded = packMatrixJSON(t, v)
		case "zset":
			v, err := client.ZRangeWithScores(ctx, key, 0, -1).Result()
			packMatrixOK(t, err)
			encoded = packMatrixJSON(t, v)
		default:
			t.Fatalf("unsupported Redis type %q at %s", kind, key)
		}
		result[key] = kind + ":" + string(encoded)
	}
	packMatrixOK(t, iter.Err())
	return result
}

func diffPackKeyspace(before, after map[string]string) []string {
	var keys []string
	for key, old := range before {
		if value, ok := after[key]; !ok || value != old {
			keys = append(keys, key)
		}
	}
	for key := range after {
		if _, ok := before[key]; !ok {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func assertPackRefused(t *testing.T, s *server, before map[string]string, resp *httptest.ResponseRecorder, want packSignatureWant) {
	t.Helper()
	diff := diffPackKeyspace(before, snapshotPackKeyspace(t, s))
	if resp.Code != want.status {
		t.Errorf("status=%d want=%d body=%s keyspace=%v", resp.Code, want.status, resp.Body.String(), diff)
	}
	var body struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if resp.Body.Len() != 0 {
		packMatrixOK(t, json.Unmarshal(resp.Body.Bytes(), &body))
	}
	if body.Code != want.code || !strings.HasPrefix(body.Error, want.prefix) {
		t.Errorf("error response=%+v want code=%s prefix=%q", body, want.code, want.prefix)
	}
	if !reflect.DeepEqual(diff, want.allowedKeys) {
		t.Errorf("keyspace changed=%v permitted=%v", diff, want.allowedKeys)
	}
	audit, err := s.loadPolicyAudit(context.Background())
	packMatrixOK(t, err)
	if len(audit) != 1 || audit[0].Action != "pack.install.rejected" || audit[0].ResourceID != matrixPackID {
		t.Errorf("unexpected rejection audit: %+v", audit)
	}
	assertPackAbsent(t, s)
}

func assertPackAbsent(t *testing.T, s *server) {
	t.Helper()
	ctx := context.Background()
	records, _, err := s.loadPackRegistry(ctx)
	packMatrixOK(t, err)
	if _, ok := records[matrixPackID]; ok {
		t.Error("pack registry contains pack")
	}
	if fragment := packMatrixFragment(t, s); fragment != nil {
		t.Error("policy bundles contain fragment")
	}
	if _, err := s.schemaRegistry.Get(ctx, "matrix-pack/Incident"); !errors.Is(err, redis.Nil) {
		t.Errorf("schema absence: %v", err)
	}
	if _, err := s.workflowStore.GetWorkflow(ctx, "matrix-pack.triage"); !errors.Is(err, redis.Nil) {
		t.Errorf("workflow absence: %v", err)
	}
	reg, _, err := s.topicRegistry.Get(ctx, "job.matrix-pack.collect")
	packMatrixOK(t, err)
	if reg != nil {
		t.Errorf("topic present: %+v", reg)
	}
}

func assertPackAccepted(t *testing.T, s *server, before map[string]string, resp *httptest.ResponseRecorder, want packSignatureWant, content, source string) {
	t.Helper()
	if resp.Code != want.status {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	records, _, err := s.loadPackRegistry(context.Background())
	packMatrixOK(t, err)
	record, ok := records[matrixPackID]
	if !ok || record.Verification == nil {
		t.Fatal("registry record or verification absent")
	}
	if record.Verification.Signed != want.signed {
		t.Errorf("Signed=%v want=%v", record.Verification.Signed, want.signed)
	}
	if want.signed && record.Verification.KID != "matrix-pub" {
		t.Errorf("kid=%q", record.Verification.KID)
	}
	if !want.signed && len(record.Verification.Warnings) == 0 {
		t.Error("unsigned warning absent")
	}
	fragment := packMatrixFragment(t, s)
	if string(fragment["content"]) != string(packMatrixJSON(t, content)) {
		t.Errorf("policy fragment content mismatch: %s", fragment["content"])
	}
	diff := diffPackKeyspace(before, snapshotPackKeyspace(t, s))
	keys := []string{"cfg:system:packs", "cfg:system:policy"}
	if source != "cordumctl" {
		keys = append(keys, "schema:matrix-pack/Incident", "schema:index", "wf:def:matrix-pack.triage", "wf:index:all", "cfg:system:topics")
	}
	for _, key := range keys {
		if !slices.Contains(diff, key) {
			t.Errorf("positive control missing key %s; diff=%v", key, diff)
		}
	}
}

func packMatrixFragment(t *testing.T, s *server) map[string]json.RawMessage {
	t.Helper()
	doc, err := s.configSvc.Get(context.Background(), "system", "policy")
	if errors.Is(err, redis.Nil) {
		return nil
	}
	packMatrixOK(t, err)
	var bundles map[string]map[string]json.RawMessage
	packMatrixOK(t, json.Unmarshal(packMatrixJSON(t, doc.Data["bundles"]), &bundles))
	return bundles[matrixPackID+"/safety"]
}

func assertPackFragmentSignatureVerifies(t *testing.T, s *server, packID, overlay string) {
	t.Helper()
	if packID != matrixPackID || overlay != "safety" {
		t.Fatal("unexpected fragment identity")
	}
	fragment := packMatrixFragment(t, s)
	if len(fragment["_signature"]) == 0 {
		t.Fatal("stored fragment signature absent")
	}
	var sig policysign.Signature
	packMatrixOK(t, json.Unmarshal(fragment["_signature"], &sig))
	var content string
	packMatrixOK(t, json.Unmarshal(fragment["content"], &content))
	trust, err := policysign.LoadTrustStoreFromEnv()
	packMatrixOK(t, err)
	pub, ok := trust.Lookup(sig.KeyID)
	if !ok {
		t.Fatalf("untrusted fragment key %q", sig.KeyID)
	}
	packMatrixOK(t, policysign.Verify(pub, []byte(content), sig))
}

func packMatrixOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var packSignatureExclusions = map[string]string{
	"handleUninstallPack": "removes a pack; installs nothing",
}

func packMatrixInventoryFunctions(t *testing.T) (map[string]*ast.FuncDecl, map[string]bool) {
	t.Helper()
	files, err := os.ReadDir(".")
	packMatrixOK(t, err)
	functions, discovered := map[string]*ast.FuncDecl{}, map[string]bool{}
	for _, entry := range files {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		packMatrixOK(t, err)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			functions[fn.Name.Name] = fn
			if packMatrixRegistryWriter(fn) {
				discovered[fn.Name.Name] = true
			}
		}
	}
	return functions, discovered
}

func TestPackSignatureSourceInventory(t *testing.T) {
	functions, discovered := packMatrixInventoryFunctions(t)
	covered := map[string]bool{}
	for source, entries := range packSignatureSources {
		if len(entries) == 0 {
			t.Errorf("source %s has no entry points", source)
		}
		for _, name := range entries {
			covered[name] = true
		}
	}
	for name, reason := range packSignatureExclusions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("exclusion %s has no reason", name)
		}
		covered[name] = true
	}
	for name := range discovered {
		if !covered[name] {
			t.Errorf("unmapped pack install/registry writer: %s", name)
		}
	}
	for name := range covered {
		if functions[name] == nil {
			t.Errorf("source entry no longer exists: %s", name)
		}
	}
	fn := functions["handleSetConfig"]
	if !packMatrixServerMethod(fn) || !slices.Contains(packSignatureSources["cordumctl"], "handleSetConfig") {
		t.Error("cordumctl must inventory the generic config *server method")
	}
}

func packMatrixRegistryWriter(fn *ast.FuncDecl) bool {
	var coreCall, registryID, set bool
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "PackRegistryID" {
			pkg, ok := selector.X.(*ast.Ident)
			registryID = registryID || (ok && pkg.Name == "packs")
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		var name string
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			name = fun.Name
		case *ast.SelectorExpr:
			name = fun.Sel.Name
		}
		switch name {
		case "installPackFromDir", "updatePackRegistry", "savePackRegistry":
			coreCall = true
		case "Set", "SetWithRetry":
			set = true
		}
		return true
	})
	return coreCall || (registryID && set)
}

func packMatrixServerMethod(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	name, ok := star.X.(*ast.Ident)
	return ok && name.Name == "server"
}
func packMatrixJSON[T any](t *testing.T, value T) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	packMatrixOK(t, err)
	return b
}
