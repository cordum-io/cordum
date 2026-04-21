package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRequestMaxAttempts = 3
	defaultRequestBackoff     = 200 * time.Millisecond
)

type RequestDetailedResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	Data       any
	Text       string
}

type RequestError struct {
	ClassName  string
	Message    string
	StatusCode int
	RequestID  string
	Code       string
	Payload    any
	Response   *RequestDetailedResponse
	Cause      error
}

func (e *RequestError) Error() string {
	if e == nil {
		return "<nil>"
	}
	parts := []string{fmt.Sprintf("%s(message=%q", e.ClassName, e.Message)}
	if e.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("status_code=%d", e.StatusCode))
	}
	if e.RequestID != "" {
		parts = append(parts, fmt.Sprintf("request_id=%q", e.RequestID))
	}
	if e.Code != "" {
		parts = append(parts, fmt.Sprintf("code=%q", e.Code))
	}
	return strings.Join(parts, ", ") + ")"
}

func (e *RequestError) Unwrap() error { return e.Cause }

func (e *RequestError) ConformanceClass() string {
	if e == nil {
		return ""
	}
	return e.ClassName
}

type RetryExhaustedError struct {
	RequestError
	Attempts int
}

func (e *RetryExhaustedError) Error() string {
	if e == nil {
		return "<nil>"
	}
	base := e.RequestError.Error()
	return strings.TrimSuffix(base, ")") + fmt.Sprintf(", attempts=%d)", e.Attempts)
}

func (c *Client) RequestDetailed(
	ctx context.Context,
	method, path string,
	body any,
	headers map[string]string,
) (*RequestDetailedResponse, error) {
	response, attempts, retryable, err := c.executeDetailed(ctx, method, path, body, headers)
	if err != nil {
		reqErr := classifyTransportError(err)
		if retryable && attempts >= defaultRequestMaxAttempts {
			return nil, &RetryExhaustedError{
				RequestError: RequestError{
					ClassName: "RetryExhaustedError",
					Message:   reqErr.Message,
					Cause:     err,
				},
				Attempts: attempts,
			}
		}
		return nil, reqErr
	}
	if response == nil {
		return nil, &RequestError{ClassName: "NetworkError", Message: "missing response"}
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}

	reqErr := classifyStatusError(response)
	if retryable && attempts >= defaultRequestMaxAttempts && shouldRetryStatus(response.StatusCode) {
		return nil, &RetryExhaustedError{
			RequestError: RequestError{
				ClassName:  "RetryExhaustedError",
				Message:    reqErr.Message,
				StatusCode: reqErr.StatusCode,
				RequestID:  reqErr.RequestID,
				Code:       reqErr.Code,
				Payload:    reqErr.Payload,
				Response:   reqErr.Response,
			},
			Attempts: attempts,
		}
	}
	return nil, reqErr
}

func (c *Client) Request(
	ctx context.Context,
	method, path string,
	body any,
	headers map[string]string,
) (any, error) {
	response, err := c.RequestDetailed(ctx, method, path, body, headers)
	if err != nil {
		return nil, err
	}
	return response.Data, nil
}

func (c *Client) executeDetailed(
	ctx context.Context,
	method, path string,
	body any,
	headers map[string]string,
) (*RequestDetailedResponse, int, bool, error) {
	payload, err := marshalBody(body)
	if err != nil {
		return nil, 0, false, fmt.Errorf("encode json: %w", err)
	}
	retryable := isRetryableMethod(method, headers)

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	var lastResp *RequestDetailedResponse
	var lastErr error
	for attempt := 1; attempt <= defaultRequestMaxAttempts; attempt++ {
		req, err := c.newDetailedRequest(ctx, method, path, payload, headers)
		if err != nil {
			return nil, attempt, retryable, fmt.Errorf("new request: %w", err)
		}

		resp, err := client.Do(req) // #nosec -- operator-provided gateway URL.
		if err != nil {
			lastErr = err
			if retryable && attempt < defaultRequestMaxAttempts {
				time.Sleep(backoffDelay(attempt, nil))
				continue
			}
			return nil, attempt, retryable, err
		}

		detailed, err := buildDetailedResponse(resp)
		if err != nil {
			return nil, attempt, retryable, err
		}
		lastResp = detailed
		if detailed.StatusCode >= 200 && detailed.StatusCode < 300 {
			return detailed, attempt, retryable, nil
		}
		if retryable && shouldRetryStatus(detailed.StatusCode) && attempt < defaultRequestMaxAttempts {
			time.Sleep(backoffDelay(attempt, detailed.Headers))
			continue
		}
		return detailed, attempt, retryable, nil
	}
	return lastResp, defaultRequestMaxAttempts, retryable, lastErr
}

