package store

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

// TenantMemoryID returns the storage id of memoryID for tenant: memoryID
// itself when it already reads "<tenant>/<name>", else "<tenant>/<memoryID>".
// It returns false for an empty tenant or memoryID, a tenant containing "/",
// and a memoryID whose "/"-separated owner segment is not tenant.
func TenantMemoryID(tenant, memoryID string) (string, bool) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" || strings.Contains(tenant, "/") || strings.TrimSpace(memoryID) == "" {
		return "", false
	}
	owner, name, qualified := strings.Cut(memoryID, "/")
	if !qualified {
		return tenant + "/" + memoryID, true
	}
	if owner != tenant || name == "" {
		return "", false
	}
	return memoryID, true
}
