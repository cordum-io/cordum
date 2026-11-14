package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordum/cordum/core/policysign"
	sdk "github.com/cordum/cordum/sdk/client"
)

// runPolicyCmd is the entry point for `cordumctl policy ...`. It
// dispatches to file and store signature commands.
func runPolicyCmd(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}
	switch args[0] {
	case "sign":
		runPolicySignCmd(args[1:])
	case "verify":
		runPolicySignVerifyCmd(args[1:])
	case "verify-store":
		runPolicyVerifyStoreCmd(args[1:])
	case "resign":
		runPolicyResignCmd(args[1:])
	default:
		usage()
		os.Exit(1)
	}
}

// runPolicySignCmd signs a policy file with an ed25519 key and writes
// a .sig sidecar containing the JSON-encoded policysign.Signature.
func runPolicySignCmd(args []string) {
	fs := newFlagSet("policy sign")
	in := fs.String("in", "", "path to policy file to sign")
	out := fs.String("out", "", "path to signature file (default: <in>.sig)")
	keyEnv := fs.String("key-env", policysign.EnvSigningKey, "env var holding the ed25519 private key")
	keyID := fs.String("key-id", "", "key id to embed in signature (defaults to "+policysign.EnvSigningKeyID+" or \"default\")")
	fs.ParseArgs(args)
	if strings.TrimSpace(*in) == "" {
		fail("--in required")
	}

	data, err := os.ReadFile(*in) // #nosec G304 -- CLI reads user-specified path by design.
	check(err)

	privRaw := strings.TrimSpace(os.Getenv(*keyEnv))
	if privRaw == "" {
		fail(fmt.Sprintf("%s not set (or empty); export the ed25519 private key (PEM or base64)", *keyEnv))
	}
	priv, err := parseSigningPrivateKey(privRaw)
	if err != nil {
		fail(fmt.Sprintf("parse private key: %v", err))
	}

	id := strings.TrimSpace(*keyID)
	if id == "" {
		id = strings.TrimSpace(os.Getenv(policysign.EnvSigningKeyID))
	}
	if id == "" {
		id = policysign.DefaultKeyID
	}

	sig, err := policysign.Sign(priv, id, data)
	check(err)
	sigBytes, err := json.MarshalIndent(sig, "", "  ")
	check(err)

	outPath := strings.TrimSpace(*out)
	if outPath == "" {
		outPath = *in + ".sig"
	}
	if err := os.WriteFile(outPath, sigBytes, 0o600); err != nil {
		fail(fmt.Sprintf("write signature: %v", err))
	}
	fmt.Fprintf(os.Stderr, "signed %s -> %s (key_id=%s, hash=%s)\n", filepath.Clean(*in), filepath.Clean(outPath), sig.KeyID, sig.Hash)
}

// runPolicySignVerifyCmd verifies a policy file against its signature.
// Exit codes:
//
//	0  signature valid
//	1  signature invalid (verification failed)
//	2  user / config error (missing flags, unreadable inputs, key issues)
func runPolicySignVerifyCmd(args []string) {
	fs := newFlagSet("policy verify")
	in := fs.String("in", "", "path to policy file")
	sigPath := fs.String("sig", "", "path to signature file (default: <in>.sig)")
	pubEnv := fs.String("public-key-env", "", "env var holding a trusted public key (optional; otherwise CORDUM_POLICY_PUBLIC_KEY_* / SAFETY_POLICY_PUBLIC_KEY are used)")
	fs.ParseArgs(args)
	if strings.TrimSpace(*in) == "" {
		exitPolicyVerify(2, "--in required")
	}

	data, err := os.ReadFile(*in) // #nosec G304 -- CLI reads user-specified path.
	if err != nil {
		exitPolicyVerify(2, fmt.Sprintf("read policy: %v", err))
	}

	resolvedSig := strings.TrimSpace(*sigPath)
	if resolvedSig == "" {
		resolvedSig = *in + ".sig"
	}
	sigBytes, err := os.ReadFile(resolvedSig) // #nosec G304 -- CLI reads user-specified path.
	if err != nil {
		exitPolicyVerify(2, fmt.Sprintf("read signature: %v", err))
	}

	var sig policysign.Signature
	trimmed := strings.TrimSpace(string(sigBytes))
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal([]byte(trimmed), &sig); err != nil {
			exitPolicyVerify(2, fmt.Sprintf("decode signature json: %v", err))
		}
	} else {
		// Legacy raw bytes: we cannot verify without an explicit key id.
		exitPolicyVerify(2, "signature file is not a JSON policysign.Signature; re-sign with cordumctl policy sign")
	}

	store, storeErr := loadTrustStoreWithOverride(*pubEnv)
	if storeErr != nil {
		exitPolicyVerify(2, storeErr.Error())
	}
	pub, ok := store.Lookup(sig.KeyID)
	if !ok {
		exitPolicyVerify(1, fmt.Sprintf("key_id %q is not trusted (known ids: %s)", sig.KeyID, strings.Join(store.IDs(), ", ")))
	}
	if err := policysign.Verify(pub, data, sig); err != nil {
		exitPolicyVerify(1, fmt.Sprintf("verification failed: %v", err))
	}
	fmt.Printf("policy %s verified against key_id=%s hash=%s\n", *in, sig.KeyID, sig.Hash)
}

