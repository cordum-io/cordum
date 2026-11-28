// qualpin writes and validates pin manifests; it runs no qualification.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cordum/cordum/tools/qualpin"
	"github.com/cordum/cordum/tools/releasequal"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "qualpin:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 2 && args[0] == "validate" {
		data, err := read(args[1])
		if err != nil {
			return err
		}
		_, err = qualpin.Parse(data)
		return err
	}
	if len(args) > 0 && args[0] == "write" {
		return write(args[1:])
	}
	return errors.New("usage: qualpin write -record RECORD -pins PINS | qualpin validate MANIFEST")
}

func write(args []string) error {
	fs := flag.NewFlagSet("write", flag.ContinueOnError)
	recordPath, pinsPath := fs.String("record", "", "releasequal record"), fs.String("pins", "", "qualification pins JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *recordPath == "" || *pinsPath == "" {
		return errors.New("write requires -record and -pins, with no positional arguments")
	}
	data, err := read(*recordPath)
	if err != nil {
		return err
	}
	record, err := releasequal.Parse(data)
	if err != nil {
		return err
	}
	data, err = read(*pinsPath)
	if err != nil {
		return err
	}
	if !json.Valid(data) {
		return errors.New("invalid pins JSON")
	}
	var pins qualpin.Pins
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pins); err != nil {
		return fmt.Errorf("pins: %w", err)
	}
	m, err := qualpin.Assemble(*record, pins)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(m)
}

func read(path string) ([]byte, error) {
	const limit = 4 << 20
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // Read-only close; no buffered writes to flush.
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	return data, nil
}
