package claude

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

var artifactInterpreters = []string{"sh", "bash", "zsh", "dash", "python", "node", "perl", "ruby"}

// artifactIsInterpreter reports a shell or script interpreter by its exact
// program name, after a path, a version suffix (python3.12) or .exe.
func artifactIsInterpreter(field string) bool {
	name := strings.TrimSuffix(filepath.Base(field), ".exe")
	return slices.Contains(artifactInterpreters, strings.TrimRight(name, "0123456789."))
}

// artifactRecognized returns "" when the binding models the program and every
// argument of a command, and the unbound reason otherwise. KUBECONFIG=
// assignments may come first. kubectl is modeled when artifactKubectlArgs
// recognizes its arguments and every file they name is in the digest. A listed
// interpreter is modeled when its first argument is a script file in the
// digest, named without a '..' element: node resolves its script by name
// (path.resolve), where the hook follows the kernel. The arguments after the
// script belong to it. With no argument, or an option first (bash also reads
// +c as -c), the interpreter runs code the command line does not name as a
// file.
func artifactRecognized(fields []string, cwd string, inputs *artifactInputs) string {
	for len(fields) > 0 && strings.HasPrefix(fields[0], "KUBECONFIG=") {
		fields = fields[1:]
	}
	hashed := func(name string) bool {
		_, ok := inputs.Files[artifactPath(cwd, name)]
		return ok
	}
	switch {
	case len(fields) == 0 || strings.Contains(fields[0], "="):
		// An assignment before the program: the shell runs the next word.
		return "unrecognized"
	case artifactIsKubectl(fields[0]):
		_, files, ok := artifactKubectlArgs(fields[1:])
		for _, file := range files {
			ok = ok && hashed(file)
		}
		if ok {
			return ""
		}
	case artifactIsInterpreter(fields[0]):
		if len(fields) == 1 || strings.IndexByte("-+", fields[1][0]) >= 0 {
			return "inline-code"
		}
		// node inspect starts a debugger, not a script named inspect.
		if hashed(fields[1]) && fields[1] != "inspect" && !slices.Contains(strings.Split(fields[1], "/"), "..") {
			return ""
		}
	}
	return "unrecognized"
}

