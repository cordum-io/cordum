//go:build !windows

package claude

import "io/fs"

// artifactSystemFile reports a Windows SYSTEM file, which exists only on
// Windows.
func artifactSystemFile(fs.FileInfo) bool { return false }
