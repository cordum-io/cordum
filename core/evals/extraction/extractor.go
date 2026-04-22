package extraction

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrNoIncidents = errors.New("no incidents matched the extraction request")

type TimeoutError struct {
	Result ExtractionResult
	Err    error
}

func (e *TimeoutError) Error() string {
	if e == nil || e.Err == nil {
		return "extraction timed out"
	}
	return e.Err.Error()
}

func (e *TimeoutError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Extractor runs incident-to-dataset extraction requests.
type Extractor interface {
	Run(ctx context.Context, req ExtractionRequest) (ExtractionResult, error)
	Validate(req ExtractionRequest) (ExtractionRequest, error)
}

type Service struct {
	deps ExtractionDeps
}

// New returns an extractor backed by the supplied dependencies.
func New(deps ExtractionDeps) *Service {
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{deps: deps}
}

func (s *Service) Validate(req ExtractionRequest) (ExtractionRequest, error) {
	if s == nil {
		return ExtractionRequest{}, fmt.Errorf("extractor is nil")
	}
	return req.Normalize(s.deps.Now())
}

func (s *Service) Run(ctx context.Context, req ExtractionRequest) (ExtractionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return ExtractionResult{}, fmt.Errorf("extractor is nil")
	}
	if s.deps.DecisionLog == nil {
		return ExtractionResult{}, fmt.Errorf("decision log store is required")
	}
	if !req.DryRun && s.deps.EvalDatasets == nil {
		return ExtractionResult{}, fmt.Errorf("eval dataset store is required")
	}

	normalized, err := s.Validate(req)
	if err != nil {
		return ExtractionResult{}, err
	}

	preview, err := s.preview(ctx, normalized)
	if err != nil {
		return ExtractionResult{}, err
	}
	if preview.ScannedDecisions == 0 && !normalized.DryRun {
		return preview, ErrNoIncidents
	}
	return preview, nil
}
