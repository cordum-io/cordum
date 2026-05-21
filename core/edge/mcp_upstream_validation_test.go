package edge

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryValidationRejectsInvalidTransport(t *testing.T) {
	upstream := validMCPUpstream("tenant-a", "bad-transport")
	upstream.Transport = "websocket"
	err := Validate(context.Background(), &upstream, string(PolicyModeObserve), nil)
	if !errors.Is(err, ErrInvalidTransport) {
		t.Fatalf("Validate error = %v, want ErrInvalidTransport", err)
	}
}

// Loopback / private / metadata targets are SSRF vectors with no legitimate
// MCP upstream use case. Reject them in observe mode just like strict mode;
// only HTTPS-only and allowlist enforcement are mode-gated.
func TestRegistryValidationRejectsLoopbackInObserveMode(t *testing.T) {
	upstream := validMCPUpstream("tenant-a", "local-dev")
	upstream.Endpoint = "http://localhost:8080"
	if err := Validate(context.Background(), &upstream, string(PolicyModeObserve), nil); !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("observe-mode localhost error = %v, want ErrUnsafeEndpoint", err)
	}
}

func TestRegistryValidationAllowsEmptyAuthSecretRef(t *testing.T) {
	upstream := validMCPUpstream("tenant-a", "no-auth")
	upstream.AuthSecretRef = ""
	if err := Validate(context.Background(), &upstream, string(PolicyModeObserve), nil); err != nil {
		t.Fatalf("empty AuthSecretRef should be valid for no-auth upstream: %v", err)
	}
}

func TestRegistryValidationRejectsInvalidNameAndRisk(t *testing.T) {
	badName := validMCPUpstream("tenant-a", "../etc/passwd")
	if err := Validate(context.Background(), &badName, string(PolicyModeObserve), nil); !errors.Is(err, ErrInvalidUpstream) {
		t.Fatalf("bad name error = %v, want ErrInvalidUpstream", err)
	}

	badRisk := validMCPUpstream("tenant-a", "bad-risk")
	badRisk.Risk = "maximum"
	if err := Validate(context.Background(), &badRisk, string(PolicyModeObserve), nil); !errors.Is(err, ErrInvalidUpstream) {
		t.Fatalf("bad risk error = %v, want ErrInvalidUpstream", err)
	}
}

func TestRegistryValidationRejectsStrictHTTPRemote(t *testing.T) {
	upstream := validMCPUpstream("tenant-a", "remote-http")
	upstream.Endpoint = "http://203.0.113.10/mcp"
	err := Validate(context.Background(), &upstream, string(PolicyModeEnterpriseStrict), []string{"remote-http"})
	if !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("strict HTTP remote error = %v, want ErrUnsafeEndpoint", err)
	}
}

func TestRegistryValidationAllowsHTTPOnlyWithExplicitOption(t *testing.T) {
	upstream := validMCPUpstream("tenant-a", "remote-http")
	upstream.Endpoint = "http://203.0.113.10/mcp"
	opts := MCPUpstreamValidationOptions{AllowPlainHTTP: true}
	err := ValidateMCPUpstreamWithOptions(context.Background(), &upstream, string(PolicyModeEnterpriseStrict), []string{"remote-http"}, opts)
	if err != nil {
		t.Fatalf("explicit AllowPlainHTTP validation error = %v, want nil", err)
	}
}

func TestRegistryValidationRejectsUnsafeTenantID(t *testing.T) {
	upstream := validMCPUpstream("*:malicious", "tenant-escape")
	err := Validate(context.Background(), &upstream, string(PolicyModeObserve), nil)
	if !errors.Is(err, ErrInvalidUpstream) {
		t.Fatalf("unsafe tenant id error = %v, want ErrInvalidUpstream", err)
	}
}

func TestRegistryValidationRejectsLoopbackHostWithFragmentNoise(t *testing.T) {
	upstream := validMCPUpstream("tenant-a", "fragment-noise")
	upstream.Endpoint = "https://127.0.0.1:8443/mcp#@attacker.example"
	err := Validate(context.Background(), &upstream, string(PolicyModeEnterpriseStrict), []string{"fragment-noise"})
	if !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("fragment-noise endpoint error = %v, want ErrUnsafeEndpoint", err)
	}
}