// exitPolicyVerify writes msg to stderr and exits with code. Keeps the
// verify command's exit contract self-documenting.
func exitPolicyVerify(code int, msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(code)
}

// parseSigningPrivateKey accepts PEM, base64, hex, or raw ed25519 key
// material and returns a usable private key. Thin wrapper around
// policysign.ParsePrivateKey so CLI error messages can wrap the call
// site context.
func parseSigningPrivateKey(raw string) (ed25519.PrivateKey, error) {
	return policysign.ParsePrivateKey(raw)
}

// loadTrustStoreWithOverride returns a TrustStore from the environment,
// optionally supplemented with the public key referenced by --public-key-env.
// The override key is registered under its own id (the env var name) so
// callers can verify against a specific key.
func loadTrustStoreWithOverride(pubEnv string) (*policysign.TrustStore, error) {
	store, err := policysign.LoadTrustStoreFromEnv()
	if err != nil {
		return nil, fmt.Errorf("load trust store: %w", err)
	}
	pubEnv = strings.TrimSpace(pubEnv)
	if pubEnv != "" {
		raw := strings.TrimSpace(os.Getenv(pubEnv))
		if raw == "" {
			return nil, fmt.Errorf("%s not set", pubEnv)
		}
		pub, err := policysign.ParsePublicKey(raw)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", pubEnv, err)
		}
		// Register under the env var name so verify errors mention it.
		if err := store.Add(pubEnv, pub); err != nil {
			return nil, fmt.Errorf("add override key: %w", err)
		}
	}
	if store.Len() == 0 {
		return nil, errors.New("no trusted public keys configured (set CORDUM_POLICY_PUBLIC_KEY_<ID> or use --public-key-env)")
	}
	return store, nil
}

type storeBundleRefusal struct{ id, category, remedy string }

// policyStoreContent follows the kernel's enabled and first-string selection.
func policyStoreContent(raw any) string {
	if content, ok := raw.(string); ok {
		return content
	}
	bundle, _ := raw.(map[string]any)
	if enabled, exists := bundle["enabled"]; exists && !slices.Contains([]string{"1", "true", "yes", "y", "on"}, strings.ToLower(strings.TrimSpace(fmt.Sprint(enabled)))) {
		return ""
	}
	for _, field := range []string{"content", "policy", "data"} {
		if content, ok := bundle[field].(string); ok {
			return content
		}
	}
	return ""
}

func storeBundleCategory(raw any, content string, trust *policysign.TrustStore) string {
	bundle, _ := raw.(map[string]any)
	fields, _ := bundle["_signature"].(map[string]any)
	if len(fields) == 0 {
		return "unsigned"
	}
	text := func(key string) string { value, _ := fields[key].(string); return strings.TrimSpace(value) }
	sig := policysign.Signature{Algorithm: text("algorithm"), KeyID: text("key_id"), Value: text("value"), Hash: text("hash")}
	switch n := fields["signed_bytes"].(type) {
	case float64:
		sig.SignedBytes = int(n)
	case int:
		sig.SignedBytes = n
	case int64:
		sig.SignedBytes = int(n)
	}
	if sig.Algorithm == "" || sig.Value == "" {
		return "malformed"
	}
	pub, ok := trust.Lookup(sig.KeyID)
	if !ok {
		return "untrusted-key"
	}
	switch err := policysign.Verify(pub, []byte(content), sig); {
	case err == nil:
		return ""
	case errors.Is(err, policysign.ErrUnsupportedAlgo), errors.Is(err, policysign.ErrInvalidSignature):
		return "malformed"
	case errors.Is(err, policysign.ErrHashMismatch):
		return "stale-signature"
	default:
		return "bad-signature"
	}
}

