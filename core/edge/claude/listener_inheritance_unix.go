//go:build !windows

package claude

import (
	"fmt"
	"net"
	"os"
	"os/exec"
)

func supportsAgentdListenerInheritance() bool {
	return true
}

func listenerFileForInheritance(ln net.Listener) (*os.File, string, error) {
	tcp, ok := ln.(*net.TCPListener)
	if !ok {
		return nil, "", fmt.Errorf("agentd listener inheritance requires TCP listener, got %T", ln)
	}
	file, err := tcp.File()
	if err != nil {
		return nil, "", fmt.Errorf("prepare inherited agentd listener: %w", err)
	}
	return file, "3", nil
}

func configureInheritedListener(cmd *exec.Cmd, file *os.File) {
	cmd.ExtraFiles = append(cmd.ExtraFiles, file)
}

func closeInheritedListenerFile(file *os.File) {
	_ = file.Close()
}
