// Package handshakemode owns strict parsing of the shared handshake rollout mode.
package handshakemode

import (
	"fmt"
	"strings"
)

const EnvName = "CORDUM_SDK_HANDSHAKE"

type Mode string

const (
	Off     Mode = "off"
	Warn    Mode = "warn"
	Enforce Mode = "enforce"
)

// Parse requires an explicit, recognized rollout mode.
func Parse(raw string) (Mode, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%s must be explicitly set to off, warn, or enforce", EnvName)
	}
	mode := Mode(strings.ToLower(trimmed))
	switch mode {
	case Off, Warn, Enforce:
		return mode, nil
	default:
		return "", fmt.Errorf("unrecognized %s value %q; valid values: off, warn, enforce", EnvName, trimmed)
	}
}
