// release-qual writes, assembles, validates and checks release qualification
// records, and starts a publish command only after every named artifact digest
// has been checked against a record. It carries no release rules of its own:
// every content rule is the tools/releasequal library's.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordum/cordum/tools/releasequal"
)

const (
	exitOK      = 0
	exitInvalid = 1 // entry, assemble, validate: content the library refuses
	exitUsage   = 2 // usage error or unreadable input
	exitRefused = 3 // check, publish: something is not the qualified artifact

	// maxJSONBytes bounds a record or fragment read into memory.
	maxJSONBytes = 4 << 20
)

const usageText = `usage: release-qual <command> [flags]

commands:
  entry    --kind image --name N --ref R --digest sha256:HEX | --kind binary --file PATH...
           [--gate ID=pass|fail ... --log URL] [--exception ID=REASON ...] --out FILE
  assemble --out FILE --ref REF --run-url URL --repository OWNER/NAME --sha SHA [--dry-run] FRAGMENT...
  validate RECORD
  check    --record RECORD [--artifact NAME=DIGEST ...] [--file PATH ...]
  publish  --record RECORD [--artifact NAME=DIGEST ...] [--file PATH ...] -- CMD [ARGS...]

Flags come before positional arguments. Write a boolean as --dry-run or
--dry-run=false, never as a separate word.

exit codes: 0 ok; 1 invalid content (entry, assemble, validate);
            2 usage error or unreadable input; 3 refused (check, publish)`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	handlers := map[string]func([]string, io.Writer, io.Writer) int{
		"entry": entry, "assemble": assemble, "validate": validate,
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return usage(stderr, "")
	}
	handler, ok := handlers[args[0]]
	if !ok {
		return usage(stderr, fmt.Sprintf("unknown command %q", args[0]))
	}
	return handler(args[1:], stdout, stderr)
}

func usage(stderr io.Writer, problem string) int {
	if problem != "" {
		fmt.Fprintf(stderr, "release-qual: %s\n", problem)
	}
	fmt.Fprintln(stderr, usageText)
	return exitUsage
}

func fail(stderr io.Writer, code int, format string, a ...any) int {
	fmt.Fprintf(stderr, "release-qual: "+format+"\n", a...)
	return code
}

// multiFlag collects a repeatable flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("release-qual "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// splitPair splits ID=VALUE at the first '='; VALUE may itself contain '='.
func splitPair(flagName, s string) (id, value string, err error) {
	id, value, ok := strings.Cut(s, "=")
	if !ok || id == "" {
		return "", "", fmt.Errorf("--%s %q: want ID=VALUE with a non-empty ID", flagName, s)
	}
	return id, value, nil
}

// fileDigest streams the file through sha256.
func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// readJSONFile reads a record or fragment, refusing anything over maxJSONBytes.
func readJSONFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxJSONBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxJSONBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxJSONBytes)
	}
	return data, nil
}

func writeJSON(path string, v any, stderr io.Writer) int {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail(stderr, exitInvalid, "encode %s: %v", path, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fail(stderr, exitUsage, "%v", err)
	}
	return exitOK
}

func entryArtifacts(kind, name, ref, digest string, files []string) ([]releasequal.Artifact, error) {
	switch kind {
	case releasequal.KindImage:
		if name == "" || ref == "" || digest == "" || len(files) > 0 {
			return nil, errors.New("--kind image needs --name, --ref and --digest, and takes no --file")
		}
		return []releasequal.Artifact{{Kind: kind, Name: name, Ref: ref, Digest: digest}}, nil
	case releasequal.KindBinary:
		if len(files) == 0 || name != "" || ref != "" || digest != "" {
			return nil, errors.New("--kind binary needs at least one --file, and takes no --name, --ref or --digest")
		}
		var artifacts []releasequal.Artifact
		for _, file := range files {
			d, err := fileDigest(file)
			if err != nil {
				return nil, err
			}
			base := filepath.Base(file)
			artifacts = append(artifacts, releasequal.Artifact{Kind: kind, Name: base, Ref: base, Digest: d})
		}
		return artifacts, nil
	}
	return nil, fmt.Errorf("--kind must be %s or %s, got %q", releasequal.KindImage, releasequal.KindBinary, kind)
}

