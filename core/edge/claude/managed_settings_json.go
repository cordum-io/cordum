package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Go merges duplicate map values and folds struct field names, whereas Claude
// uses exact JSON keys. Reject ambiguity before decoding the typed document.
// Unknown unrelated settings remain accepted. Hook objects implement only the
// generated command contract; unknown execution modifiers must fail closed.
func validateManagedSettingsJSON(data []byte) error {
	if int64(len(data)) > maxManagedSettingsFileBytes {
		return fmt.Errorf("managed-settings.json exceeds %d bytes", maxManagedSettingsFileBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := scanManagedSettingsJSON(dec, "settings", 0); err != nil {
		return fmt.Errorf("parse managed-settings.json: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("parse managed-settings.json: exactly one JSON value required")
	}
	return nil
}

func scanManagedSettingsJSON(dec *json.Decoder, scope string, depth int) error {
	if depth > 128 {
		return fmt.Errorf("JSON nesting exceeds 128 levels")
	}
	token, err := dec.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON token")
	}
	switch token {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for dec.More() {
			keyToken, err := dec.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return fmt.Errorf("invalid JSON object field")
			}
			if seen[key] {
				return fmt.Errorf("duplicate JSON object field")
			}
			seen[key] = true
			known := false
			for _, canonical := range managedSettingsJSONFields(scope) {
				if key == canonical {
					known = true
				}
				if key != canonical && strings.EqualFold(key, canonical) {
					return fmt.Errorf("noncanonical JSON field spelling; use %s", canonical)
				}
			}
			if (scope == "hook-set" || scope == "hook-command") && !known {
				return fmt.Errorf("unsupported field in managed hook object")
			}
			child := ""
			switch {
			case scope == "settings" && key == "allowedMcpServers":
				child = "mcp-list"
			case scope == "settings" && key == "hooks":
				child = "hook-events"
			case scope == "hook-events":
				child = "hook-set-list"
			case scope == "hook-set" && key == "hooks":
				child = "hook-command-list"
			}
			if err := scanManagedSettingsJSON(dec, child, depth+1); err != nil {
				return err
			}
		}
		if end, err := dec.Token(); err != nil || end != json.Delim('}') {
			return fmt.Errorf("invalid JSON object")
		}
	case json.Delim('['):
		child := ""
		switch scope {
		case "mcp-list":
			child = "mcp-entry"
		case "hook-set-list":
			child = "hook-set"
		case "hook-command-list":
			child = "hook-command"
		}
		for dec.More() {
			if err := scanManagedSettingsJSON(dec, child, depth+1); err != nil {
				return err
			}
		}
		if end, err := dec.Token(); err != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid JSON array")
		}
	}
	return nil
}

func managedSettingsJSONFields(scope string) []string {
	switch scope {
	case "settings":
		return []string{"$schema", "allowManagedHooksOnly", "allowManagedMcpServersOnly", "allowedHttpHookUrls", "allowedMcpServers", "managedMcpServers", "allowClaudeInChromeWithManagedMcp", "disableBypassPermissionsMode", "forceRemoteSettingsRefresh", "apiKeyHelper", "env", "hooks"}
	case "mcp-entry":
		return []string{"serverUrl", "serverName", "serverCommand"}
	case "hook-set":
		return []string{"matcher", "hooks"}
	case "hook-command":
		return []string{"type", "command", "timeout", "onFailure"}
	}
	return nil
}
