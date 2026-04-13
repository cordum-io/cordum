package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	EnvTelemetryEndpoint     = "CORDUM_TELEMETRY_ENDPOINT"
	defaultTelemetryEndpoint = "https://telemetry.cordum.io/v1/report"
	defaultReporterTimeout   = 10 * time.Second
	defaultReporterAttempts  = 3
	defaultReporterBackoff   = 250 * time.Millisecond
)

// Reporter sends telemetry payloads to the configured HTTPS endpoint.
type Reporter struct {
	client      *http.Client
	endpoint    string
	maxAttempts int
	baseBackoff time.Duration
	sleep       func(context.Context, time.Duration) error
}

func EndpointFromEnv() string {
	if value := strings.TrimSpace(os.Getenv(EnvTelemetryEndpoint)); value != "" {
		return value
	}
	return defaultTelemetryEndpoint
}

func NewReporter(endpoint string, client *http.Client) *Reporter {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = EndpointFromEnv()
	}
	if client == nil {
		client = &http.Client{Timeout: defaultReporterTimeout}
	}
	return &Reporter{
		client:      client,
		endpoint:    strings.TrimSpace(endpoint),
		maxAttempts: defaultReporterAttempts,
		baseBackoff: defaultReporterBackoff,
		sleep:       sleepContext,
	}
}

func (r *Reporter) Endpoint() string {
	if r == nil {
		return ""
	}
	return r.endpoint
}

func (r *Reporter) Report(ctx context.Context, payload TelemetryPayload) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("telemetry reporter unavailable")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.endpoint)), "https://") {
		return fmt.Errorf("telemetry endpoint must use https")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telemetry payload: %w", err)
	}

	var lastErr error
	backoff := r.baseBackoff
	for attempt := 1; attempt <= r.maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build telemetry request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := r.client.Do(req)
		if err == nil && resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			err = fmt.Errorf("telemetry report rejected with status %d", resp.StatusCode)
		}
		if err != nil {
			lastErr = err
		}
		if attempt == r.maxAttempts {
			break
		}
		if err := r.sleep(ctx, backoff); err != nil {
			return err
		}
		backoff *= 2
	}
	return lastErr
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
