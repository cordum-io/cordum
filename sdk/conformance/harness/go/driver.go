package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	sdkclient "github.com/cordum/cordum/sdk/client"
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
	Kind          string            `json:"kind"`
	OperationID   string            `json:"operationId"`
	Auth          map[string]any    `json:"auth"`
	Headers       map[string]string `json:"headers"`
	PathParams    map[string]string `json:"pathParams"`
	Query         map[string]any    `json:"query"`
	Body          any               `json:"body"`
	Expect        Expect            `json:"expect"`
	Extract       map[string]string `json:"extract"`
	DurationMs    int               `json:"durationMs"`
	MaxPages      int               `json:"maxPages"`
	MaxEvents     int               `json:"maxEvents"`
	MaxDurationMs int               `json:"maxDurationMs"`
	ErrorClass    string            `json:"errorClass"`
}

type Expect struct {
	Status      int               `json:"status"`
	ErrorClass  string            `json:"errorClass"`
	Body        any               `json:"body"`
	BodyMatches map[string]any    `json:"bodyMatches"`
	Headers     map[string]string `json:"headers"`
	PageCount   any               `json:"pageCount"`
	TotalItems  any               `json:"totalItems"`
	Events      []ExpectedEvent   `json:"events"`
	Fields      map[string]any    `json:"fields"`
}

type ExpectedEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type streamEvent struct {
	Type string
	Data any
}

// Driver runs one fixture against the simulator via the public Go SDK client.
// It maintains the `$vars.*` bag across steps via Extract hooks.
type Driver struct {
	BaseURL string
	APIKey  string
	Tenant  string
	Vars    map[string]any
}

// NewDriver returns a ready-to-run driver with sensible defaults.
func NewDriver(baseURL, apiKey, tenant string) *Driver {
	return &Driver{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Tenant:  tenant,
		Vars:    map[string]any{"apiKey": apiKey, "tenant": tenant},
	}
}

// RunFixture executes every step in the fixture and returns the first
// step-level failure, or nil if all passed.
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
	default:
		return fmt.Errorf("unknown step kind %q", step.Kind)
	}
}

func (d *Driver) dispatch(fx *Fixture, _ int, step Step) error {
	route, ok := operationMap[step.OperationID]
	if !ok {
		return fmt.Errorf("unknown operationId %q", step.OperationID)
	}

	path := route.path
	for k, v := range step.PathParams {
		path = strings.ReplaceAll(path, "{"+k+"}", d.resolveString(v))
	}
	query := buildQuery(step.Query, d.Vars)
	if query != "" {
		path += "?" + query
	}

	authHeaders, apiKey := d.resolveAuth(fx.Setup, step.Auth)
	headers := d.resolveHeaders(fx.Setup.Headers, step.Headers)
	for key, value := range authHeaders {
		headers[key] = value
	}
	client := &sdkclient.Client{
		BaseURL:  d.BaseURL,
		APIKey:   apiKey,
		TenantID: d.Tenant,
	}

	switch step.Kind {
	case "stream":
		return d.dispatchStream(client, route.method, path, headers, step)
	case "paginate":
		return d.dispatchPaginate(client, route.method, path, headers, step)
	}

	body := resolveVars(step.Body, d.Vars)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	response, err := client.RequestDetailed(ctx, route.method, path, body, headers)
	if step.Kind == "assert_error" {
		if err == nil {
			return fmt.Errorf("expected %s but request succeeded", d.expectedErrorClass(step))
		}
		return d.assertSDKError(err, step)
	}
	if err != nil {
		return err
	}

	if response.StatusCode != step.Expect.Status {
		return fmt.Errorf("status=%d want %d; body=%s", response.StatusCode, step.Expect.Status, truncate(response.Body, 240))
	}
	return d.assertResponse(response, step)
}

func (d *Driver) dispatchPaginate(client *sdkclient.Client, method, path string, headers map[string]string, step Step) error {
	maxPages := step.MaxPages
	if maxPages <= 0 {
		maxPages = 10
	}

	pageCount := 0
	totalItems := 0
	nextPath := path
	for pageCount < maxPages {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		response, err := client.RequestDetailed(ctx, method, nextPath, nil, headers)
		cancel()
		if err != nil {
			return err
		}
		pageCount++

		body, ok := response.Data.(map[string]any)
		if !ok {
			return fmt.Errorf("paginate response is %T, want object", response.Data)
		}
		items, ok := body["items"].([]any)
		if !ok {
			return fmt.Errorf("paginate response missing items array")
		}
		totalItems += len(items)

		cursor := nextCursorFromBody(body)
		if cursor == "" {
			break
		}
		nextPath = mergeCursor(path, cursor)
	}

	if err := assertCountExpectation("pageCount", pageCount, step.Expect.PageCount); err != nil {
		return err
	}
	if err := assertCountExpectation("totalItems", totalItems, step.Expect.TotalItems); err != nil {
		return err
	}
	return nil
}

