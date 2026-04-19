package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordum/cordum/core/policysign"
)

// runPolicyCmd is the entry point for `cordumctl policy ...`. It
// dispatches to sign/verify subcommands.
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
