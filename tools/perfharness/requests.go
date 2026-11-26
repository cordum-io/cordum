package perfharness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/sdk/client"
	"github.com/google/uuid"
)

type executor struct {
	client            *http.Client
	mix               []Sequence
	weight            uint64
	sequence          atomic.Uint64
	base, key, tenant string
}

func newExecutor(w *Workload) (*executor, error) {
	e := &executor{mix: w.RequestMix, base: strings.TrimRight(os.Getenv(w.Target.BaseURLEnv), "/"), key: os.Getenv(w.Target.APIKeyEnv), tenant: os.Getenv(w.Target.TenantEnv)}
	u, err := url.Parse(e.base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || e.key == "" || e.tenant == "" {
		return nil, fmt.Errorf("target must be HTTP(S) without credentials/query/fragment; key and tenant must be set")
	}
	for _, sequence := range e.mix {
		if uint64(sequence.Weight) > ^uint64(0)-e.weight {
			return nil, fmt.Errorf("sequence weights overflow")
		}
		e.weight += uint64(sequence.Weight)
		for _, req := range sequence.Requests {
			if req.Kind != "jobs.submit" && req.Kind != "jobs.approve" {
				return nil, fmt.Errorf("unsupported request adapter")
			}
		}
	}
	transport, err := client.BuildTLSTransportErr(client.TLSOptions{CACertPath: os.Getenv("CORDUM_TLS_CA")})
	if err != nil {
		return nil, fmt.Errorf("invalid CORDUM_TLS_CA")
	}
	e.client = &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if transport != nil {
		e.client.Transport = transport
	}
	return e, nil
}

func (e *executor) do(ctx context.Context, method, path string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, e.base+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("invalid request")
	}
	req.Header.Set("X-API-Key", e.key)
	req.Header.Set("X-Tenant-ID", e.tenant)
	req.Header.Set("Content-Type", "application/json")
	res, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed")
	}
	data, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20+1))
	closeErr := res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 || readErr != nil || closeErr != nil || len(data) > 1<<20 {
		return nil, fmt.Errorf("request or response failed")
	}
	return data, nil
}

func (e *executor) version(ctx context.Context) TargetVersion {
	data, err := e.do(ctx, http.MethodGet, "/api/v1/status", nil)
	var status struct {
		Build struct {
			Version string `json:"version"`
		} `json:"build"`
	}
	if err == nil && json.Unmarshal(data, &status) == nil && status.Build.Version != "" && len(status.Build.Version) <= 256 && !strings.Contains(status.Build.Version, e.key) {
		return TargetVersion{Version: status.Build.Version, Source: "status_endpoint"}
	}
	return TargetVersion{Version: "unknown", Source: "unknown"}
}

func (e *executor) pick(n uint64) Sequence {
	n %= e.weight
	for _, sequence := range e.mix {
		if n < uint64(sequence.Weight) {
			return sequence
		}
		n -= uint64(sequence.Weight)
	}
	return e.mix[0]
}

func (e *executor) execute(ctx context.Context, record func(time.Duration, error)) {
	n := e.sequence.Add(1)
	values := map[string]string{"seq": strconv.FormatUint(n, 10), "uuid": uuid.NewString()}
	for _, spec := range e.pick(n - 1).Requests {
		if ctx.Err() != nil {
			return
		}
		request, err := expandRequest(spec, values)
		if err != nil {
			record(0, err)
			return
		}
		path := "/api/v1/jobs"
		if request.Kind == "jobs.approve" {
			id := request.PathParams["job_id"]
			if err := e.awaitApproval(ctx, id); err != nil {
				record(0, err)
				return
			}
			path = "/api/v1/approvals/" + url.PathEscape(id) + "/approve"
		}
		payload, err := json.Marshal(request.Payload)
		if err != nil {
			record(0, err)
			return
		}
		start := time.Now()
		data, err := e.do(ctx, http.MethodPost, path, payload)
		elapsed := time.Since(start)
		if err == nil {
			err = captureFields(data, request.Capture, values)
		}
		record(elapsed, err)
		if err != nil {
			return
		}
	}
}

func expandRequest(spec Request, values map[string]string) (Request, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return Request{}, err
	}
	missing := false
	expanded := templateToken.ReplaceAllStringFunc(string(data), func(token string) string {
		value, ok := values[token[2:len(token)-2]]
		if !ok {
			missing = true
		}
		quoted, _ := json.Marshal(value) // A string is always JSON encodable.
		return string(quoted[1 : len(quoted)-1])
	})
	if missing {
		return Request{}, fmt.Errorf("missing template capture")
	}
	var request Request
	decoder := json.NewDecoder(strings.NewReader(expanded))
	decoder.UseNumber()
	err = decoder.Decode(&request)
	return request, err
}

func captureFields(data []byte, captures map[string]string, values map[string]string) error {
	if len(captures) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return fmt.Errorf("invalid capture response")
	}
	for name, field := range captures {
		var value string
		if json.Unmarshal(fields[field], &value) != nil || value == "" {
			return fmt.Errorf("missing string capture")
		}
		values["capture."+name] = value
	}
	return nil
}

func (e *executor) awaitApproval(ctx context.Context, id string) error {
	if id == "" || id == "." || id == ".." {
		return fmt.Errorf("missing or invalid job id")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := e.do(ctx, http.MethodGet, "/api/v1/jobs/"+url.PathEscape(id), nil)
		var job struct {
			State model.JobState `json:"state"`
		}
		if err != nil || json.Unmarshal(data, &job) != nil {
			return fmt.Errorf("approval status unavailable")
		}
		if job.State == model.JobStateApproval {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("approval wait expired")
		case <-ticker.C:
		}
	}
}
