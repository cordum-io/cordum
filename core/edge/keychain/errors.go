package keychain

import "errors"

// ErrKeyringNotFound is returned by Keyring.Get when the requested key has no
// stored value in the backend, and by LoadSecret when neither the keychain
// nor the env fallback (dev mode only) yielded a value.
var ErrKeyringNotFound = errors.New("keychain: secret not found")

// ErrKeyringUnavailable is returned when the OS-native keychain backend is
// unreachable — Linux without a libsecret/D-Bus session, macOS keychain
// locked or ACL denied, Windows Credential Manager service not running. In
// strict bootstrap mode this is a fatal failure; agentd refuses to start.
var ErrKeyringUnavailable = errors.New("keychain: backend unavailable")

// ErrKeyringPermissionDenied is returned when the backend is reachable but
// the calling process is not permitted to read the requested entry. On macOS
// this surfaces when the Keychain ACL has not been granted; on Linux when
// the Secret Service collection is locked; on Windows when the credential
// is bound to a different user SID.
var ErrKeyringPermissionDenied = errors.New("keychain: permission denied")
