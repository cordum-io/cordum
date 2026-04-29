package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cordum/cordum/core/packs/signing"
	"gopkg.in/yaml.v3"
)

// Pack signing subcommands (task-6ced7932). Implemented here rather
// than extending the existing runPackCmd dispatch in pack.go so the
// large pack-install file stays focused on its own concerns.

const (
	envPackSigningKey  = "CORDUM_PACK_SIGNING_KEY"
	defaultPackSigFile = "pack.yaml.sig"
)

// dispatchPackSigningCmd is called from runPackCmd for subcommands
// that this file owns. Returns true when it handled the verb.
func dispatchPackSigningCmd(verb string, args []string) (handled bool, err error) {
	switch verb {
	case "keygen":
		return true, runPackSignKeygen(args)
	case "sign":
		return true, runPackSign(args)
	case "verify-signature":
		return true, runPackVerifySignature(args)
	case "export-key":
		return true, runPackExportKey(args)
	}
	return false, nil
}

// runPackSignKeygen generates a new Ed25519 keypair for pack signing.
func runPackSignKeygen(args []string) error {
	fs := newFlagSet("pack keygen")
	out := fs.String("out", defaultPackSigningKeyPath(), "private key output path (0600 perms on POSIX)")
	kid := fs.String("kid", "", "key id to advertise (default auto-generated pack-<8hex>)")
	force := fs.Bool("force", false, "overwrite an existing private key file")
	fs.ParseArgs(args)

	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ed25519 keypair: %w", err)
	}
	keyID := strings.TrimSpace(*kid)
	if keyID == "" {
		// Derive a stable key id from the public key so operators
		// can regenerate the same KID from a fresh export.
		sum := sha256.Sum256(pub)
		keyID = "pack-" + hex.EncodeToString(sum[:4])
	}

	destination := filepath.Clean(strings.TrimSpace(*out))
	if destination == "" {
		return fmt.Errorf("output path required")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	flag := os.O_CREATE | os.O_EXCL | os.O_WRONLY
	if *force {
		flag = os.O_CREATE | os.O_TRUNC | os.O_WRONLY
	}
	// #nosec G304 -- output key path is an explicit CLI/operator destination.
	f, err := os.OpenFile(destination, flag, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("refusing to overwrite %s (pass --force to replace)", destination)
		}
		return fmt.Errorf("create private key file: %w", err)
	}
	payload := packPrivateKeyRecord{
		KeyID:      keyID,
		Algorithm:  signing.AlgorithmEd25519,
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
	}
	if _, err := fmt.Fprintln(f, packPrivateKeyHeader); err != nil {
		_ = f.Close()
		return err
	}
	if err := yaml.NewEncoder(f).Encode(payload); err != nil {
		_ = f.Close()
		return fmt.Errorf("write private key: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(destination, 0o600); err != nil {
			return fmt.Errorf("chmod 0600 %s: %w", destination, err)
		}
	}

	fmt.Printf("kid: %s\n", keyID)
	fmt.Printf("algorithm: %s\n", signing.AlgorithmEd25519)
	fmt.Printf("public_key_b64: %s\n", base64.StdEncoding.EncodeToString(pub))
	fmt.Fprintf(os.Stderr, "wrote private key: %s\n", destination)
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "warning: restrict ACLs on the private key file manually on Windows")
	}
	return nil
}