func (d *Driver) dispatchStream(client *sdkclient.Client, method, path string, headers map[string]string, step Step) error {
	timeout := 5 * time.Second
	if step.MaxDurationMs > 0 {
		timeout = time.Duration(step.MaxDurationMs) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	response, err := client.RequestDetailed(ctx, method, path, nil, headers)
	if err != nil {
		return err
	}
	if step.Expect.Status != 0 && response.StatusCode != step.Expect.Status {
		return fmt.Errorf("status=%d want %d", response.StatusCode, step.Expect.Status)
	}

	events, err := parseStreamEvents(response.Text)
	if err != nil {
		return err
	}
	if step.MaxEvents > 0 && len(events) < step.MaxEvents {
		return fmt.Errorf("stream events=%d want >=%d", len(events), step.MaxEvents)
	}
	if len(step.Expect.Events) == 0 {
		if len(events) == 0 {
			return fmt.Errorf("stream body carries no SSE frames: %s", truncate(response.Body, 200))
		}
		return nil
	}
	if len(events) < len(step.Expect.Events) {
		return fmt.Errorf("stream events=%d want >=%d", len(events), len(step.Expect.Events))
	}
	for i, expected := range step.Expect.Events {
		actual := events[i]
		if actual.Type != expected.Type {
			return fmt.Errorf("stream event %d type=%s want %s", i, actual.Type, expected.Type)
		}
		if err := Diff(actual.Data, expected.Data, fmt.Sprintf("$.events[%d].data", i)); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) assertResponse(response *sdkclient.RequestDetailedResponse, step Step) error {
	if step.Expect.Body != nil {
		if err := Diff(response.Data, step.Expect.Body, "$"); err != nil {
			return err
		}
	}
	for path, expected := range step.Expect.BodyMatches {
		selected, err := selectJSONPath(response.Data, path)
		if err != nil {
			return fmt.Errorf("bodyMatches %s: %w", path, err)
		}
		if err := Diff(selected, expected, path); err != nil {
			return err
		}
	}
	for key, selector := range step.Extract {
		selected, err := selectJSONPath(response.Data, selector)
		if err != nil {
			return fmt.Errorf("extract %s: %w", key, err)
		}
		d.Vars[key] = selected
	}
	return nil
}

func (d *Driver) assertSDKError(err error, step Step) error {
	class, status, payload := extractSDKError(err)
	expectedClass := d.expectedErrorClass(step)
	if expectedClass != "" && class != expectedClass {
		return fmt.Errorf("errorClass=%s want %s", class, expectedClass)
	}

	expectedStatus := step.Expect.Status
	if expectedStatus == 0 {
		expectedStatus = inferErrorStatus(expectedClass)
	}
	if expectedStatus != 0 && status != expectedStatus {
		return fmt.Errorf("status=%d want %d", status, expectedStatus)
	}

	if len(step.Expect.Fields) == 0 {
		return nil
	}
	for selector, expected := range step.Expect.Fields {
		selected, selectErr := selectJSONPath(payload, "$."+selector)
		if selectErr != nil {
			selected, selectErr = selectJSONPath(payload, selector)
		}
		if selectErr != nil {
			return fmt.Errorf("fields %s: %w", selector, selectErr)
		}
		if err := Diff(selected, expected, selector); err != nil {
			return err
		}
	}
	return nil
}

func extractSDKError(err error) (string, int, any) {
	switch typed := err.(type) {
	case *sdkclient.RetryExhaustedError:
		return "RetryExhaustedError", typed.StatusCode, responsePayload(typed.Response)
	case *sdkclient.RequestError:
		return typed.ConformanceClass(), typed.StatusCode, responsePayload(typed.Response)
	default:
		return "NetworkError", 0, nil
	}
}

func responsePayload(response *sdkclient.RequestDetailedResponse) any {
	if response == nil {
		return nil
	}
	return response.Data
}

func (d *Driver) expectedErrorClass(step Step) string {
	if step.ErrorClass != "" {
		return step.ErrorClass
	}
	if step.Expect.ErrorClass != "" {
		return step.Expect.ErrorClass
	}
	if raw, ok := step.Expect.Fields["errorClass"].(string); ok && raw != "" {
		return raw
	}
	if raw, ok := step.Expect.BodyMatches["errorClass"].(string); ok && raw != "" {
		return raw
	}
	return inferClassFromExpect(step.Expect)
}

func inferClassFromExpect(expect Expect) string {
	if expect.Status == 0 {
		return ""
	}
	switch expect.Status {
	case 400, 422:
		return "ValidationError"
	case 401:
		return "AuthenticationError"
	case 403:
		return "AuthorizationError"
	case 404:
		return "NotFoundError"
	case 409:
		return "ConflictError"
	case 429:
		return "RateLimitError"
	default:
		if expect.Status >= 500 {
			return "ServerError"
		}
		return ""
	}
}

func (d *Driver) resolveAuth(setup Setup, stepAuth map[string]any) (map[string]string, string) {
	auth := setup.Auth
	if stepAuth != nil {
		auth = stepAuth
	}
	if auth == nil {
		return nil, d.APIKey
	}

	kind, _ := auth["kind"].(string)
	value, _ := auth["value"].(string)
	value = d.resolveString(value)

	switch kind {
	case "apiKey":
		return nil, value
	case "bearer":
		return map[string]string{"Authorization": "Bearer " + value}, ""
	case "none":
		return nil, ""
	default:
		return nil, d.APIKey
	}
}

func (d *Driver) resolveHeaders(setupHeaders, stepHeaders map[string]string) map[string]string {
	headers := map[string]string{}
	for key, value := range setupHeaders {
		headers[key] = d.resolveString(value)
	}
	for key, value := range stepHeaders {
		headers[key] = d.resolveString(value)
	}
	return headers
}

func (d *Driver) resolveString(s string) string {
	if !strings.HasPrefix(s, "$vars.") {
		return s
	}
	key := strings.TrimPrefix(s, "$vars.")
	if value, ok := d.Vars[key]; ok {
		return fmt.Sprintf("%v", value)
	}
	return ""
}

func buildQuery(q map[string]any, vars map[string]any) string {
	if len(q) == 0 {
		return ""
	}
	values := url.Values{}
	for key, value := range q {
		resolved := resolveVars(value, vars)
		values.Set(key, fmt.Sprintf("%v", resolved))
	}
	return values.Encode()
}

func mergeCursor(path, cursor string) string {
	u, err := url.Parse(path)
	if err != nil {
		return path
	}
	query := u.Query()
	query.Set("cursor", cursor)
	u.RawQuery = query.Encode()
	return u.String()
}

func nextCursorFromBody(body map[string]any) string {
	for _, key := range []string{"nextCursor", "cursor"} {
		if raw, ok := body["next_cursor"].(string); ok && strings.TrimSpace(raw) != "" {
			return raw
		}
		if raw, ok := body[key].(string); ok && strings.TrimSpace(raw) != "" {
			return raw
		}
	}
	return ""
}

func assertCountExpectation(name string, actual int, expected any) error {
	switch typed := expected.(type) {
	case nil:
		return nil
	case float64:
		if actual != int(typed) {
			return fmt.Errorf("%s=%d want %d", name, actual, int(typed))
		}
		return nil
	case int:
		if actual != typed {
			return fmt.Errorf("%s=%d want %d", name, actual, typed)
		}
		return nil
	case string:
		if strings.HasPrefix(typed, ">=") {
			want := 0
			if _, err := fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(typed, ">=")), "%d", &want); err != nil {
				return fmt.Errorf("%s expectation %q invalid", name, typed)
			}
			if actual < want {
				return fmt.Errorf("%s=%d want >=%d", name, actual, want)
			}
			return nil
		}
		want := 0
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &want); err != nil {
			return fmt.Errorf("%s expectation %q invalid", name, typed)
		}
		if actual != want {
			return fmt.Errorf("%s=%d want %d", name, actual, want)
		}
		return nil
	default:
		return fmt.Errorf("%s expectation %T unsupported", name, expected)
	}
}

