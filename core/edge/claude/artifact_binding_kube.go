package claude

import (
	"errors"
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

// artifactIsKubectl reports the kubectl program by its exact name. A word with
// '=' is an assignment or a value (KUBECONFIG=dir/kubectl), not the program.
func artifactIsKubectl(field string) bool {
	name := filepath.Base(field)
	return !strings.Contains(field, "=") && (name == "kubectl" || name == "kubectl.exe")
}

// artifactKubectlFlags maps each kubectl flag the binding models, by long
// name, to what it takes: a target value, file values or nothing.
var artifactKubectlFlags = map[string]string{
	"context": "target", "namespace": "target", "kubeconfig": "target",
	"filename": "files", "from-file": "files", "from-env-file": "files",
	"recursive": "switch",
}

var artifactKubectlShorthands = map[byte]string{'n': "namespace", 'f': "filename", 'R': "recursive"}

// artifactKubectlArgs reads kubectl's arguments as pflag v1.0.9 does
// (parseArgs, parseLongArg, parseSingleShortArg). It returns the last value of
// each modeled flag, by long name, every path the file flags name (pflag
// appends each occurrence), and whether every argument is recognized: only
// modeled flags, a value for each value flag, file values
// artifactKubectlFiles accepts, and the subcommand apply with no other
// positional, create or get. An unmodeled flag is read as a switch, so the
// target flags after it still parse. kubectl picks its subcommand before it
// parses flags (cobra stripFlags), and that lookup reads the word after a bare
// -R or --recursive as their value and the word after -Rf or -Rn as a
// command. So a switch before the subcommand, and a cluster whose later letter
// takes the next word, are unrecognized.
func artifactKubectlArgs(args []string) (map[string]string, []string, bool) {
	values, ok := map[string]string{}, true
	var positionals, files []string
	set := func(name, value string, written bool) {
		kind, known := artifactKubectlFlags[name]
		switch {
		case !known || kind == "switch":
			ok = ok && known
		case !written && len(args) == 0:
			ok = false
		default:
			if !written {
				value, args = args[0], args[1:]
			}
			values[name] = value
			if kind == "files" {
				paths, valid := artifactKubectlFiles(name, value)
				files, ok = append(files, paths...), ok && valid
			}
		}
	}
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch {
		case arg == "--":
			args, ok = nil, false // what follows is not modeled, and not kubectl's flags
		case len(positionals) == 0 && (arg == "-R" || arg == "--recursive"):
			ok = false
		case len(arg) < 2 || arg[0] != '-':
			positionals = append(positionals, arg)
		case arg[1] == '-':
			name, value, written := strings.Cut(arg[2:], "=")
			set(name, value, written)
		default:
			ok = artifactKubectlCluster(arg, set) && ok
		}
	}
	return values, files, ok && artifactKubectlSubcommand(positionals)
}

// artifactKubectlFiles returns the paths in a file flag's value. pflag splits
// the value with encoding/csv, which on plain bytes is a split at ','.
// --from-file reads each item as kubectl's ParseFileSource does: a path, or
// key=path with one '=' that neither starts nor ends the item. It reports
// false for an item kubectl rejects and for - (stdin).
func artifactKubectlFiles(name, value string) ([]string, bool) {
	paths := strings.Split(value, ",")
	for i, item := range paths {
		if key, path, found := strings.Cut(item, "="); found && name == "from-file" {
			if key == "" || path == "" || strings.Contains(path, "=") {
				return nil, false
			}
			paths[i] = path
		}
		if paths[i] == "-" {
			return nil, false
		}
	}
	return paths, true
}

// artifactKubectlCluster reads a cluster of short flags as
// parseSingleShortArg does: a value letter takes the rest of the cluster,
// after an optional '=', or else the next argument. It reports false when a
// value letter after the first takes the next argument.
func artifactKubectlCluster(arg string, set func(name, value string, written bool)) bool {
	for i := 1; i < len(arg); i++ {
		name, rest := artifactKubectlShorthands[arg[i]], arg[i+1:]
		switch kind := artifactKubectlFlags[name]; {
		case len(rest) > 1 && rest[0] == '=':
			set(name, rest[1:], true)
		case kind == "switch" || kind == "":
			set(name, "", false)
			continue
		case rest != "":
			set(name, rest, true)
		default:
			set(name, "", false)
			return i == 1
		}
		return true
	}
	return true
}

