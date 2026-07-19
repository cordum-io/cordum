//go:build handshakeinterop

package handshakeinterop

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
)

func (h *interopHarness) writeKeys() {
	h.t.Helper()
	for _, identity := range h.server.identities {
		encoded, err := x509.MarshalPKCS8PrivateKey(identity.privateKey)
		if err != nil {
			h.t.Fatalf("marshal worker key: %v", err)
		}
		writePrivateFile(h.t, h.workerKeyPath(identity), "PRIVATE KEY", encoded)
	}
	publicKey, err := x509.MarshalPKIXPublicKey(&h.server.schedulerKey.PublicKey)
	if err != nil {
		h.t.Fatalf("marshal scheduler key: %v", err)
	}
	writePrivateFile(h.t, filepath.Join(h.tempDir, "scheduler-public.pem"), "PUBLIC KEY", publicKey)
}

func (h *interopHarness) workerKeyPath(identity *interopIdentity) string {
	return filepath.Join(h.tempDir, identity.keyID+"-private.pem")
}

func writePrivateFile(t fatalHelper, path, blockType string, data []byte) {
	t.Helper()
	encoded := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: data})
	if encoded == nil {
		t.Fatalf("encode %s", blockType)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
}

type fatalHelper interface {
	Helper()
	Fatalf(string, ...interface{})
}
