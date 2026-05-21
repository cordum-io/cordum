//go:build windows

package claude

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/cordum/cordum/core/edge/listenerhandoff"
)

func supportsAgentdListenerInheritance() bool {
	return true
}

func listenerFileForInheritance(ln net.Listener) (*os.File, string, string, error) {
	tcp, ok := ln.(*net.TCPListener)
	if !ok {
		return nil, "", "", fmt.Errorf("agentd listener inheritance requires TCP listener, got %T", ln)
	}
	file, err := tcp.File()
	if err != nil {
		return nil, "", "", fmt.Errorf("prepare inherited agentd listener: %w", err)
	}
	handle := syscall.Handle(file.Fd())
	if err := syscall.SetHandleInformation(handle, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
		_ = file.Close()
		return nil, "", "", fmt.Errorf("mark inherited agentd listener handle: %w", err)
	}
	return file, listenerhandoff.HandleEnv, strconv.FormatUint(uint64(file.Fd()), 10), nil
}

func configureInheritedListener(cmd *exec.Cmd, file *os.File) {
	handle := syscall.Handle(file.Fd())
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, handle)
}

func closeInheritedListenerFile(file *os.File) {
	handle := syscall.Handle(file.Fd())
	_ = syscall.SetHandleInformation(handle, syscall.HANDLE_FLAG_INHERIT, 0)
	_ = file.Close()
}
