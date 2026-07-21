package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

const (
	maxHandshakePrivateKeyBytes = 16 * 1024
	maxHandshakePublicKeyBytes  = 4 * 1024
)

var handshakeBootIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func validateHandshakeBootID(name, value string) error {
	if !handshakeBootIDPattern.MatchString(value) {
		return fmt.Errorf("%s must be 1-128 ASCII letters, digits, '.', '_', ':', or '-'", name)
	}
	return nil
}

func loadPinnedHandshakeKeyPair(privatePath, publicPath string) (*ecdsa.PrivateKey, []byte, string, error) {
	privatePEM, err := readBoundedHandshakeKey(privatePath, maxHandshakePrivateKeyBytes)
	if err != nil {
		return nil, nil, "", fmt.Errorf("load scheduler handshake private key: %w", err)
	}
	defer clearBytes(privatePEM)
	privateKey, err := parseHandshakePrivateKey(privatePEM)
	if err != nil {
		return nil, nil, "", err
	}
	publicPEM, err := readBoundedHandshakeKey(publicPath, maxHandshakePublicKeyBytes)
	if err != nil {
		return nil, nil, "", fmt.Errorf("load scheduler handshake public key: %w", err)
	}
	publicKey, publicDER, err := parseHandshakePublicKey(publicPEM)
	if err != nil {
		return nil, nil, "", err
	}
	if !privateKey.PublicKey.Equal(publicKey) {
		return nil, nil, "", fmt.Errorf("scheduler handshake public key does not match private key")
	}
	canonical := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	fingerprint := sha256.Sum256(publicDER)
	return privateKey, canonical, hex.EncodeToString(fingerprint[:]), nil
}

func readBoundedHandshakeKey(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- administrator-configured key path is the intended input.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("key file exceeds %d bytes", limit)
	}
	return data, nil
}

func parseHandshakePrivateKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, rest := pem.Decode(data)
	if block == nil || strings.TrimSpace(string(rest)) != "" {
		return nil, fmt.Errorf("scheduler handshake private key must contain one PEM block")
	}
	var key *ecdsa.PrivateKey
	switch block.Type {
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse scheduler handshake private key: %w", err)
		}
		var ok bool
		key, ok = parsed.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("scheduler handshake private key must be ECDSA P-256")
		}
	case "EC PRIVATE KEY":
		var err error
		key, err = x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse scheduler handshake private key: %w", err)
		}
	default:
		return nil, fmt.Errorf("scheduler handshake private key must be PKCS#8 or EC PEM")
	}
	if !validP256PrivateKey(key) {
		return nil, fmt.Errorf("scheduler handshake private key must be ECDSA P-256")
	}
	return key, nil
}

func parseHandshakePublicKey(data []byte) (*ecdsa.PublicKey, []byte, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" || strings.TrimSpace(string(rest)) != "" {
		return nil, nil, fmt.Errorf("scheduler handshake public key must contain one SPKI PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse scheduler handshake public key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok || !validP256PublicKey(key) {
		return nil, nil, fmt.Errorf("scheduler handshake public key must be ECDSA P-256")
	}
	return key, block.Bytes, nil
}

func validP256PrivateKey(key *ecdsa.PrivateKey) bool {
	// key.D is read-only here: a nil guard before ECDH(), which panics
	// (rather than erroring) on a zero-value scalar. Bytes()/ECDH() give no
	// nil-safe alternative to guard with.
	if key == nil || key.D == nil || !validP256PublicKey(&key.PublicKey) { //nolint:staticcheck // SA1019: nil guard, see comment above
		return false
	}
	privateKey, err := key.ECDH()
	if err != nil {
		return false
	}
	publicKey, err := key.PublicKey.ECDH()
	return err == nil && bytes.Equal(privateKey.PublicKey().Bytes(), publicKey.Bytes())
}

func validP256PublicKey(key *ecdsa.PublicKey) bool {
	// key.X/key.Y are read-only here: a nil guard before ECDH(), which
	// panics (rather than erroring) on zero-value coordinates. Bytes()/ECDH()
	// give no nil-safe alternative to guard with.
	if key == nil || key.Curve != elliptic.P256() || key.X == nil || key.Y == nil { //nolint:staticcheck // SA1019: nil guard, see comment above
		return false
	}
	_, err := key.ECDH()
	return err == nil
}

func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
