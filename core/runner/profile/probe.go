package profile

// HostProber reports what this host can enforce, from read-only checks only:
// it writes no file and creates no process, namespace or cgroup. Root is where
// proc, sys and runtime paths are read; empty means "/", so the zero value
// probes the real host.
type HostProber struct{ Root string }
