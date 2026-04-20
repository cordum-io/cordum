package store

import "errors"

// ErrEvalDatasetVersionExists is returned by EvalDatasetStore.Create when a
// dataset with the same (tenant, name, version) already exists. The gateway
// handler maps this to HTTP 409 Conflict. The immutability rail forbids
// overwriting an existing version, so this error is expected to be a
// routine outcome of concurrent create-with-same-version requests and is
// not a bug on its own.
var ErrEvalDatasetVersionExists = errors.New("eval dataset version already exists")

// ErrEvalDatasetNotFound is returned by EvalDatasetStore.Get and
// EvalDatasetStore.GetByNameVersion when no dataset matches. The gateway
// handler maps this to HTTP 404.
var ErrEvalDatasetNotFound = errors.New("eval dataset not found")