// Internal-host SSRF vectors must be rejected regardless of policy mode.
// Strict and observe both refuse loopback, RFC1918, link-local (incl. cloud
// metadata 169.254.169.254), IPv4/IPv6 unspecified, multicast, and IPv6 ULA.
func TestMCPUpstream_RejectsInternalHostsAtAllPaths(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
	}{
		{"aws_imds_v4", "http://169.254.169.254/latest/meta-data/"},
		{"aws_imds_v6", "http://[fd00:ec2::254]/"},
		{"aws_ecs_metadata", "http://169.254.170.2/"},
		{"do_metadata", "http://169.254.169.254/metadata/v1/"},
		{"loopback_v4", "http://127.0.0.1/"},
		{"loopback_v6", "http://[::1]/"},
		{"rfc1918_10", "http://10.0.0.1/"},
		{"rfc1918_192", "http://192.168.1.1/"},
		{"rfc1918_172", "http://172.16.0.1/"},
		{"link_local_v4", "http://169.254.10.1/"},
		{"link_local_v6", "http://[fe80::1]/"},
		{"ula_fc", "http://[fc00::1]/"},
		{"ula_fd", "http://[fd00::1]/"},
		{"multicast_v4", "http://224.0.0.1/"},
		{"multicast_v6", "http://[ff02::1]/"},
		{"unspecified_v4", "http://0.0.0.0/"},
		{"unspecified_v6", "http://[::]/"},
		{"ipv4_mapped_metadata", "http://[::ffff:169.254.169.254]/"},
	}
	for _, mode := range []PolicyMode{PolicyModeObserve, PolicyModeEnforce, PolicyModeEnterpriseStrict} {
		for _, tc := range cases {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				upstream := validMCPUpstream("tenant-a", "internal-target")
				upstream.Endpoint = tc.endpoint
				err := Validate(context.Background(), &upstream, string(mode), []string{"internal-target"})
				if !errors.Is(err, ErrUnsafeEndpoint) {
					t.Fatalf("Validate %s error = %v, want ErrUnsafeEndpoint", tc.endpoint, err)
				}
			})
		}
	}
}

// Cloud-metadata hostnames must be rejected by name BEFORE DNS resolution
// regardless of mode, including case variants and trailing-dot FQDN form.
func TestMCPUpstream_RejectsCloudMetadataHostnames(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
	}{
		{"gcp_lowercase", "http://metadata.google.internal/computeMetadata/"},
		{"gcp_mixed_case", "http://Metadata.Google.Internal/"},
		{"gcp_trailing_dot", "http://metadata.google.internal./"},
		{"aws_instance_data", "http://instance-data.ec2.internal/"},
		{"azure_metadata", "http://metadata.azure.com/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := validMCPUpstream("tenant-a", "metadata-host")
			upstream.Endpoint = tc.endpoint
			err := Validate(context.Background(), &upstream, string(PolicyModeObserve), nil)
			if !errors.Is(err, ErrUnsafeEndpoint) {
				t.Fatalf("Validate %s error = %v, want ErrUnsafeEndpoint", tc.endpoint, err)
			}
		})
	}
}

// IPv6 zone-ID form (fe80::1%eth0) MUST be rejected — zone identifier must
// be stripped before IP-class classification rather than treated as a host.
func TestMCPUpstream_RejectsIPv6WithZoneID(t *testing.T) {
	upstream := validMCPUpstream("tenant-a", "zone-id")
	upstream.Endpoint = "http://[fe80::1%25eth0]/"
	err := Validate(context.Background(), &upstream, string(PolicyModeObserve), nil)
	if !errors.Is(err, ErrUnsafeEndpoint) {
		t.Fatalf("zone-id endpoint error = %v, want ErrUnsafeEndpoint", err)
	}
}
