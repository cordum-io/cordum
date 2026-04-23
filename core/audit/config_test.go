package audit

import (
	"net"
	"testing"
)

func TestNewExporterFromEnv_EmptyType(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "")
	exp, err := NewExporterFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected discard exporter for empty type")
	}
	defer func() { _ = exp.Close() }()
	if _, ok := exp.Backend().(*DiscardExporter); !ok {
		t.Fatalf("Backend() = %T, want *DiscardExporter", exp.Backend())
	}
}

func TestNewExporterFromEnv_NoneType(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "none")
	exp, err := NewExporterFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected discard exporter for 'none' type")
	}
	defer func() { _ = exp.Close() }()
	if _, ok := exp.Backend().(*DiscardExporter); !ok {
		t.Fatalf("Backend() = %T, want *DiscardExporter", exp.Backend())
	}
}

func TestNewExporterFromEnv_NoneTypeCaseInsensitive(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "NONE")
	exp, err := NewExporterFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected discard exporter for 'NONE' type")
	}
	defer func() { _ = exp.Close() }()
	if _, ok := exp.Backend().(*DiscardExporter); !ok {
		t.Fatalf("Backend() = %T, want *DiscardExporter", exp.Backend())
	}
}

func TestNewExporterFromEnv_UnknownType(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "splunk")
	_, err := NewExporterFromEnv()
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestNewExporterFromEnv_Webhook(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "webhook")
	t.Setenv("CORDUM_AUDIT_EXPORT_WEBHOOK_URL", "https://example.com/hook")

	exp, err := NewExporterFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter for webhook")
	}
	_ = exp.Close()
}

func TestNewExporterFromEnv_WebhookMissingURL(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "webhook")
	t.Setenv("CORDUM_AUDIT_EXPORT_WEBHOOK_URL", "")

	_, err := NewExporterFromEnv()
	if err == nil {
		t.Fatal("expected error when webhook URL is missing")
	}
}

func TestNewExporterFromEnv_WebhookWithSecret(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "webhook")
	t.Setenv("CORDUM_AUDIT_EXPORT_WEBHOOK_URL", "https://example.com/hook")
	t.Setenv("CORDUM_AUDIT_EXPORT_WEBHOOK_SECRET", "my-secret")

	exp, err := NewExporterFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter for webhook with secret")
	}
	_ = exp.Close()
}

func TestNewExporterFromEnv_Syslog(t *testing.T) {
	// Start a TCP listener so NewSyslogExporter can connect.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "syslog")
	t.Setenv("CORDUM_AUDIT_EXPORT_SYSLOG_ADDR", "tcp://"+ln.Addr().String())

	exp, err := NewExporterFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter for syslog")
	}
	_ = exp.Close()
}

func TestNewExporterFromEnv_SyslogMissingAddr(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "syslog")
	t.Setenv("CORDUM_AUDIT_EXPORT_SYSLOG_ADDR", "")

	_, err := NewExporterFromEnv()
	if err == nil {
		t.Fatal("expected error when syslog addr is missing")
	}
}

func TestNewExporterFromEnv_SyslogBadAddrFormat(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "syslog")
	t.Setenv("CORDUM_AUDIT_EXPORT_SYSLOG_ADDR", "http://localhost:514")

	_, err := NewExporterFromEnv()
	if err == nil {
		t.Fatal("expected error for non-tcp/udp syslog address")
	}
}

func TestNewExporterFromEnv_Datadog(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "datadog")
	t.Setenv("CORDUM_AUDIT_EXPORT_DD_API_KEY", "test-api-key")

	exp, err := NewExporterFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter for datadog")
	}
	_ = exp.Close()
}

func TestNewExporterFromEnv_DatadogMissingKey(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "datadog")
	t.Setenv("CORDUM_AUDIT_EXPORT_DD_API_KEY", "")

	_, err := NewExporterFromEnv()
	if err == nil {
		t.Fatal("expected error when datadog API key is missing")
	}
}

func TestNewExporterFromEnv_DatadogWithSiteAndTags(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "datadog")
	t.Setenv("CORDUM_AUDIT_EXPORT_DD_API_KEY", "test-api-key")
	t.Setenv("CORDUM_AUDIT_EXPORT_DD_SITE", "eu1")
	t.Setenv("CORDUM_AUDIT_EXPORT_DD_TAGS", "env:test,team:platform")

	exp, err := NewExporterFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter")
	}
	_ = exp.Close()
}

func TestNewExporterFromEnv_CloudWatch(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_REGION", "us-west-2")
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "cloudwatch")
	t.Setenv("CORDUM_AUDIT_EXPORT_CW_LOG_GROUP", "/cordum/audit")
	t.Setenv("CORDUM_AUDIT_EXPORT_CW_LOG_STREAM", "test-stream")

	exp, err := NewExporterFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil exporter for cloudwatch")
	}
	_ = exp.Close()
}

func TestNewExporterFromEnv_CloudWatchMissingGroup(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "cloudwatch")
	t.Setenv("CORDUM_AUDIT_EXPORT_CW_LOG_GROUP", "")
	t.Setenv("CORDUM_AUDIT_EXPORT_CW_LOG_STREAM", "stream")

	_, err := NewExporterFromEnv()
	if err == nil {
		t.Fatal("expected error when cloudwatch log group is missing")
	}
}

func TestNewExporterFromEnv_CloudWatchMissingStream(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "cloudwatch")
	t.Setenv("CORDUM_AUDIT_EXPORT_CW_LOG_GROUP", "group")
	t.Setenv("CORDUM_AUDIT_EXPORT_CW_LOG_STREAM", "")

	_, err := NewExporterFromEnv()
	if err == nil {
		t.Fatal("expected error when cloudwatch log stream is missing")
	}
}

func TestNewExporterFromEnv_CloudWatchMissingAWSCreds(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "cloudwatch")
	t.Setenv("CORDUM_AUDIT_EXPORT_CW_LOG_GROUP", "group")
	t.Setenv("CORDUM_AUDIT_EXPORT_CW_LOG_STREAM", "stream")

	_, err := NewExporterFromEnv()
	if err == nil {
		t.Fatal("expected error when AWS credentials are missing")
	}
}

func TestParseSyslogAddr_TCP(t *testing.T) {
	network, address, err := parseSyslogAddr("tcp://syslog.example.com:514")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if network != "tcp" {
		t.Errorf("network = %q, want tcp", network)
	}
	if address != "syslog.example.com:514" {
		t.Errorf("address = %q, want syslog.example.com:514", address)
	}
}

func TestParseSyslogAddr_UDP(t *testing.T) {
	network, address, err := parseSyslogAddr("udp://10.0.0.1:1514")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if network != "udp" {
		t.Errorf("network = %q, want udp", network)
	}
	if address != "10.0.0.1:1514" {
		t.Errorf("address = %q, want 10.0.0.1:1514", address)
	}
}

func TestParseSyslogAddr_InvalidProtocol(t *testing.T) {
	_, _, err := parseSyslogAddr("http://localhost:514")
	if err == nil {
		t.Fatal("expected error for http:// protocol")
	}
}

func TestParseSyslogAddr_NoProtocol(t *testing.T) {
	_, _, err := parseSyslogAddr("localhost:514")
	if err == nil {
		t.Fatal("expected error for missing protocol")
	}
}