// entryFragment applies the gates and exceptions to every artifact in the fragment.
func entryFragment(artifacts []releasequal.Artifact, gates, exceptions []string, log string) (releasequal.Fragment, error) {
	frag := releasequal.Fragment{Artifacts: artifacts, Gates: []releasequal.Gate{}, Exceptions: []releasequal.Exception{}}
	if len(gates) > 0 && log == "" {
		return frag, errors.New("--gate needs --log")
	}
	var names []string
	for _, a := range artifacts {
		names = append(names, a.Name)
	}
	for _, g := range gates {
		id, result, err := splitPair("gate", g)
		if err != nil {
			return frag, err
		}
		for _, a := range artifacts {
			frag.Gates = append(frag.Gates, releasequal.Gate{ID: id, Artifact: a.Name, Digest: a.Digest, Result: result, Log: log})
		}
	}
	for _, e := range exceptions {
		id, reason, err := splitPair("exception", e)
		if err != nil {
			return frag, err
		}
		frag.Exceptions = append(frag.Exceptions, releasequal.Exception{ID: id, Artifacts: slices.Clone(names), Reason: reason})
	}
	return frag, nil
}

func entry(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("entry", stderr)
	kind, name := fs.String("kind", "", "image or binary"), fs.String("name", "", "image name")
	ref, digest := fs.String("ref", "", "image reference"), fs.String("digest", "", "image digest, sha256:HEX")
	log, out := fs.String("log", "", "URL of the gate log"), fs.String("out", "", "fragment file to write")
	var files, gates, exceptions multiFlag
	fs.Var(&files, "file", "binary file to digest (repeatable)")
	fs.Var(&gates, "gate", "ID=pass|fail (repeatable)")
	fs.Var(&exceptions, "exception", "ID=REASON (repeatable)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 || *out == "" {
		return usage(stderr, "entry: --out is required and no positional argument is accepted")
	}
	artifacts, err := entryArtifacts(*kind, *name, *ref, *digest, files)
	if err != nil {
		return fail(stderr, exitUsage, "entry: %v", err)
	}
	frag, err := entryFragment(artifacts, gates, exceptions, *log)
	if err != nil {
		return fail(stderr, exitUsage, "entry: %v", err)
	}
	// A fragment is only written when it would assemble: check it under a fixed
	// placeholder header, so the library alone decides what is valid.
	header := releasequal.Release{Ref: "refs/heads/release-qual-entry-check", DryRun: true, RunURL: "https://localhost/release-qual-entry-check"}
	source := releasequal.Source{Repository: "release-qual/entry-check", SHA: strings.Repeat("0", 40)}
	if _, err := releasequal.Assemble(header, source, frag); err != nil {
		return fail(stderr, exitInvalid, "entry: %v", err)
	}
	return writeJSON(*out, frag, stderr)
}

func assemble(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("assemble", stderr)
	out, ref := fs.String("out", "", "record file to write"), fs.String("ref", "", "release ref")
	runURL, repo := fs.String("run-url", "", "URL of the release run"), fs.String("repository", "", "OWNER/NAME")
	sha, dryRun := fs.String("sha", "", "source commit SHA"), fs.Bool("dry-run", false, "mark the record as a dry run")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *out == "" || *ref == "" || *runURL == "" || *repo == "" || *sha == "" || fs.NArg() == 0 {
		return usage(stderr, "assemble: --out, --ref, --run-url, --repository, --sha and at least one FRAGMENT are required")
	}
	var fragments []releasequal.Fragment
	for _, path := range fs.Args() {
		data, err := readJSONFile(path)
		if err != nil {
			return fail(stderr, exitUsage, "assemble: %v", err)
		}
		frag, err := releasequal.ParseFragment(data)
		if err != nil {
			return fail(stderr, exitInvalid, "assemble: fragment %s: %v", path, err)
		}
		fragments = append(fragments, frag)
	}
	record, err := releasequal.Assemble(releasequal.Release{Ref: *ref, DryRun: *dryRun, RunURL: *runURL},
		releasequal.Source{Repository: *repo, SHA: *sha}, fragments...)
	if err != nil {
		return fail(stderr, exitInvalid, "assemble: %v", err)
	}
	return writeJSON(*out, record, stderr)
}

func validate(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("validate", stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		return usage(stderr, "validate: exactly one RECORD is required")
	}
	data, err := readJSONFile(fs.Arg(0))
	if err != nil {
		return fail(stderr, exitUsage, "validate: %v", err)
	}
	record, err := releasequal.Parse(data)
	if err != nil {
		return fail(stderr, exitInvalid, "validate: %v", err)
	}
	images := 0
	for _, a := range record.Artifacts {
		if a.Kind == releasequal.KindImage {
			images++
		}
	}
	fmt.Fprintf(stdout, "VALID %d artifacts (%d image, %d binary), %d gates, %d exceptions, source %s, ref %s, dry_run %t\n",
		len(record.Artifacts), images, len(record.Artifacts)-images, len(record.Gates), len(record.Exceptions),
		record.Source.SHA, record.Release.Ref, record.Release.DryRun)
	return exitOK
}
