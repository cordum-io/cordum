package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// httpReader issues bounded read-only GETs against a provider API. Used
// by all four scanners so token-redaction, body-size limiting, and
// scheme/host validation stay uniform.
type httpReader struct {
	base       *url.URL
	client     *http.Client
	authHeader string
	authValue  string
	headers    map[string]string
}

func newHTTPReader(rawBaseURL string, client *http.Client) (*httpReader, error) {
	u, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("base URL scheme must be http(s), got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("base URL host is required")
	}
	return &httpReader{base: u, client: boundedHTTPClient(client), headers: map[string]string{}}, nil
}

func (h *httpReader) withBearer(token string) *httpReader {
	if strings.TrimSpace(token) != "" {
		h.authHeader = "Authorization"
		h.authValue = "Bearer " + token
	}
	return h
}

func (h *httpReader) withTokenHeader(name, token string) *httpReader {
	if strings.TrimSpace(token) != "" && strings.TrimSpace(name) != "" {
		h.authHeader = name
		h.authValue = token
	}
	return h
}

func (h *httpReader) withBasicAuth(user, password string) *httpReader {
	if strings.TrimSpace(user) != "" {
		h.authHeader = "BasicAuth"
		h.authValue = user + ":" + password
	}
	return h
}

// resolve builds the absolute URL for a relative API path. The caller
// is responsible for any per-segment URL encoding (e.g. GitLab API
// project identifiers must be `%2F`-encoded); resolve preserves the
// caller's encoding verbatim by concatenating strings instead of
// round-tripping through `url.URL.Path`, which would otherwise double-
// encode `%` characters.
func (h *httpReader) resolve(rel string) string {
	if strings.HasPrefix(rel, "http://") || strings.HasPrefix(rel, "https://") {
		return rel
	}
	base := h.base.String()
	base = strings.TrimRight(base, "/")
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	return base + rel
}

// get performs a bounded GET request, decodes JSON into `dst` when
// dst != nil, and returns the (possibly truncated) raw response body
// on the side. Returns (http.StatusCode, error) — callers handle 404
// without bubbling it up as a hard error.
func (h *httpReader) get(ctx context.Context, rel string, dst interface{}) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.resolve(rel), nil)
	if err != nil {
		return 0, nil, fmt.Errorf("new request: %w", err)
	}
	switch h.authHeader {
	case "":
		// anonymous request
	case "BasicAuth":
		parts := strings.SplitN(h.authValue, ":", 2)
		if len(parts) == 2 {
			req.SetBasicAuth(parts[0], parts[1])
		}
	default:
		req.Header.Set(h.authHeader, h.authValue)
	}
	req.Header.Set("Accept", "application/json,application/xml,text/plain;q=0.9")
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("GET %s: %w", redactURLForError(rel), err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyBytes))
	if readErr != nil {
		return resp.StatusCode, body, fmt.Errorf("read body: %w", readErr)
	}
	if resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, body, nil
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, body, fmt.Errorf("GET %s: status %d", redactURLForError(rel), resp.StatusCode)
	}
	if dst != nil {
		if err := json.Unmarshal(body, dst); err != nil {
			return resp.StatusCode, body, fmt.Errorf("decode JSON %s: %w", redactURLForError(rel), err)
		}
	}
	return resp.StatusCode, body, nil
}

// getRaw returns the raw body bytes only, useful for YAML / XML
// endpoints where structured decoding happens elsewhere.
func (h *httpReader) getRaw(ctx context.Context, rel string) (int, []byte, error) {
	return h.get(ctx, rel, nil)
}

// redactURLForError drops the URL query (which often carries CI tokens)
// and host (which may leak operator-private domains) from an URL before
// surfacing it in a wrapped error.
func redactURLForError(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	// Drop scheme://host so internal hostnames don't leak via error logs.
	if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			return rest[j:]
		}
		return "/"
	}
	return raw
}
