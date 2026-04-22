package extraction

import (
	"context"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

type fakeDecisionLogStore struct {
	err       error
	pages     map[model.SafetyDecision][]model.DecisionPage
	page      model.DecisionPage
	calls     map[model.SafetyDecision]int
	queries   []model.DecisionQuery
	lastQuery model.DecisionQuery
}

func (f *fakeDecisionLogStore) AppendDecision(context.Context, model.DecisionLogRecord) error {
	return nil
}

func (f *fakeDecisionLogStore) QueryDecisions(_ context.Context, query model.DecisionQuery) (model.DecisionPage, error) {
	f.lastQuery = query
	f.queries = append(f.queries, query)
	if f.err != nil {
		return model.DecisionPage{}, f.err
	}
	if f.pages == nil {
		return f.page, nil
	}
	if f.calls == nil {
		f.calls = make(map[model.SafetyDecision]int)
	}
	idx := f.calls[query.Verdict]
	f.calls[query.Verdict] = idx + 1
	pages := f.pages[query.Verdict]
	if idx >= len(pages) {
		return model.DecisionPage{}, nil
	}
	return pages[idx], nil
}

type fakeJobStore struct {
	requests map[string]*pb.JobRequest
	errors   map[string]error
	calls    []string
}

func (f *fakeJobStore) GetJobRequest(_ context.Context, jobID string) (*pb.JobRequest, error) {
	f.calls = append(f.calls, jobID)
	if f.errors != nil {
		if err, ok := f.errors[jobID]; ok {
			return nil, err
		}
	}
	if f.requests == nil {
		return nil, redis.Nil
	}
	req, ok := f.requests[jobID]
	if !ok {
		return nil, redis.Nil
	}
	return req, nil
}

type fakeEvalDatasetStore struct {
	versions     []model.EvalDataset
	listErr      error
	createErrors []error
	created      []model.EvalDataset
	nextID       string
}

func (f *fakeEvalDatasetStore) CreateEvalDataset(_ context.Context, dataset model.EvalDataset) (model.EvalDataset, error) {
	f.created = append(f.created, dataset)
	if len(f.createErrors) > 0 {
		err := f.createErrors[0]
		f.createErrors = f.createErrors[1:]
		if err != nil {
			return model.EvalDataset{}, err
		}
	}
	created := dataset
	if created.ID == "" {
		if f.nextID != "" {
			created.ID = f.nextID
		} else {
			created.ID = "dataset-created"
		}
	}
	created.EntryCount = len(created.Entries)
	return created, nil
}

func (f *fakeEvalDatasetStore) GetEvalDataset(context.Context, string, string) (model.EvalDataset, error) {
	return model.EvalDataset{}, nil
}

func (f *fakeEvalDatasetStore) ListEvalDatasets(context.Context, string, model.EvalDatasetFilter, string, int) (model.EvalDatasetPage, error) {
	return model.EvalDatasetPage{}, nil
}

func (f *fakeEvalDatasetStore) DeleteEvalDataset(context.Context, string, string) error { return nil }

func (f *fakeEvalDatasetStore) GetEvalDatasetByNameVersion(context.Context, string, string, int) (model.EvalDataset, error) {
	return model.EvalDataset{}, nil
}

func (f *fakeEvalDatasetStore) ListEvalDatasetVersions(context.Context, string, string) ([]model.EvalDataset, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]model.EvalDataset(nil), f.versions...), nil
}
