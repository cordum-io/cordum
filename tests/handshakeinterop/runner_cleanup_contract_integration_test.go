//go:build handshakeinterop

package handshakeinterop

import (
	"os"
	"strings"
	"testing"
)

func TestLinuxRunnerDelegatesToOwnedTempCleanup(t *testing.T) {
	data, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatalf("read run.sh: %v", err)
	}
	text := string(data)
	for _, required := range []string{
		`. "$cordum_root/tests/handshakeinterop/cleanup.sh"`,
		`remove_owned_temp "$temp_root" "$temp_base"`,
		"trap cleanup EXIT",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("run.sh missing cleanup contract %q", required)
		}
	}
}
