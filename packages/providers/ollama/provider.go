package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

type Provider struct {
	url    string
	model  string
	client *http.Client
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
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}
	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Response, nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
