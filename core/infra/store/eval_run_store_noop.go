package store

import (
	"context"

	"github.com/cordum/cordum/core/evals/runner"
)

// NoopEvalRunStore implements the EvalRunStore surface with inert
// stubs so gateway unit tests for sibling handlers don't have to stand
// up miniredis just to pass a non-nil store value. Reads return
// ErrEvalRunNotFound, List returns an empty page, Delete is a silent
// success. Create fails loudly so tests don't accidentally rely on
// in-memory persistence from the noop.
type NoopEvalRunStore struct{}

// NewNoopEvalRunStore returns a ready-to-use noop store.
func NewNoopEvalRunStore() *NoopEvalRunStore { return &NoopEvalRunStore{} }

func (*NoopEvalRunStore) CreateRun(context.Context, runner.RunResult) error {
	return noopErr("noop eval run store: CreateRun is not supported — wire a real store")
}

func (*NoopEvalRunStore) GetRun(context.Context, string, string) (runner.RunResult, error) {
	return runner.RunResult{}, ErrEvalRunNotFound
}

func (*NoopEvalRunStore) ListRuns(context.Context, string, RunFilter, string, int) (RunPage, error) {
	return RunPage{Items: []runner.RunResult{}}, nil
}

func (*NoopEvalRunStore) DeleteRun(context.Context, string, string) error { return nil }

func (*NoopEvalRunStore) GCExpired(context.Context, string) (int, error) { return 0, nil }
