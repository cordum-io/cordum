package perfharness

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

func sampleQueue(ctx context.Context, client *http.Client, address string, source QueueSource) *float64 {
	u, err := url.Parse(address)
	if err != nil || u.User != nil {
		return nil
	}
	if source.Kind == "nats_jsz" {
		q := u.Query()
		q.Set("accounts", "true")
		q.Set("streams", "true")
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil
	}
	res, err := client.Do(req)
	if err != nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20+1))
	closeErr := res.Body.Close()
	if err != nil || closeErr != nil || res.StatusCode != 200 || len(data) > 1<<20 {
		return nil
	}
	var value *float64
	if source.Kind == "prometheus" {
		value = prometheusValue(data, source.Metric)
	} else {
		value = streamValue(data, source.Stream)
	}
	if value == nil || *value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return nil
	}
	return value
}

func prometheusValue(data []byte, name string) *float64 {
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(data))
	if err != nil || families[name] == nil || len(families[name].Metric) != 1 {
		return nil
	}
	m := families[name].Metric[0]
	if m.Gauge != nil {
		return m.Gauge.Value
	}
	if m.Untyped != nil {
		return m.Untyped.Value
	}
	return nil
}

func streamValue(data []byte, name string) *float64 {
	var doc struct {
		Accounts []struct {
			Streams []struct {
				Name  string `json:"name"`
				State struct {
					Messages *float64 `json:"messages"`
				} `json:"state"`
			} `json:"stream_detail"`
		} `json:"account_details"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	var value *float64
	found := false
	for _, account := range doc.Accounts {
		for _, stream := range account.Streams {
			if stream.Name == name {
				if found {
					return nil
				}
				found = true
				value = stream.State.Messages
			}
		}
	}
	return value
}

func collectQueue(ctx context.Context, client *http.Client, address string, source QueueSource, start time.Time) []QueueSample {
	interval := time.Duration(source.IntervalMS) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var samples []QueueSample
	at := start
	for ctx.Err() == nil {
		sampleCtx, cancel := context.WithTimeout(ctx, interval)
		value := sampleQueue(sampleCtx, client, address, source)
		cancel()
		samples = append(samples, QueueSample{TMS: float64(at.Sub(start)) / float64(time.Millisecond), Value: value})
		select {
		case <-ctx.Done():
			return samples
		case at = <-ticker.C:
		}
	}
	return samples
}