// artifactKubectlSubcommand reports a modeled subcommand. kubectl runs a
// kubectl-create-* plugin for a create subcommand it does not have.
func artifactKubectlSubcommand(positionals []string) bool {
	switch {
	case len(positionals) == 0:
		return false
	case positionals[0] == "apply":
		return len(positionals) == 1
	case positionals[0] == "create":
		return len(positionals) == 1 || positionals[1] == "configmap" || positionals[1] == "secret"
	}
	return positionals[0] == "get"
}

// artifactKubeTarget returns the context and namespace kubectl will use, and
// an empty context when it cannot tell. --kubeconfig, --context and
// --namespace/-n on the command line win, as does a KUBECONFIG= assignment
// before kubectl; otherwise the kubeconfig files decide, merged as kubectl
// merges them. --kubeconfig names one file and KUBECONFIG a list; with
// neither, kubectl reads .kube/config under HOME, joined as client-go joins
// it. Without HOME it reads .kube/config under its cwd, or on Windows under
// another home, so the target is unknown.
func artifactKubeTarget(fields []string, cwd string, env map[string]string) (string, string) {
	paths := filepath.SplitList(envValue(env, "KUBECONFIG"))
	at := slices.IndexFunc(fields, artifactIsKubectl)
	for _, field := range fields[:at] {
		if value, ok := strings.CutPrefix(field, "KUBECONFIG="); ok {
			paths = filepath.SplitList(value)
		}
	}
	flags, _, _ := artifactKubectlArgs(fields[at+1:])
	if flags["kubeconfig"] != "" {
		paths = []string{flags["kubeconfig"]} // an empty --kubeconfig is not an explicit path
	}
	if home := envValue(env, "HOME"); len(paths) == 0 && home != "" {
		paths = []string{filepath.Join(home, ".kube", "config")}
	}
	kubeContext, namespace := flags["context"], flags["namespace"]
	current, namespaces := artifactKubeconfigs(paths, cwd)
	if namespaces == nil {
		return "", "" // no file, or one kubectl may read differently, can change the target even with --context
	}
	if kubeContext == "" {
		kubeContext = current
	}
	if namespace == "" {
		namespace = namespaces[kubeContext]
	}
	if namespace == "" && envValue(env, "KUBERNETES_SERVICE_HOST") != "" {
		return "", "" // in a pod kubectl takes POD_NAMESPACE or the service account's namespace
	}
	if namespace == "" {
		namespace = "default"
	}
	return kubeContext, namespace
}

// artifactKubeconfigs reads the kubeconfig files kubectl would read. The first
// file that sets current-context wins, and so does the first definition of
// each context. Each file must be a readable regular file holding plain YAML
// that yaml.v3 decodes, or no YAML document (an empty config); otherwise it
// returns no namespaces and the target is unknown. kubectl skips a missing
// file, but client-go first copies an older .kube/.kubeconfig into a missing
// .kube/config. kubectl's YAML reader (yaml v2) keeps the last value of a
// repeated key, which yaml.v3 rejects, and applies a merge key after the keys
// before it, where yaml.v3 keeps those keys.
func artifactKubeconfigs(paths []string, cwd string) (string, map[string]string) {
	if len(paths) == 0 {
		return "", nil
	}
	current, namespaces := "", map[string]string{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		path = artifactPath(cwd, path)
		if artifactSpecialPath(path) {
			return "", nil
		}
		file, reason := artifactOpen(path)
		if reason != "" {
			return "", nil
		}
		var node yaml.Node
		err := yaml.NewDecoder(io.LimitReader(file, artifactMaxFileBytes)).Decode(&node)
		_ = file.Close()
		if errors.Is(err, io.EOF) {
			continue
		}
		var config artifactKubeconfig
		if err != nil || !artifactPlainYAML(&node) || node.Decode(&config) != nil {
			return "", nil
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

// artifactPlainYAML reports YAML with no alias, merge key or explicit tag,
// which yaml v2 and yaml.v3 can read differently.
func artifactPlainYAML(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode || node.Tag == "!!merge" || node.Style&yaml.TaggedStyle != 0 {
		return false
	}
	return !slices.ContainsFunc(node.Content, func(child *yaml.Node) bool { return !artifactPlainYAML(child) })
}