// runPackSign reads a pack.yaml manifest, walks the declared
// resources, and writes the signed envelope next to pack.yaml.
func runPackSign(args []string) error {
	fs := newFlagSet("pack sign")
	keyPath := fs.String("key", "", "private key file (defaults to $CORDUM_PACK_SIGNING_KEY or "+defaultPackSigningKeyPath()+")")
	keyIDFlag := fs.String("key-id", "", "override the key id stored in the envelope")
	outPath := fs.String("out", "", "signature file output (defaults to <packRoot>/pack.yaml.sig)")
	asJSON := fs.Bool("json", false, "write the envelope as JSON instead of YAML")
	fs.ParseArgs(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("pack root path required")
	}
	root := fs.Arg(0)
	record, err := loadPackPrivateKeyFromFlags(*keyPath)
	if err != nil {
		return err
	}
	keyID := strings.TrimSpace(*keyIDFlag)
	if keyID == "" {
		keyID = record.KeyID
	}

	manifest, err := signing.BuildManifest(root)
	if err != nil {
		return err
	}
	signed, err := signing.SignManifest(manifest, record.priv, keyID)
	if err != nil {
		return err
	}

	sigPath := strings.TrimSpace(*outPath)
	if sigPath == "" {
		sigPath = filepath.Join(root, defaultPackSigFile)
	}
	data, err := encodeSignedManifest(signed, *asJSON)
	if err != nil {
		return err
	}
	if err := os.WriteFile(sigPath, data, 0o600); err != nil {
		return fmt.Errorf("write signature file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "signed %s@%s with kid %s -> %s\n",
		signed.Manifest.PackID, signed.Manifest.PackVersion, signed.Signature.KeyID, sigPath)
	return nil
}

// runPackVerifySignature verifies a pack.yaml.sig envelope against a
// trusted public-key directory or single public-key file.
func runPackVerifySignature(args []string) error {
	fs := newFlagSet("pack verify-signature")
	trustedDir := fs.String("trusted-keys", "", "directory of trusted *.pub files (one per kid)")
	keyFile := fs.String("key", "", "single trusted public-key file (kid inferred from filename)")
	sigPath := fs.String("sig", "", "signature file (default <packRoot>/pack.yaml.sig)")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("pack root path required")
	}
	root := fs.Arg(0)

	envelopePath := strings.TrimSpace(*sigPath)
	if envelopePath == "" {
		envelopePath = filepath.Join(root, defaultPackSigFile)
	}
	envelope, err := loadSignedManifest(envelopePath)
	if err != nil {
		return err
	}

	keyring, err := loadPackKeyring(strings.TrimSpace(*trustedDir), strings.TrimSpace(*keyFile))
	if err != nil {
		return err
	}

	if err := signing.VerifyPack(root, envelope, keyring); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	fmt.Printf("ok: %s@%s signed by kid=%s verified\n",
		envelope.Manifest.PackID, envelope.Manifest.PackVersion, envelope.Signature.KeyID)
	return nil
}

// runPackExportKey reads a pack signing private key and prints the
// public key + kid + algorithm as JSON for registry submission.
func runPackExportKey(args []string) error {
	fs := newFlagSet("pack export-key")
	keyPath := fs.String("key", "", "private key file (defaults to $CORDUM_PACK_SIGNING_KEY or "+defaultPackSigningKeyPath()+")")
	fs.ParseArgs(args)
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	record, err := loadPackPrivateKeyFromFlags(*keyPath)
	if err != nil {
		return err
	}
	pub := record.priv.Public().(ed25519.PublicKey)
	out := struct {
		KID          string `json:"kid"`
		Algorithm    string `json:"algorithm"`
		PublicKeyB64 string `json:"public_key_b64"`
	}{
		KID:          record.KeyID,
		Algorithm:    signing.AlgorithmEd25519,
		PublicKeyB64: base64.StdEncoding.EncodeToString(pub),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// ---------------------------------------------------------------------
// Envelope encoding / decoding
// ---------------------------------------------------------------------

func encodeSignedManifest(signed signing.SignedManifest, asJSON bool) ([]byte, error) {
	if asJSON {
		return json.MarshalIndent(signed, "", "  ")
	}
	var buf []byte
	buf = append(buf, []byte("# cordum pack signature\n")...)
	body, err := yaml.Marshal(signed)
	if err != nil {
		return nil, err
	}
	return append(buf, body...), nil
}

func loadSignedManifest(path string) (signing.SignedManifest, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- signature path is an explicit CLI/operator input.
	if err != nil {
		return signing.SignedManifest{}, fmt.Errorf("read signature file %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(raw))
	var envelope signing.SignedManifest
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return signing.SignedManifest{}, fmt.Errorf("decode json envelope: %w", err)
		}
		return envelope, nil
	}
	if err := yaml.Unmarshal(raw, &envelope); err != nil {
		return signing.SignedManifest{}, fmt.Errorf("decode yaml envelope: %w", err)
	}
	return envelope, nil
}

// ---------------------------------------------------------------------
// Private-key + keyring loading
// ---------------------------------------------------------------------

const packPrivateKeyHeader = "# cordum pack signing key - KEEP PRIVATE (0600)"

type packPrivateKeyRecord struct {
	KeyID      string `yaml:"kid"`
	Algorithm  string `yaml:"algorithm"`
	PrivateKey string `yaml:"private_key_b64"`
	priv       ed25519.PrivateKey
}

func defaultPackSigningKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".cordum", "pack-signing.key")
	}
	return filepath.Join(home, ".cordum", "pack-signing.key")
}

