//go:build handshakeinterop

package handshakeinterop

import (
	"os"
	"sort"
	"strings"
)

var childEnvironmentAllowlist = []string{
	"COMSPEC", "LANG", "LC_ALL", "PATH", "PATHEXT", "SYSTEMDRIVE",
	"SYSTEMROOT", "TEMP", "TMP", "TMPDIR", "TZ", "WINDIR",
}

func clientProcessEnvironment(overrides []string) []string {
	values := make(map[string]string, len(childEnvironmentAllowlist)+len(overrides)+4)
	for _, name := range childEnvironmentAllowlist {
		if value, exists := lookupEnvironmentFold(name); exists {
			values[strings.ToUpper(name)] = name + "=" + value
		}
	}
	for _, item := range []string{"NODE_PATH=", "PYTHONHOME=", "PYTHONPATH=", "PYTHONNOUSERSITE=1"} {
		putEnvironment(values, item)
	}
	for _, item := range overrides {
		putEnvironment(values, item)
	}
	keys := make([]string, 0, len(values))
	for name := range values {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, name := range keys {
		environment = append(environment, values[name])
	}
	return environment
}

func lookupEnvironmentFold(want string) (string, bool) {
	for _, item := range os.Environ() {
		name, value, found := strings.Cut(item, "=")
		if found && strings.EqualFold(name, want) {
			return value, true
		}
	}
	return "", false
}

func putEnvironment(values map[string]string, item string) {
	name, _, found := strings.Cut(item, "=")
	if found && strings.TrimSpace(name) != "" {
		values[strings.ToUpper(name)] = item
	}
}
