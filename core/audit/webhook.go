package audit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WebhookExporter sends SIEM events as JSON via HTTP POST.
type WebhookExporter struct {
	client   *http.Client
	endpoint string
	headers  map[string]string
	secret   string // HMAC-SHA256 signing key (optional)
}

const webhookSecretMinLength = 32

// WebhookOption configures a WebhookExporter.
type WebhookOption func(*WebhookExporter)

// WithWebhookHeaders adds custom HTTP headers to every request.
func WithWebhookHeaders(h map[string]string) WebhookOption {
	return func(w *WebhookExporter) { w.headers = h }
}

// WithWebhookSecret enables HMAC-SHA256 request signing.
// The signature is sent in the X-Cordum-Signature header.
func WithWebhookSecret(secret string) WebhookOption {
	return func(w *WebhookExporter) { w.secret = secret }
}

func validateWebhookSecret(secret string) error {
	if secret != "" && len(secret) < webhookSecretMinLength {
		return fmt.Errorf("webhook secret must be >=%d chars (got %d)", webhookSecretMinLength, len(secret))
	}
	return nil
}

// WithWebhookTimeout sets the HTTP client timeout.
func WithWebhookTimeout(d time.Duration) WebhookOption {
	return func(w *WebhookExporter) { w.client.Timeout = d }
}

// NewWebhookExporter creates a webhook exporter that POSTs JSON to endpoint.
func NewWebhookExporter(endpoint string, opts ...WebhookOption) *WebhookExporter {
	w := &WebhookExporter{
		client:   safeHTTPClient(10 * time.Second),
		endpoint: endpoint,
		headers:  map[string]string{},
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Export sends a batch of events as a JSON array via HTTP POST.
func (w *WebhookExporter) Export(ctx context.Context, events []SIEMEvent) error {
	body, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("audit webhook marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("audit webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range w.headers {
		req.Header.Set(k, v)
	}

	if w.secret != "" {
		mac := hmac.New(sha256.New, []byte(w.secret))
		mac.Write(body)
		sig := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Cordum-Signature", "sha256="+sig)
	}

	resp, err := w.client.Do(req) // #nosec -- endpoint is operator-configured.
	if err != nil {
		return fmt.Errorf("audit webhook post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("audit webhook drain: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("audit webhook returned %d", resp.StatusCode)
	}
	return nil
}

// Close is a no-op for the webhook exporter.
func (w *WebhookExporter) Close() error { return nil }