func classifyStoreBundles(bundles map[string]any, trust *policysign.TrustStore, owners map[string]string) []storeBundleRefusal {
	var refusals []storeBundleRefusal
	for id, raw := range bundles {
		content := policyStoreContent(raw)
		if strings.TrimSpace(content) == "" {
			continue
		}
		if category := storeBundleCategory(raw, content, trust); category != "" {
			remedy := "resign"
			if owner := owners[id]; owner != "" {
				remedy = "reinstall-pack:" + owner
			}
			refusals = append(refusals, storeBundleRefusal{id, category, remedy})
		}
	}
	slices.SortFunc(refusals, func(a, b storeBundleRefusal) int { return strings.Compare(a.id, b.id) })
	return refusals
}

func policyStoreCheck(client *restClient, pubEnv string) ([]storeBundleRefusal, int) {
	trust, err := loadTrustStoreWithOverride(pubEnv)
	if err != nil {
		exitPolicyVerify(2, "no trusted policy keys: export the safety kernel's CORDUM_POLICY_PUBLIC_KEY_<ID> values; "+err.Error())
	}
	doc, err := client.getConfig(context.Background(), "system", "policy")
	if err != nil {
		exitPolicyVerify(2, err.Error())
	}
	bundles, ok := doc.Data["bundles"].(map[string]any)
	if !ok && doc.Data["bundles"] != nil {
		exitPolicyVerify(2, "policy bundles must be an object")
	}
	records, err := listPackRecords(context.Background(), client)
	if err != nil {
		exitPolicyVerify(2, err.Error())
	}
	ids, owners, count := []string{}, map[string]string{}, 0
	for id := range records {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		for _, fragment := range records[id].Overlays.Policy {
			if owners[fragment.FragmentID] == "" {
				owners[fragment.FragmentID] = id
			}
		}
	}
	for _, raw := range bundles {
		if strings.TrimSpace(policyStoreContent(raw)) != "" {
			count++
		}
	}
	return classifyStoreBundles(bundles, trust, owners), count
}

func policyStoreClient(fs *flagSet) *restClient {
	transport, err := sdk.BuildTLSTransportErr(fs.tlsOptions())
	if err != nil {
		exitPolicyVerify(2, fmt.Sprintf("tls configuration error: %v", err))
	}
	return newRestClient(*fs.gateway, *fs.apiKey, *fs.tenant, transport)
}

func runPolicyVerifyStoreCmd(args []string) {
	fs := newFlagSet("policy verify-store")
	pubEnv := fs.String("public-key-env", "", "env var holding a trusted public key")
	fs.ParseArgs(args)
	if fs.NArg() != 0 {
		exitPolicyVerify(2, "unexpected argument")
	}
	refusals, count := policyStoreCheck(policyStoreClient(fs), *pubEnv)
	for _, refusal := range refusals {
		fmt.Printf("REFUSED %s %s remedy=%s\n", refusal.id, refusal.category, refusal.remedy)
	}
	if len(refusals) != 0 {
		fmt.Printf("%d of %d enabled policy bundles would be refused by the enforced store source\n", len(refusals), count)
		os.Exit(1)
	}
	fmt.Printf("all %d enabled policy bundles verify\n", count)
}

func runPolicyResignCmd(args []string) {
	fs := newFlagSet("policy resign")
	all := fs.Bool("all", false, "re-sign all refused non-pack bundles")
	pubEnv := fs.String("public-key-env", "", "env var holding a trusted public key for --all")
	var ids []string
	fs.Func("bundle", "bundle id (repeatable or comma-separated)", func(raw string) error {
		for _, id := range strings.Split(raw, ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				return errors.New("bundle id must not be empty")
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		return nil
	})
	fs.ParseArgs(args)
	if fs.NArg() != 0 || *all == (len(ids) > 0) {
		exitPolicyVerify(2, "select either --bundle ID or --all")
	}
	client, code := policyStoreClient(fs), 0
	if *all {
		refusals, _ := policyStoreCheck(client, *pubEnv)
		for _, refusal := range refusals {
			if refusal.remedy != "resign" {
				fmt.Printf("SKIPPED %s %s\n", refusal.id, refusal.remedy)
				code = 1
			} else {
				ids = append(ids, refusal.id)
			}
		}
	}
	for _, id := range ids {
		path := "/api/v1/policy/bundles/" + url.PathEscape(strings.ReplaceAll(id, "/", "~")) + "/resign"
		var result struct {
			KeyID string `json:"key_id"`
		}
		if err := client.doJSON(context.Background(), http.MethodPost, path, nil, &result); err != nil {
			exitPolicyVerify(1, err.Error())
		}
		fmt.Printf("RESIGNED %s key_id=%s\n", id, result.KeyID)
	}
	os.Exit(code)
}
