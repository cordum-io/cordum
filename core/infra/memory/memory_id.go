package memory

import "strings"

// NormalizeMemoryID cleans user-supplied memory identifiers.
// It strips the mem: prefix to keep IDs consistent with mem:<id>:* keys.
func NormalizeMemoryID(raw string) string {
	id := strings.TrimSpace(raw)
	if id == "" {
		return ""
	}
	id = strings.TrimPrefix(id, "mem:")
	return strings.TrimSpace(id)
}