// artifactNames returns each string in a field that may name a file, read the
// way command-line parsers read values: the field, the text after each '=',
// ',', ':' or '@' (key=path, a,b, src:dst, @path), and every value a short
// option or a cluster of them can carry attached (-fFILE, -RfFILE), each also
// cut at the next ',' and at the next ':' (a comma-list item may hold a ':').
// On Windows a ':' ends a drive letter (D:/dir), so no name starts after it.
// A string that names no file costs one stat; a named file missing from the
// digest would keep an approval valid after the file changed.
func artifactNames(field string) []string {
	separators := "=,:@"
	if runtime.GOOS == "windows" {
		separators = "=,@"
	}
	starts := []int{0}
	for i := range len(field) {
		if strings.IndexByte(separators, field[i]) >= 0 {
			starts = append(starts, i+1)
		}
	}
	if cluster, ok := strings.CutPrefix(field, "-"); ok {
		for i := range artifactFlagRun(cluster) {
			starts = append(starts, i+2)
		}
	}
	names := make([]string, 0, 3*len(starts))
	for _, start := range starts {
		rest := field[start:]
		for _, sep := range []string{",", ":"} {
			if before, _, found := strings.Cut(rest, sep); found {
				names = append(names, before)
			}
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

// artifactZshEquals reports a word zsh rewrites when EQUALS is set, as it is
// by default: '=cmd' becomes the path of cmd, at the start of a word and after
// the '=' or a ':' of an assignment (or, under MAGIC_EQUAL_SUBST, of any
// name=value argument).
func artifactZshEquals(field string) bool {
	return strings.HasPrefix(field, "=") || strings.Contains(field, "==") || strings.Contains(field, ":=")
}

// artifactWindowsDevice reports a name Windows opens as a device, with any
// extension (CON, con.yaml, NUL, COM1): the command reads the device.
func artifactWindowsDevice(name string) bool {
	stem, _, _ := strings.Cut(filepath.Base(name), ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	port, found := strings.CutPrefix(stem, "COM")
	if !found {
		port, found = strings.CutPrefix(stem, "LPT")
	}
	return found && (len(port) == 1 && '0' <= port[0] && port[0] <= '9' || port == "¹" || port == "²" || port == "³")
}

// artifactWindowsUnreadable reports a Windows name the binding cannot read as
// the command will. Windows resolves a rooted (/dir) or drive-relative (D:dir)
// name against a current drive the binding does not know, and Git Bash
// rewrites a rooted name through its mount table first. A device name is not
// a file, even when stat of cwd/name says missing. A ':' after the volume name
// is an NTFS stream to Go and another character to Git Bash's MSYS programs.
// None of these is stat'ed.
func artifactWindowsUnreadable(name string) bool {
	volume := filepath.VolumeName(name)
	return artifactWindowsDevice(name) || strings.Contains(name[len(volume):], ":") ||
		!filepath.IsAbs(name) && (name[0] == '/' || volume != "")
}

// artifactTrailingDot reports a name with an element that ends in '.', other
// than . and ..: Windows opens it without the dot, while Git Bash's MSYS
// programs open it as written. A bound command holds no '\'.
func artifactTrailingDot(name string) bool {
	return slices.ContainsFunc(strings.Split(name, "/"), func(element string) bool {
		return element != "." && element != ".." && strings.HasSuffix(element, ".")
	})
}

// artifactSpecialPath checks each hop before following it: resolving a procfs
// magic link first would hide the reader-dependent name behind an ordinary file.
func artifactSpecialPath(path string) bool {
	if artifactNamespacePath(path) {
		return true
	}
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return true
		}
		path = artifactPath(wd, path)
	}
	var cur string
	var names []string
	if !artifactQueuePath(path, &cur, &names) {
		return true
	}
	for links := 0; len(names) > 0; {
		name := names[0]
		names = names[1:]
		switch name {
		case ".":
			continue
		case "..":
			cur = filepath.Dir(cur) // cur is link-free, so this is the kernel's parent
			continue
		}
		next := filepath.Join(cur, name)
		if artifactPseudoPath(next) {
			return true
		}
		info, err := os.Lstat(next)
		if err != nil {
			return false // addFile's stat decides missing or unreadable names
		}
		if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
			cur = next
			continue
		}
		// Windows junctions report ModeIrregular; Readlink resolves them too.
		target, err := os.Readlink(next)
		links++
		if err != nil || links > 40 || !artifactQueuePath(target, &cur, &names) {
			return true
		}
	}
	return false
}

// artifactQueuePath prepends a written path or link target without cleaning '..'.
// Absolute targets restart at their root; relative ones keep the link's parent.
func artifactQueuePath(path string, cur *string, names *[]string) bool {
	if artifactNamespacePath(path) || runtime.GOOS == "windows" && artifactWindowsDevice(path) {
		return false
	}
	if filepath.IsAbs(path) {
		vol := filepath.VolumeName(path)
		*cur, path = vol+string(filepath.Separator), path[len(vol):]
	}
	parts := strings.FieldsFunc(path, func(r rune) bool { return r < 0x80 && os.IsPathSeparator(byte(r)) })
	*names = append(parts, *names...)
	return true
}

func artifactPseudoPath(path string) bool {
	if runtime.GOOS != "windows" {
		for _, root := range []string{"/proc", "/dev", "/sys"} {
			if path == root || strings.HasPrefix(path, root+"/") {
				return true
			}
		}
	}
	return false
}

// artifactNamespacePath rejects Windows device, NT and network namespaces.
func artifactNamespacePath(path string) bool {
	return runtime.GOOS == "windows" && (len(path) >= 2 && os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) || strings.HasPrefix(path, `\??\`))
}
