package listenerhandoff

import (
	"runtime"
	"strings"
)

const (
	// FDEnv is the internal launcher -> agentd listener handoff key used on
	// Unix platforms where exec.Cmd.ExtraFiles exposes the first inherited file
	// as fd 3 in the child.
	FDEnv = "CORDUM_AGENTD_LISTENER_FD"
	// HandleEnv is the internal launcher -> agentd listener handoff key used on
	// Windows where a duplicated socket handle is made inheritable and passed in
	// SysProcAttr.AdditionalInheritedHandles.
	HandleEnv = "CORDUM_AGENTD_LISTENER_HANDLE"
)

func EnvKeyForCurrentPlatform() string {
	return EnvKeyForGOOS(runtime.GOOS)
}

func EnvKeyForGOOS(goos string) string {
	if goos == "windows" {
		return HandleEnv
	}
	return FDEnv
}

func ValueForCurrentPlatform(env map[string]string) (string, string) {
	return ValueForGOOS(env, runtime.GOOS)
}

func ValueForGOOS(env map[string]string, goos string) (string, string) {
	key := EnvKeyForGOOS(goos)
	return key, strings.TrimSpace(env[key])
}
