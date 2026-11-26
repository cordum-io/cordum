package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/cordum/cordum/tools/certgen"
)

func runGenerateCertsCmd(args []string) {
	fs := flag.NewFlagSet("generate-certs", flag.ExitOnError)
	dir := fs.String("dir", "./certs", "output directory for generated certificates")
	force := fs.Bool("force", false, "overwrite existing certificate files")
	days := fs.Int("days", 365, "certificate validity period in days")
	var sans labelFlags
	fs.Var(&sans, "san", "extra DNS name or IP for the server certificate, added to the defaults (repeatable)")
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}

	fmt.Printf("Generating TLS certificates in %s ...\n", *dir)
	if err := certgen.GenerateAll(certgen.Options{
		BaseDir: *dir,
		Days:    *days,
		Force:   *force,
		SANs:    sans,
	}); err != nil {
		fail(fmt.Sprintf("certificate generation failed: %v", err))
	}
	fmt.Println("  ca/ca.crt      CA certificate")
	fmt.Println("  ca/ca.key      CA private key")
	fmt.Println("  server/tls.crt Server certificate")
	fmt.Println("  server/tls.key Server private key")
	fmt.Println("  client/tls.crt Client certificate")
	fmt.Println("  client/tls.key Client private key")
	if len(sans) > 0 {
		fmt.Println("Extra server SANs: " + strings.Join(sans, ", "))
	}
	fmt.Println("Done.")
}
