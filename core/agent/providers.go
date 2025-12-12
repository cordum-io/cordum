package agent

import "context"

// ModelProvider defines a minimal interface for text generation.
// Callers construct prompts; providers return the generated response.
type ModelProvider interface {
	Generate(ctx context.Context, prompt string) (string, error)
}
