package secrets

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// AWS environment variable names.
const (
	EnvAWSRegion          = "AWS_REGION"
	EnvAWSAccessKeyID     = "AWS_ACCESS_KEY_ID"
	EnvAWSSecretAccessKey = "AWS_SECRET_ACCESS_KEY"
	EnvAWSSessionToken    = "AWS_SESSION_TOKEN"
	EnvAWSEndpointURL     = "AWS_ENDPOINT_URL" // override for testing
)

const (
	awsSMServiceName = "secretsmanager"
	awsSMAPIVersion  = "secretsmanager.2017-10-13"
	awsSMTarget      = "secretsmanager.GetSecretValue"
)

// AWSSecretsManagerProvider resolves secrets from AWS Secrets Manager
// using the HTTP API with SigV4 signing.  No AWS SDK dependency.
//
// Required environment:
//   - AWS_REGION (required)
//   - AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY (required)
//   - AWS_SESSION_TOKEN (optional, for temporary credentials)
//   - AWS_ENDPOINT_URL (optional, for testing / LocalStack)
type AWSSecretsManagerProvider struct {
	region       string
	accessKey    string
	secretKey    string
	sessionToken string
	endpointURL  string
	client       *http.Client
}

// AWSOption configures optional AWSSecretsManagerProvider behaviour.
type AWSOption func(*AWSSecretsManagerProvider)

// WithAWSHTTPClient overrides the default HTTP client.
func WithAWSHTTPClient(c *http.Client) AWSOption {
	return func(a *AWSSecretsManagerProvider) { a.client = c }
}

// WithAWSEndpoint overrides the endpoint URL (for LocalStack / testing).
func WithAWSEndpoint(url string) AWSOption {
	return func(a *AWSSecretsManagerProvider) { a.endpointURL = url }
}

// NewAWSSecretsManagerProvider creates an AWS Secrets Manager provider.
//
// It reads credentials from the provided arguments and falls back to
// environment variables.  For production use with IAM roles or instance
// profiles, consider extending this to use the EC2 IMDS credential
// chain.
func NewAWSSecretsManagerProvider(region, accessKey, secretKey, sessionToken string, opts ...AWSOption) (*AWSSecretsManagerProvider, error) {
	region = strings.TrimSpace(region)
	accessKey = strings.TrimSpace(accessKey)
	secretKey = strings.TrimSpace(secretKey)
	sessionToken = strings.TrimSpace(sessionToken)

	if region == "" {
		return nil, fmt.Errorf("aws-sm: region is required (set %s)", EnvAWSRegion)
	}
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("aws-sm: credentials required (set %s and %s)", EnvAWSAccessKeyID, EnvAWSSecretAccessKey)
	}

	a := &AWSSecretsManagerProvider{
		region:       region,
		accessKey:    accessKey,
		secretKey:    secretKey,
		sessionToken: sessionToken,
		endpointURL:  fmt.Sprintf("https://secretsmanager.%s.amazonaws.com", region),
		client:       &http.Client{Timeout: 10 * time.Second},
	}
	for _, o := range opts {
		o(a)
	}
	return a, nil
}

func (a *AWSSecretsManagerProvider) Scheme() string { return "aws-sm" }

func (a *AWSSecretsManagerProvider) Resolve(ctx context.Context, ref SecretRef) (string, error) {
	// Build the GetSecretValue JSON body.
	reqBody, _ := json.Marshal(map[string]string{
		"SecretId": ref.Path,
	})

	now := time.Now().UTC()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpointURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return "", fmt.Errorf("aws-sm: build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", awsSMTarget)
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	req.Header.Set("Host", req.URL.Host)
	if a.sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", a.sessionToken)
	}

	// Sign the request with SigV4.
	a.signV4(req, reqBody, now)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("aws-sm: request %s: %w", MaskSecretPath(ref.Path), err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("aws-sm: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", a.parseAWSError(ref.Path, resp.StatusCode, body)
	}

	var result awsSMGetSecretValueOutput
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("aws-sm: parse response for %s: %w", MaskSecretPath(ref.Path), err)
	}

	if result.SecretString == "" {
		return "", fmt.Errorf("aws-sm: %s: binary secrets not supported (use SecretString)",
			MaskSecretPath(ref.Path))
	}

	// If a key is specified, parse the SecretString as JSON and extract.
	if ref.Key != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(result.SecretString), &m); err != nil {
			return "", fmt.Errorf("aws-sm: %s#%s: secret is not JSON: %w",
				MaskSecretPath(ref.Path), ref.Key, err)
		}
		val, ok := m[ref.Key]
		if !ok {
			return "", fmt.Errorf("aws-sm: %s#%s: %w",
				MaskSecretPath(ref.Path), ref.Key, ErrKeyNotFound)
		}
		s, ok := val.(string)
		if !ok {
			return "", fmt.Errorf("aws-sm: %s#%s: value is %T, expected string",
				MaskSecretPath(ref.Path), ref.Key, val)
		}
		return s, nil
	}

	return result.SecretString, nil
}

