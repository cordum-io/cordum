package buildinfo

import (
	"fmt"
	"log"
)

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Info returns a single-line build summary.
func Info() string {
	return fmt.Sprintf("version=%s commit=%s date=%s", Version, Commit, Date)
}

// Log writes the build summary with the service name.
func Log(service string) {
	log.Printf("%s %s", service, Info())
}
