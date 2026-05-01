package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cordum/cordum/core/edge/claude"
)

// writeEdgeSettingsOutput writes generated Claude settings to stdout when the
// output path is "-" and otherwise creates a new file without overwriting an
// existing operator-managed settings file.
func writeEdgeSettingsOutput(stdout io.Writer, outputPath string, payload []byte) (err error) {
	if stdout == nil {
		stdout = io.Discard
	}
	if outputPath == "-" || outputPath == "" {
		if _, err := stdout.Write(payload); err != nil {
			return fmt.Errorf("write settings stdout: %w", err)
		}
		if len(payload) == 0 || payload[len(payload)-1] != '\n' {
			if _, err := io.WriteString(stdout, "\n"); err != nil {
				return fmt.Errorf("write settings stdout newline: %w", err)
			}
		}
		return nil
	}
	clean := filepath.Clean(outputPath)
	if _, err := os.Stat(clean); err == nil {
		return fmt.Errorf("refusing to overwrite existing settings file %s", clean)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat settings output %s: %w", clean, err)
	}
	f, err := os.OpenFile(clean, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create settings output %s: %w", clean, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close settings output %s: %w", clean, cerr)
		}
	}()
	if _, err := f.Write(payload); err != nil {
		return fmt.Errorf("write settings output %s: %w", clean, err)
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		if _, err := f.Write([]byte("\n")); err != nil {
			return fmt.Errorf("write settings output newline %s: %w", clean, err)
		}
	}
	return nil
}

func renderEdgeSettingsPreview(payload []byte) string {
	return claude.RenderSettingsPreview(payload, "settings-output")
}
