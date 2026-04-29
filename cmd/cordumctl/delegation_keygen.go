package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordum/cordum/core/auth/delegation"
)

func runDelegationCmd(args []string) {
	if len(args) < 1 {
		delegationUsage()
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "keygen":
		err = runDelegationKeygenE(args[1:])
	default:
		delegationUsage()
		os.Exit(1)
	}
	if err != nil {
		fail(err.Error())
	}
}

func delegationUsage() {
	fmt.Fprintln(os.Stderr, `Usage: cordumctl delegation <command>

Commands:
  keygen [--out ./delegation-ed25519.pem]          Generate a delegation Ed25519 keypair`)
}

func runDelegationKeygenE(args []string) error {
	fs := newFlagSet("delegation keygen")
	out := fs.String("out", "./delegation-ed25519.pem", "private key output path")
	kid := fs.String("kid", "", "active key id to advertise (default dlg-1)")
	fs.ParseArgs(args)

	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	signingKey, err := delegation.GenerateSigningKey(*kid)
	if err != nil {
		return err
	}
	privatePEM, err := delegation.EncodePrivateKeyPEM(signingKey.PrivateKey)
	if err != nil {
		return err
	}
	publicKey, err := delegation.EncodePublicKeyBase64(signingKey.PublicKey())
	if err != nil {
		return err
	}

	destination := filepath.Clean(strings.TrimSpace(*out))
	if destination == "" {
		return fmt.Errorf("output path required")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create private key file: %w", err)
	}
	if _, err := file.Write(privatePEM); err != nil {
		_ = file.Close()
		return fmt.Errorf("write private key: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close private key file: %w", err)
	}

	fmt.Println(publicKey)
	return nil
}