func (a *AWSSecretsManagerProvider) Close() error { return nil }

// ---------------------------------------------------------------------------
// SigV4 signing (minimal implementation for Secrets Manager)
// ---------------------------------------------------------------------------

func (a *AWSSecretsManagerProvider) signV4(req *http.Request, payload []byte, now time.Time) {
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	// Canonical request.
	payloadHash := sha256Hex(payload)
	signedHeaders := "content-type;host;x-amz-date;x-amz-target"
	if a.sessionToken != "" {
		signedHeaders = "content-type;host;x-amz-date;x-amz-security-token;x-amz-target"
	}

	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-amz-date:%s\n",
		req.Header.Get("Content-Type"), req.URL.Host, amzDate)
	if a.sessionToken != "" {
		canonicalHeaders = fmt.Sprintf("content-type:%s\nhost:%s\nx-amz-date:%s\nx-amz-security-token:%s\n",
			req.Header.Get("Content-Type"), req.URL.Host, amzDate, a.sessionToken)
	}
	canonicalHeaders += fmt.Sprintf("x-amz-target:%s\n", req.Header.Get("X-Amz-Target"))

	// CanonicalHeaders already ends with \n, so we use %s%s (no extra
	// newline) between canonicalHeaders and signedHeaders.  Format:
	//   Method\nURI\nQueryString\nCanonicalHeaders\nSignedHeaders\nPayloadHash
	canonicalRequest := fmt.Sprintf("%s\n/\n\n%s%s\n%s",
		req.Method, canonicalHeaders, signedHeaders, payloadHash)

	// String to sign.
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, a.region, awsSMServiceName)
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate, credentialScope, sha256Hex([]byte(canonicalRequest)))

	// Signing key.
	kDate := hmacSHA256([]byte("AWS4"+a.secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(a.region))
	kService := hmacSHA256(kRegion, []byte(awsSMServiceName))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		a.accessKey, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// AWS error parsing
// ---------------------------------------------------------------------------

type awsErrorResponse struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

func (a *AWSSecretsManagerProvider) parseAWSError(path string, status int, body []byte) error {
	var awsErr awsErrorResponse
	_ = json.Unmarshal(body, &awsErr)

	masked := MaskSecretPath(path)

	switch {
	case strings.Contains(awsErr.Type, "ResourceNotFoundException"):
		return fmt.Errorf("aws-sm: %s: %w", masked, ErrSecretNotFound)
	case strings.Contains(awsErr.Type, "AccessDeniedException"),
		strings.Contains(awsErr.Type, "UnauthorizedException"):
		return fmt.Errorf("aws-sm: %s: %w", masked, ErrAccessDenied)
	case strings.Contains(awsErr.Type, "InvalidRequestException"):
		return fmt.Errorf("aws-sm: %s: invalid request: %s", masked, awsErr.Message)
	default:
		return fmt.Errorf("aws-sm: %s: HTTP %d: %s: %s",
			masked, status, awsErr.Type, truncate(awsErr.Message, 200))
	}
}

// ---------------------------------------------------------------------------
// AWS response types
// ---------------------------------------------------------------------------

type awsSMGetSecretValueOutput struct {
	Name         string `json:"Name"`
	SecretString string `json:"SecretString"`
	VersionId    string `json:"VersionId"`
	ARN          string `json:"ARN"`
}

// ---------------------------------------------------------------------------
// NewAWSSecretsManagerProviderFromEnv is a convenience constructor that
// reads all configuration from environment variables.
// ---------------------------------------------------------------------------

// NewAWSSecretsManagerProviderFromEnv creates an AWS Secrets Manager
// provider from environment variables.
func NewAWSSecretsManagerProviderFromEnv(opts ...AWSOption) (*AWSSecretsManagerProvider, error) {
	return NewAWSSecretsManagerProvider(
		os.Getenv(EnvAWSRegion),
		os.Getenv(EnvAWSAccessKeyID),
		os.Getenv(EnvAWSSecretAccessKey),
		os.Getenv(EnvAWSSessionToken),
		opts...,
	)
}
