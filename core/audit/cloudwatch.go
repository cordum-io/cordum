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
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// CloudWatchExporter sends SIEM events to AWS CloudWatch Logs via the
// PutLogEvents API. Uses AWS Signature V4 for authentication.
//
// Credentials are read from standard AWS environment variables:
// AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION.
type CloudWatchExporter struct {
	mu            sync.Mutex
	client        *http.Client
	region        string
	accessKey     string
	secretKey     string
	logGroup      string
	logStream     string
	endpoint      string
	sequenceToken string
}

// CloudWatchOption configures a CloudWatchExporter.
type CloudWatchOption func(*CloudWatchExporter)

// WithCloudWatchRegion overrides the AWS region (default: AWS_REGION env var).
func WithCloudWatchRegion(region string) CloudWatchOption {
	return func(c *CloudWatchExporter) { c.region = region }
}

// WithCloudWatchEndpoint overrides the AWS Logs endpoint (useful for tests).
func WithCloudWatchEndpoint(endpoint string) CloudWatchOption {
	return func(c *CloudWatchExporter) { c.endpoint = endpoint }
}

// NewCloudWatchExporter creates a CloudWatch Logs exporter.
// Reads AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, and AWS_REGION from env.
func NewCloudWatchExporter(logGroup, logStream string, opts ...CloudWatchOption) (*CloudWatchExporter, error) {
	c := &CloudWatchExporter{
		client:    safeHTTPClient(10 * time.Second),
		region:    os.Getenv("AWS_REGION"),
		accessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
		secretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		logGroup:  logGroup,
		logStream: logStream,
	}
	for _, o := range opts {
		o(c)
	}
	if c.region == "" {
		c.region = "us-east-1"
	}
	if c.endpoint == "" {
		c.endpoint = fmt.Sprintf("https://logs.%s.amazonaws.com", c.region)
	}
	if c.accessKey == "" || c.secretKey == "" {
		return nil, fmt.Errorf("audit cloudwatch: AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY must be set")
	}
	return c, nil
}

// cwInputLogEvent matches the CloudWatch Logs PutLogEvents InputLogEvent.
type cwInputLogEvent struct {
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message"`
}

// cwPutLogEventsInput is the PutLogEvents request body.
type cwPutLogEventsInput struct {
	LogGroupName  string            `json:"logGroupName"`
	LogStreamName string            `json:"logStreamName"`
	LogEvents     []cwInputLogEvent `json:"logEvents"`
	SequenceToken string            `json:"sequenceToken,omitempty"`
}

// Export sends events to CloudWatch Logs via PutLogEvents.
func (c *CloudWatchExporter) Export(ctx context.Context, events []SIEMEvent) error {
	if len(events) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.exportWithSequence(ctx, events, true)
}

func (c *CloudWatchExporter) exportWithSequence(ctx context.Context, events []SIEMEvent, allowRetry bool) error {
	logEvents := make([]cwInputLogEvent, 0, len(events))
	for _, ev := range events {
		msg, err := json.Marshal(ev)
		if err != nil {
			slog.Error("audit cloudwatch: skipping event with marshal failure",
				"event_type", ev.EventType,
				"error", err,
			)
			continue
		}
		logEvents = append(logEvents, cwInputLogEvent{
			Timestamp: ev.Timestamp.UnixMilli(),
			Message:   string(msg),
		})
	}
	if len(logEvents) == 0 {
		return nil
	}
	// CloudWatch requires events sorted by timestamp.
	sort.Slice(logEvents, func(i, j int) bool {
		return logEvents[i].Timestamp < logEvents[j].Timestamp
	})

	payload := cwPutLogEventsInput{
		LogGroupName:  c.logGroup,
		LogStreamName: c.logStream,
		LogEvents:     logEvents,
	}
	if c.sequenceToken != "" {
		payload.SequenceToken = c.sequenceToken
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("audit cloudwatch marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("audit cloudwatch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "Logs_20140328.PutLogEvents")

	c.signV4(req, body)

	resp, err := c.client.Do(req) // #nosec -- endpoint is operator-configured.
	if err != nil {
		return fmt.Errorf("audit cloudwatch post: %w", err)
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		slog.Warn("audit cloudwatch: unable to read response body",
			"status_code", resp.StatusCode,
			"error", readErr,
		)
		respBody = nil
	}

	if resp.StatusCode >= 400 {
		if allowRetry {
			if expected, errType := parseCloudWatchError(respBody); expected != "" {
				c.sequenceToken = expected
				if errType == "DataAlreadyAcceptedException" {
					return nil
				}
				return c.exportWithSequence(ctx, events, false)
			}
		}
		return fmt.Errorf("audit cloudwatch returned %d", resp.StatusCode)
	}
	if next := parseCloudWatchNextToken(respBody); next != "" {
		c.sequenceToken = next
	}
	return nil
}

// Close is a no-op for the CloudWatch exporter.
func (c *CloudWatchExporter) Close() error { return nil }

// signV4 adds AWS Signature V4 headers to the request.
func (c *CloudWatchExporter) signV4(req *http.Request, body []byte) {
	now := time.Now().UTC()
	datestamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("Host", req.URL.Host)

	// Step 1: Canonical request
	payloadHash := sha256Hex(body)
	signedHeaders := "content-type;host;x-amz-date;x-amz-target"
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-amz-date:%s\nx-amz-target:%s\n",
		req.Header.Get("Content-Type"),
		req.URL.Host,
		amzDate,
		req.Header.Get("X-Amz-Target"),
	)
	canonicalRequest := strings.Join([]string{
		req.Method,
		"/",
		"", // query string
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// Step 2: String to sign
	scope := fmt.Sprintf("%s/%s/logs/aws4_request", datestamp, c.region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	// Step 3: Signing key
	kDate := hmacSHA256([]byte("AWS4"+c.secretKey), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(c.region))
	kService := hmacSHA256(kRegion, []byte("logs"))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	// Step 4: Signature
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		c.accessKey, scope, signedHeaders, signature,
	))
}

func parseCloudWatchNextToken(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var resp struct {
		NextSequenceToken string `json:"nextSequenceToken"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ""
	}
	return resp.NextSequenceToken
}

func parseCloudWatchError(body []byte) (string, string) {
	if len(body) == 0 {
		return "", ""
	}
	var resp struct {
		Type                  string `json:"__type"`
		Code                  string `json:"code"`
		ExpectedSequenceToken string `json:"expectedSequenceToken"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", ""
	}
	errType := resp.Type
	if errType == "" {
		errType = resp.Code
	}
	if strings.Contains(errType, "#") {
		parts := strings.Split(errType, "#")
		errType = parts[len(parts)-1]
	}
	switch errType {
	case "InvalidSequenceTokenException", "DataAlreadyAcceptedException":
		return resp.ExpectedSequenceToken, errType
	default:
		return "", ""
	}
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
