package mcp

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// maxRedirects is the maximum number of HTTP redirects followed.
const maxRedirects = 5

// SafeHTTPClient returns an *http.Client with redirect protection.
// It limits the total number of redirects and blocks redirects to
// non-HTTPS targets, preventing SSRF via open-redirect chains and
// redirect-loop denial-of-service.
func SafeHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("mcp: stopped after %d redirects", maxRedirects)
			}
			if req.URL.Scheme != "https" {
				return errors.New("mcp: redirect to non-HTTPS target blocked")
			}
			return nil
		},
	}
}
