package claude

import (
	"io"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// artifactKubeconfig holds the kubeconfig fields that pick a kubectl target.
type artifactKubeconfig struct {
	CurrentContext string `yaml:"current-context"`
	Contexts       []struct {
		Name    string `yaml:"name"`
		Context struct {
			Namespace string `yaml:"namespace"`
		} `yaml:"context"`
	} `yaml:"contexts"`
}

func artifactIsKubectl(field string) bool {
	return filepath.Base(field) == "kubectl"
}

// artifactKubeTarget returns the context and namespace kubectl will use, and
// an empty context when it cannot tell. --kubeconfig, --context and
// --namespace/-n on the command line win, as does a KUBECONFIG= assignment
// before kubectl; otherwise the kubeconfig files decide, merged as kubectl
// merges them.
func artifactKubeTarget(fields []string, cwd string, env map[string]string) (string, string) {
	kubeconfig := envValue(env, "KUBECONFIG")
	at := slices.IndexFunc(fields, artifactIsKubectl)
	for _, field := range fields[:at] {
		if value, ok := strings.CutPrefix(field, "KUBECONFIG="); ok {
			kubeconfig = value
		}
	}
	var kubeContext, namespace string
	args := fields[at+1:]
	for i, arg := range args {
		flag, value, inline := strings.Cut(arg, "=")
		if !inline && i+1 < len(args) {
			value = args[i+1]
		}
		switch flag {
		case "--kubeconfig":
			kubeconfig = value
		case "--context":
			kubeContext = value
		case "--namespace", "-n":
			namespace = value
		}
	}
	current, namespaces := artifactKubeconfigs(kubeconfig, cwd, env)
	if kubeContext == "" {
		kubeContext = current
	}
	if namespace == "" {
		namespace = namespaces[kubeContext]
	}
	if namespace == "" {
		namespace = "default"
	}
	return kubeContext, namespace
}

// artifactKubeconfigs reads the kubeconfig files kubectl would read: the
// KUBECONFIG list, or ~/.kube/config. The first file that sets current-context
// wins, and so does the first definition of each context.
func artifactKubeconfigs(kubeconfig, cwd string, env map[string]string) (string, map[string]string) {
	paths := filepath.SplitList(kubeconfig)
	if len(paths) == 0 {
		home := envValue(env, "HOME")
		if home == "" {
			home = envValue(env, "USERPROFILE")
		}
		if home != "" {
			paths = []string{filepath.Join(home, ".kube", "config")}
		}
	}
	current, namespaces := "", map[string]string{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		file, reason := artifactOpen(artifactPath(cwd, path))
		if reason != "" {
			continue
		}
		var config artifactKubeconfig
		err := yaml.NewDecoder(io.LimitReader(file, artifactMaxFileBytes)).Decode(&config)
		_ = file.Close()
		if err != nil {
			continue
		}
		if current == "" {
			current = config.CurrentContext
		}
		for _, entry := range config.Contexts {
			if _, seen := namespaces[entry.Name]; !seen {
				namespaces[entry.Name] = entry.Context.Namespace
			}
		}
	}
	return current, namespaces
}