func parseStreamEvents(text string) ([]streamEvent, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	frames := strings.Split(normalized, "\n\n")
	events := make([]streamEvent, 0, len(frames))
	for _, frame := range frames {
		if strings.TrimSpace(frame) == "" {
			continue
		}
		eventType := "message"
		dataLines := []string{}
		for _, line := range strings.Split(frame, "\n") {
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			}
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		rawData := strings.Join(dataLines, "\n")
		var data any = rawData
		if rawData != "" {
			if err := json.Unmarshal([]byte(rawData), &data); err != nil {
				data = rawData
			}
		}
		events = append(events, streamEvent{Type: eventType, Data: data})
	}
	return events, nil
}

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
	case "ServerError", "RetryExhaustedError":
		return 500
	default:
		return 0
	}
}

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
		bracket := strings.Index(rawPart, "[")
		if bracket >= 0 && strings.HasSuffix(rawPart, "]") {
			name := rawPart[:bracket]
			idxStr := rawPart[bracket+1 : len(rawPart)-1]
			if name != "" {
				mapping, ok := cur.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("cannot index %s on %T", name, cur)
				}
				cur = mapping[name]
			}
			items, ok := cur.([]any)
			if !ok {
				return nil, fmt.Errorf("%s: not an array", rawPart)
			}
			index := 0
			if _, err := fmt.Sscanf(idxStr, "%d", &index); err != nil {
				return nil, fmt.Errorf("bad array index %s", idxStr)
			}
			if index < 0 || index >= len(items) {
				return nil, fmt.Errorf("index %d out of range (len=%d)", index, len(items))
			}
			cur = items[index]
			continue
		}
		mapping, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cannot descend into %s on %T", rawPart, cur)
		}
		cur = mapping[rawPart]
	}
	return cur, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
