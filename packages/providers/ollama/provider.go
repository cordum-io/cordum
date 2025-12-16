package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Provider struct {
	url    string
	model  string
	client *http.Client
	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

type request struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	Stream  bool   `json:"stream"`
	Options any    `json:"options,omitempty"`
}

type response struct {
	Response string `json:"response"`
}

// NewFromEnv builds an Ollama provider using OLLAMA_URL/OLLAMA_MODEL or defaults.
func NewFromEnv() *Provider {
	return &Provider{
		url:    envOrDefault("OLLAMA_URL", "http://ollama:11434"),
		model:  envOrDefault("OLLAMA_MODEL", "llama3"),
		client: &http.Client{Timeout: 150 * time.Second},
	}
}

// Generate implements the model provider contract.
func (p *Provider) Generate(ctx context.Context, prompt string) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("empty prompt")
	}
	if p.isCircuitOpen() {
		return "", fmt.Errorf("ollama circuit open")
	}
	body, _ := json.Marshal(&request{
		Model:  p.model,
		Prompt: prompt,
		Stream: false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		p.recordFailure()
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg := ""
		if body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096)); readErr == nil {
			msg = strings.TrimSpace(string(body))
			var parsed struct {
				Error string `json:"error"`
			}
			if jsonErr := json.Unmarshal(body, &parsed); jsonErr == nil && strings.TrimSpace(parsed.Error) != "" {
				msg = strings.TrimSpace(parsed.Error)
			}
		}
		p.recordFailure()
		if msg != "" {
			return "", fmt.Errorf("ollama %d: %s", resp.StatusCode, msg)
		}
		return "", fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}
	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		p.recordFailure()
		return "", err
	}
	p.recordSuccess()
	return out.Response, nil
}

const (
	ollamaCircuitFailBudget = 3
	ollamaCircuitOpenFor    = 30 * time.Second
)

func (p *Provider) isCircuitOpen() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.openUntil.After(time.Now())
}

func (p *Provider) recordFailure() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failures++
	if p.failures >= ollamaCircuitFailBudget {
		p.openUntil = time.Now().Add(ollamaCircuitOpenFor)
		p.failures = 0
	}
}

func (p *Provider) recordSuccess() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failures = 0
	p.openUntil = time.Time{}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
