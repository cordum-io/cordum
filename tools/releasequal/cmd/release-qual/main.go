// release-qual writes, assembles, validates and checks release qualification
// records, and starts a publish command only after every named artifact digest
// has been checked against a record.
package main

import (
	"io"
	"os"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int { return 2 }