func loadPackPrivateKeyFromFlags(flagPath string) (packPrivateKeyRecord, error) {
	if p := strings.TrimSpace(flagPath); p != "" {
		return loadPackPrivateKeyFromFile(p)
	}
	if raw := strings.TrimSpace(os.Getenv(envPackSigningKey)); raw != "" {
		return decodePackPrivateKeyBytes([]byte(raw))
	}
	return loadPackPrivateKeyFromFile(defaultPackSigningKeyPath())
}

func loadPackPrivateKeyFromFile(path string) (packPrivateKeyRecord, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- private key path is an explicit CLI/operator input or cordumctl default.
	if err != nil {
		return packPrivateKeyRecord{}, fmt.Errorf("read private key %s: %w", path, err)
	}
	return decodePackPrivateKeyBytes(raw)
}

func decodePackPrivateKeyBytes(raw []byte) (packPrivateKeyRecord, error) {
	// Skip a leading header comment line if present.
	body := raw
	if idx := skipCommentHeader(body); idx > 0 {
		body = body[idx:]
	}
	var record packPrivateKeyRecord
	if err := yaml.Unmarshal(body, &record); err != nil {
		return packPrivateKeyRecord{}, fmt.Errorf("decode private key: %w", err)
	}
	record.KeyID = strings.TrimSpace(record.KeyID)
	if record.Algorithm == "" {
		record.Algorithm = signing.AlgorithmEd25519
	}
	if record.Algorithm != signing.AlgorithmEd25519 {
		return packPrivateKeyRecord{}, fmt.Errorf("%w: %s", signing.ErrUnsupportedAlgorithm, record.Algorithm)
	}
	if record.KeyID == "" {
		return packPrivateKeyRecord{}, fmt.Errorf("%w: key file missing kid", signing.ErrInvalidKey)
	}
	priv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(record.PrivateKey))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return packPrivateKeyRecord{}, fmt.Errorf("%w: private key must be a %d-byte base64 blob", signing.ErrInvalidKey, ed25519.PrivateKeySize)
	}
	record.priv = ed25519.PrivateKey(priv)
	return record, nil
}

func skipCommentHeader(raw []byte) int {
	i := 0
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t') {
		i++
	}
	if i >= len(raw) || raw[i] != '#' {
		return 0
	}
	// Skip to end of line.
	for i < len(raw) && raw[i] != '\n' {
		i++
	}
	if i < len(raw) && raw[i] == '\n' {
		i++
	}
	return i
}

func loadPackKeyring(trustedDir, keyFile string) (map[string]ed25519.PublicKey, error) {
	out := map[string]ed25519.PublicKey{}
	if keyFile != "" {
		kid, pub, err := loadPackPublicKeyFile(keyFile)
		if err != nil {
			return nil, err
		}
		out[kid] = pub
	}
	if trustedDir != "" {
		err := filepath.WalkDir(trustedDir, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(strings.ToLower(p), ".pub") {
				return nil
			}
			kid, pub, err := loadPackPublicKeyFile(p)
			if err != nil {
				return fmt.Errorf("trusted key %s: %w", p, err)
			}
			out[kid] = pub
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no trusted keys loaded (pass --key or --trusted-keys)")
	}
	return out, nil
}

type packPublicKeyRecord struct {
	KeyID        string `yaml:"kid" json:"kid"`
	Algorithm    string `yaml:"algorithm" json:"algorithm"`
	PublicKeyB64 string `yaml:"public_key_b64" json:"public_key_b64"`
}

func loadPackPublicKeyFile(path string) (string, ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- public key path is an explicit CLI/operator input.
	if err != nil {
		return "", nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	var record packPublicKeyRecord
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal(raw, &record); err != nil {
			return "", nil, err
		}
	} else if err := yaml.Unmarshal(raw, &record); err != nil {
		return "", nil, err
	}
	record.KeyID = strings.TrimSpace(record.KeyID)
	if record.KeyID == "" {
		record.KeyID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if record.Algorithm != "" && record.Algorithm != signing.AlgorithmEd25519 {
		return "", nil, fmt.Errorf("%w: %s", signing.ErrUnsupportedAlgorithm, record.Algorithm)
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(record.PublicKeyB64))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return "", nil, fmt.Errorf("%w: public key must be a %d-byte base64 blob", signing.ErrInvalidKey, ed25519.PublicKeySize)
	}
	return record.KeyID, ed25519.PublicKey(pub), nil
}
