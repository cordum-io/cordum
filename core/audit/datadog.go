package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Datadog site endpoints for log intake (v2 API).
var datadogSites = map[string]string{
	"us1": "https://http-intake.logs.datadoghq.com",
	"us3": "https://http-intake.logs.us3.datadoghq.com",
	"us5": "https://http-intake.logs.us5.datadoghq.com",
	"eu1": "https://http-intake.logs.datadoghq.eu",
	"ap1": "https://http-intake.logs.ap1.datadoghq.com",
}

// DatadogExporter sends SIEM events to the Datadog Log Intake API.
type DatadogExporter struct {
	client   *http.Client
	apiKey   string
	endpoint string
	tags     string
	hostname string
}

// DatadogOption configures a DatadogExporter.
type DatadogOption func(*DatadogExporter)

// WithDatadogSite sets the Datadog site (us1, us3, us5, eu1, ap1).
func WithDatadogSite(site string) DatadogOption {
	return func(d *DatadogExporter) {
		if base, ok := datadogSites[site]; ok {
			d.endpoint = base + "/api/v2/logs"
		}
	}
}

// WithDatadogTags sets custom ddtags (comma-separated key:value pairs).
func WithDatadogTags(tags string) DatadogOption {
	return func(d *DatadogExporter) { d.tags = tags }
}

// NewDatadogExporter creates a Datadog log intake exporter.
func NewDatadogExporter(apiKey string, opts ...DatadogOption) *DatadogExporter {
	hostname, _ := os.Hostname()
	d := &DatadogExporter{
		client:   &http.Client{Timeout: 10 * time.Second},
		apiKey:   apiKey,
		endpoint: datadogSites["us1"] + "/api/v2/logs",
		tags:     "service:cordum-gateway",
		hostname: hostname,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// ddLogEntry is the Datadog log intake payload format.
type ddLogEntry struct {
	DDSource string `json:"ddsource"`
	DDTags   string `json:"ddtags"`
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
	Message  string `json:"message"`
}

// Export sends a batch of events to the Datadog Log Intake API.
func (d *DatadogExporter) Export(ctx context.Context, events []SIEMEvent) error {
	entries := make([]ddLogEntry, len(events))
	for i, ev := range events {
		msg, _ := json.Marshal(ev)
		entries[i] = ddLogEntry{
			DDSource: "cordum",
			DDTags:   d.tags,
			Hostname: d.hostname,
			Service:  "cordum",
			Message:  string(msg),
		}
	}

	body, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("audit datadog marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("audit datadog request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("DD-API-KEY", d.apiKey)

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("audit datadog post: %w", err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("audit datadog drain: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("audit datadog returned %d", resp.StatusCode)
	}
	return nil
}

// Close is a no-op for the Datadog exporter.
func (d *DatadogExporter) Close() error { return nil }
