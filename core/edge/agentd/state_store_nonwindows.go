//go:build !windows

package agentd

func applyStatePathPermissions(string) error {
	return nil
}

func verifyStateDirPermissions(string) error {
	return nil
}