func marshalBody(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	return json.Marshal(body)
}

func (c *Client) newDetailedRequest(
	ctx context.Context,
	method, path string,
	payload []byte,
	headers map[string]string,
) (*http.Request, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), reader)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
	if tenant := strings.TrimSpace(c.TenantID); tenant != "" && req.Header.Get("X-Tenant-ID") == "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	for key, value := range headers {
		if strings.TrimSpace(key) == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	return req, nil
}

func buildDetailedResponse(resp *http.Response) (*RequestDetailedResponse, error) {
	if resp == nil {
		return nil, nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	detailed := &RequestDetailedResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		Body:       body,
		Text:       string(body),
	}
	if len(body) > 0 && strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		var data any
		if err := json.Unmarshal(body, &data); err == nil {
			detailed.Data = data
		} else {
			detailed.Data = detailed.Text
		}
	} else if len(body) > 0 {
		detailed.Data = detailed.Text
	}
	return detailed, nil
}

func isRetryableMethod(method string, headers map[string]string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	case http.MethodPost:
		for key := range headers {
			if strings.EqualFold(key, "Idempotency-Key") {
				return true
			}
		}
	}
	return false
}

func shouldRetryStatus(status int) bool {
	switch status {
	case 408, 425, 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}

func backoffDelay(attempt int, headers http.Header) time.Duration {
	if headers != nil {
		if raw := strings.TrimSpace(headers.Get("Retry-After")); raw != "" {
			if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
				return time.Duration(seconds) * time.Second
			}
			if when, err := http.ParseTime(raw); err == nil {
				delay := time.Until(when)
				if delay > 0 {
					return delay
				}
			}
		}
	}

	delay := defaultRequestBackoff
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay > 2*time.Second {
			return 2 * time.Second
		}
	}
	return delay
}

func classifyTransportError(err error) *RequestError {
	if err == nil {
		return &RequestError{ClassName: "NetworkError", Message: "request failed"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &RequestError{ClassName: "TimeoutError", Message: err.Error(), Cause: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &RequestError{ClassName: "TimeoutError", Message: err.Error(), Cause: err}
	}
	return &RequestError{ClassName: "NetworkError", Message: err.Error(), Cause: err}
}

func classifyStatusError(response *RequestDetailedResponse) *RequestError {
	if response == nil {
		return &RequestError{ClassName: "NetworkError", Message: "request failed"}
	}

	requestID := response.Headers.Get("X-Request-Id")
	if requestID == "" {
		requestID = response.Headers.Get("X-Request-ID")
	}

	message := strings.TrimSpace(response.Text)
	code := ""
	payload := response.Data
	if obj, ok := payload.(map[string]any); ok {
		if rawError, ok := obj["error"].(map[string]any); ok {
			if value, ok := rawError["message"].(string); ok && strings.TrimSpace(value) != "" {
				message = strings.TrimSpace(value)
			}
			if value, ok := rawError["code"].(string); ok {
				code = value
			}
		}
		if code == "" {
			if value, ok := obj["code"].(string); ok {
				code = value
			}
		}
		if message == "" {
			if value, ok := obj["message"].(string); ok && strings.TrimSpace(value) != "" {
				message = strings.TrimSpace(value)
			}
		}
	}
	if message == "" {
		message = fmt.Sprintf("unexpected status %d", response.StatusCode)
	}

	return &RequestError{
		ClassName:  statusClassName(response.StatusCode),
		Message:    message,
		StatusCode: response.StatusCode,
		RequestID:  requestID,
		Code:       code,
		Payload:    payload,
		Response:   response,
	}
}

func statusClassName(status int) string {
	switch status {
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
		if status >= 500 {
			return "ServerError"
		}
		return "CordumError"
	}
}
