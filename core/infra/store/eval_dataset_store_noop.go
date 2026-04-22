package store

import (
	"context"

	"github.com/cordum/cordum/core/model"
)

// NoopEvalDatasetStore implements model.EvalDatasetStore with inert,
// predictable stubs. It exists so callers that never touch the eval
// dataset API (unit tests for sibling handlers, lightweight gateway
// fixtures) do not have to stand up miniredis just to pass a non-nil
// interface value.
//
// Semantics chosen to minimize surprise: every read returns
// ErrEvalDatasetNotFound, List returns an empty page, Delete is a no-op
// success, and Create fails with a clear error so test authors don't
// accidentally rely on in-memory persistence from the noop and then see
// different behavior on a real Redis store.
type NoopEvalDatasetStore struct{}

var _ model.EvalDatasetStore = (*NoopEvalDatasetStore)(nil)

// NewNoopEvalDatasetStore returns a ready-to-use noop implementation.
func NewNoopEvalDatasetStore() *NoopEvalDatasetStore {
	return &NoopEvalDatasetStore{}
}

func (*NoopEvalDatasetStore) CreateEvalDataset(context.Context, model.EvalDataset) (model.EvalDataset, error) {
	return model.EvalDataset{}, errNoopCreate
}

func (*NoopEvalDatasetStore) GetEvalDataset(context.Context, string, string) (model.EvalDataset, error) {
	return model.EvalDataset{}, ErrEvalDatasetNotFound
}

func (*NoopEvalDatasetStore) ListEvalDatasets(context.Context, string, model.EvalDatasetFilter, string, int) (model.EvalDatasetPage, error) {
	return model.EvalDatasetPage{Items: []model.EvalDataset{}}, nil
}

func (*NoopEvalDatasetStore) DeleteEvalDataset(context.Context, string, string) error {
	return nil
}

func (*NoopEvalDatasetStore) GetEvalDatasetByNameVersion(context.Context, string, string, int) (model.EvalDataset, error) {
	return model.EvalDataset{}, ErrEvalDatasetNotFound
}

func (*NoopEvalDatasetStore) ListEvalDatasetVersions(context.Context, string, string) ([]model.EvalDataset, error) {
	return []model.EvalDataset{}, nil
}

// errNoopCreate is the sentinel returned by NoopEvalDatasetStore.Create
// to make it obvious when a test has stumbled into needing the real
// store but was wired with the noop. It intentionally does NOT wrap
// ErrEvalDatasetNotFound or ErrEvalDatasetVersionExists because those
// are distinct outcomes that test assertions might key on.
var errNoopCreate = noopErr("noop eval dataset store: Create is not supported — wire a real store to exercise this path")

type noopErr string

func (e noopErr) Error() string { return string(e) }
