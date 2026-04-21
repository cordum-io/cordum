package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Fixture mirrors the JSON-Schema shape documented in SPEC.md.
type Fixture struct {
	SchemaVersion int      `json:"schemaVersion"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Tags          []string `json:"tags"`
	Setup         Setup    `json:"setup"`
	Steps         []Step   `json:"steps"`
}

type Setup struct {
	Auth    map[string]any    `json:"auth"`
	Headers map[string]string `json:"headers"`
}

type Step struct {
	Kind        string            `json:"kind"`
	OperationID string            `json:"operationId"`
	Auth        map[string]any    `json:"auth"`
	Headers     map[string]string `json:"headers"`
	PathParams  map[string]string `json:"pathParams"`
	Query       map[string]any    `json:"query"`
	Body        any               `json:"body"`
	Expect      Expect            `json:"expect"`
	Extract     map[string]string `json:"extract"`
	DurationMs  int               `json:"durationMs"`
	// Stream-specific fields (used when kind == "stream")
	EventCount int `json:"eventCount"`
	// Assert-error fields (kind == "assert_error")
	ErrorClass string `json:"errorClass"`
	// Paginate fields
	MaxPages int `json:"maxPages"`
}

// Expect is the per-step oracle. `Status` may be a single int or an
// array — the fixture library uses single int today but the shape
// allows for future tolerance.
type Expect struct {
	Status      int               `json:"status"`
	Body        any               `json:"body"`
	BodyMatches map[string]any    `json:"bodyMatches"`
	Headers     map[string]string `json:"headers"`
}

// Driver runs one fixture against the simulator. It maintains the
// `$vars.*` bag across steps via Extract hooks.
type Driver struct {
	BaseURL string
	Client  *http.Client
	APIKey  string
	Tenant  string
	Vars    map[string]any
}

// NewDriver returns a ready-to-run driver with sensible defaults.
func NewDriver(baseURL, apiKey, tenant string) *Driver {
	return &Driver{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Client:  &http.Client{Timeout: 10 * time.Second},
		APIKey:  apiKey,
		Tenant:  tenant,
		Vars:    map[string]any{"apiKey": apiKey, "tenant": tenant},
	}
}

// RunFixture executes every step in the fixture and returns the first
// step-level failure, or nil if all passed. Streaming fixtures are
// tolerated via the `stream` step kind which reads the SSE body and
// asserts an event-count lower bound.
func (d *Driver) RunFixture(fx *Fixture) error {
	for i, step := range fx.Steps {
		if err := d.runStep(fx, i, step); err != nil {
			return fmt.Errorf("step %d (%s %s): %w", i, step.Kind, step.OperationID, err)
		}
	}
	return nil
}

func (d *Driver) runStep(fx *Fixture, idx int, step Step) error {
	switch step.Kind {
	case "sleep":
		time.Sleep(time.Duration(step.DurationMs) * time.Millisecond)
		return nil
	case "request", "assert_error", "stream", "paginate":
		return d.dispatch(fx, idx, step)
	}
	return fmt.Errorf("unknown step kind %q", step.Kind)
}

// dispatch looks up the operation → (method, path) pair, substitutes
// path + query params, executes the HTTP call, and applies the expect
// block via the shared diff engine.
func (d *Driver) dispatch(fx *Fixture, _ int, step Step) error {
	route, ok := operationMap[step.OperationID]
	if !ok {
		return fmt.Errorf("unknown operationId %q", step.OperationID)
	}
	path := route.path
	for k, v := range step.PathParams {
		resolved := d.resolveString(v)
		path = strings.ReplaceAll(path, "{"+k+"}", resolved)
	}
	url := d.BaseURL + path
	if q := buildQuery(step.Query, d.Vars); q != "" {
		url += "?" + q
	}

	var bodyReader io.Reader
	if step.Body != nil {
		resolved := resolveVars(step.Body, d.Vars)
		b, err := json.Marshal(resolved)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(route.method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	d.applyAuth(req, fx.Setup, step.Auth)
	for k, v := range fx.Setup.Headers {
		req.Header.Set(k, d.resolveString(v))
	}
	for k, v := range step.Headers {
		req.Header.Set(k, d.resolveString(v))
	}

	resp, err := d.Client.Do(req)
	if err != nil {
		if step.Kind == "assert_error" && step.ErrorClass == "NetworkError" {
			return nil
		}
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if step.Kind == "stream" {
		return assertStream(body, step)
	}

	expectStatus := step.Expect.Status
	if expectStatus == 0 && step.Kind == "assert_error" {
		expectStatus = inferErrorStatus(step.ErrorClass)
	}
	if expectStatus != 0 && resp.StatusCode != expectStatus {
		return fmt.Errorf("status=%d want %d; body=%s", resp.StatusCode, expectStatus, truncate(body, 240))
	}

	if step.Kind == "assert_error" {
		if step.ErrorClass != "" {
			return assertErrorClass(resp.StatusCode, body, step.ErrorClass)
		}
		return nil
	}

	var actual any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &actual); err != nil {
			return fmt.Errorf("decode body: %w; raw=%s", err, truncate(body, 240))
		}
	}
	if step.Expect.Body != nil {
		if err := Diff(actual, step.Expect.Body, "$"); err != nil {
			return err
		}
	}
	for path, expected := range step.Expect.BodyMatches {
		selected, err := selectJSONPath(actual, path)
		if err != nil {
			return fmt.Errorf("bodyMatches %s: %w", path, err)
		}
		if err := Diff(selected, expected, path); err != nil {
			return err
		}
	}
	for k, v := range step.Extract {
		selected, err := selectJSONPath(actual, v)
		if err != nil {
			return fmt.Errorf("extract %s: %w", v, err)
		}
		d.Vars[k] = selected
	}
	return nil
}

func (d *Driver) applyAuth(req *http.Request, setup Setup, stepAuth map[string]any) {
	auth := setup.Auth
	if stepAuth != nil {
		auth = stepAuth
	}
	if auth == nil {
		req.Header.Set("X-API-Key", d.APIKey)
		return
	}
	kind, _ := auth["kind"].(string)
	value, _ := auth["value"].(string)
	value = d.resolveString(value)
	switch kind {
	case "apiKey":
		if value != "" {
			req.Header.Set("X-API-Key", value)
		}
	case "bearer":
		if value != "" {
			req.Header.Set("Authorization", "Bearer "+value)
		}
	case "none":
		// no auth header — for the unauthorized fixture
	default:
		req.Header.Set("X-API-Key", d.APIKey)
	}
}

func (d *Driver) resolveString(s string) string {
	if !strings.HasPrefix(s, "$vars.") {
		return s
	}
	key := strings.TrimPrefix(s, "$vars.")
	if v, ok := d.Vars[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// buildQuery renders step.Query into a URL query string, resolving
// $vars.* placeholders in values.
func buildQuery(q map[string]any, vars map[string]any) string {
	if len(q) == 0 {
		return ""
	}
	parts := make([]string, 0, len(q))
	for k, v := range q {
		resolved := resolveVars(v, vars)
		parts = append(parts, fmt.Sprintf("%s=%v", k, resolved))
	}
	return strings.Join(parts, "&")
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// selectJSONPath implements a minimal subset of JSONPath sufficient
// for the fixture library's bodyMatches expressions: `$.foo`,
// `$.foo.bar`, `$.items[0]`, `$.items[0].id`.
func selectJSONPath(root any, expr string) (any, error) {
	if !strings.HasPrefix(expr, "$") {
		return nil, fmt.Errorf("path must start with $: %s", expr)
	}
	if expr == "$" {
		return root, nil
	}
	parts := strings.Split(strings.TrimPrefix(expr, "$"), ".")
	cur := root
	for _, rawPart := range parts {
		if rawPart == "" {
			continue
		}
		// Array-index accessor: name[n]
		bracket := strings.Index(rawPart, "[")
		if bracket >= 0 && strings.HasSuffix(rawPart, "]") {
			name := rawPart[:bracket]
			idxStr := rawPart[bracket+1 : len(rawPart)-1]
			if name != "" {
				m, ok := cur.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("cannot index %s on %T", name, cur)
				}
				cur = m[name]
			}
			arr, ok := cur.([]any)
			if !ok {
				return nil, fmt.Errorf("%s: not an array", rawPart)
			}
			var idx int
			if _, err := fmt.Sscanf(idxStr, "%d", &idx); err != nil {
				return nil, fmt.Errorf("bad array index %s", idxStr)
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("index %d out of range (len=%d)", idx, len(arr))
			}
			cur = arr[idx]
			continue
		}
		// Plain key
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cannot descend into %s on %T", rawPart, cur)
		}
		cur = m[rawPart]
	}
	return cur, nil
}

// assertStream parses a best-effort SSE body and confirms the
// expected event count is present. Full SSE semantics can land in
// step 9's parity expansion; v1 is count-based.
func assertStream(body []byte, step Step) error {
	text := string(body)
	events := strings.Count(text, "\ndata:")
	if step.EventCount > 0 && events < step.EventCount {
		return fmt.Errorf("stream events=%d want >=%d", events, step.EventCount)
	}
	if strings.Count(text, "event:") == 0 && events == 0 {
		return fmt.Errorf("stream body carries no SSE frames: %s", truncate(body, 200))
	}
	return nil
}

// assertErrorClass maps gateway error codes + HTTP statuses to the
// conformance error-class taxonomy the fixture declares.
func assertErrorClass(status int, body []byte, class string) error {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	expected := inferErrorStatus(class)
	if expected != 0 && status != expected {
		return fmt.Errorf("errorClass=%s expects status=%d, got %d (body=%s)", class, expected, status, truncate(body, 240))
	}
	return nil
}

// inferErrorStatus maps an ErrorClass name to its canonical HTTP
// status per SPEC.md's taxonomy. Returns 0 for classes that are
// status-agnostic.
func inferErrorStatus(class string) int {
	switch class {
	case "AuthenticationError":
		return 401
	case "AuthorizationError":
		return 403
	case "NotFoundError":
		return 404
	case "ValidationError":
		return 400
	case "ConflictError":
		return 409
	case "RateLimitError":
		return 429
	case "ServerError":
		return 500
	}
	return 0
}
