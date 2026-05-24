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

// supportsAgentdListenerInheritance reports whether the launcher hands its
// reserved loopback listener to cordum-agentd across exec (true) or releases the
// port and lets agentd re-bind it (false).
//
// Windows returns false. Duplicating the loopback socket handle into the agentd
// child across this exec.Cmd path is unreliable in this build: agentd ends up
// re-binding the 127.0.0.1:<port> the launcher reserved and fails with "Only one
// usage of each socket address (protocol/network address/port) is normally
// permitted", so cordum-agentd exits before becoming ready (reproduces across
// random ports). Returning false routes the Windows launcher through
// reserveLoopbackHookURLLegacy (net.Listen -> Close -> pass URL only, no
// CORDUM_AGENTD_LISTENER_HANDLE handoff); agentd then binds the freed port fresh
// and starts reliably.
//
// Tradeoff: this reintroduces the small reserve->bind race window (a same-user
// loopback process could grab the port between Close and agentd's bind) that the
// held-listener handoff was designed to close. Accepted as identical to the
// pre-inheritance behavior and to the Unix legacy fallback (loopback, same-user).
// Real Windows socket-handle passing (Option B) stays available via the dormant
// inheritance helpers below if it is ever revisited.
func supportsAgentdListenerInheritance() bool {
	return false
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
