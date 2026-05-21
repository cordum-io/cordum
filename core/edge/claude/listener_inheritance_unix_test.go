//go:build !windows

package claude

import (
	"net"
	"os"
	"os/exec"
	"testing"
)

func TestListenerInheritanceUnixUsesExtraFilesFD(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	file, envKey, envValue, err := listenerFileForInheritance(ln)
	if err != nil {
		t.Fatalf("listenerFileForInheritance returned error: %v", err)
	}
	defer closeInheritedListenerFile(file)
	if envKey != "CORDUM_AGENTD_LISTENER_FD" {
		t.Fatalf("Unix listener env key = %q, want CORDUM_AGENTD_LISTENER_FD", envKey)
	}
	if envValue != "3" {
		t.Fatalf("Unix listener env value = %q, want fd 3", envValue)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	configureInheritedListener(cmd, file)
	if len(cmd.ExtraFiles) != 1 || cmd.ExtraFiles[0] != file {
		t.Fatalf("ExtraFiles = %#v, want inherited listener file only", cmd.ExtraFiles)
	}
}
