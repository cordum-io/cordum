package claude

import (
	"path/filepath"
	"slices"
	"strings"
)

var artifactInterpreters = []string{"sh", "bash", "zsh", "dash", "python", "node", "perl", "ruby"}

// artifactIsInterpreter reports a shell or script interpreter under any
// spelling of its program: a path, a version suffix (python3.12) or .exe.
func artifactIsInterpreter(field string) bool {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(field)), ".exe")
	return slices.Contains(artifactInterpreters, strings.TrimRight(name, "0123456789."))
}

// artifactIsCodeOption reports -c, -e or -m, alone or in a cluster of short
// options (bash -ec ls, python3 -mdeploy): the interpreter then runs code or
// a module that the command line does not name as a file.
func artifactIsCodeOption(field string) bool {
	cluster, ok := strings.CutPrefix(field, "-")
	return ok && strings.ContainsAny(cluster[:artifactFlagRun(cluster)], "cem")
}

// artifactNames returns each string in a field that may name a file, read the
// way flag parsers such as kubectl's read it: the field, the text after each
// '=' or ',', and every value a short option or a cluster of them can carry
// attached (-fFILE, -RfFILE), each also cut at the next ','. A string that
// names no file costs one stat; a named file missing from the digest would
// keep an approval valid after the file changed.
func artifactNames(field string) []string {
	starts := []int{0}
	for i := range len(field) {
		if field[i] == '=' || field[i] == ',' {
			starts = append(starts, i+1)
		}
	}
	if cluster, ok := strings.CutPrefix(field, "-"); ok {
		for i := range artifactFlagRun(cluster) {
			starts = append(starts, i+2)
		}
	}
	names := make([]string, 0, 2*len(starts))
	for _, start := range starts {
		rest := field[start:]
		if head, _, cut := strings.Cut(rest, ","); cut {
			names = append(names, head)
		}
		names = append(names, rest)
	}
	return names
}

// artifactFlagRun returns the length of the run of short-option letters, ASCII
// letters and digits, at the start of s.
func artifactFlagRun(s string) int {
	n := 0
	for n < len(s) && ('a' <= s[n] && s[n] <= 'z' || 'A' <= s[n] && s[n] <= 'Z' || '0' <= s[n] && s[n] <= '9') {
		n++
	}
	return n
}
