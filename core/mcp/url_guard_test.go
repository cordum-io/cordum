package mcp

import (
	"context"
	"errors"
	"net"
	"net/url"
	"reflect"
	"testing"
)

func TestNormalizeAllowedHosts(t *testing.T) {
	t.Parallel()
	got := normalizeAllowedHosts([]string{
		" example.com ",
		".example.com",
		"https://api.example.com/path",
		"[::1]:8081",
		"127.0.0.1:8081",
		"",
	})
	want := []string{
		"example.com",
		"api.example.com",
		"::1",
		"127.0.0.1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeAllowedHosts mismatch: got=%v want=%v", got, want)
	}
}

func TestValidateOutboundTargetURL(t *testing.T) {
	t.Parallel()

	origLookup := outboundLookupHostIPs
	t.Cleanup(func() {
		outboundLookupHostIPs = origLookup
	})

	outboundLookupHostIPs = func(_ context.Context, host string) ([]net.IP, error) {
		switch host {
		case "api.example.com":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		case "internal.example.com":
			return []net.IP{net.ParseIP("10.0.0.5")}, nil
		default:
			return nil, errors.New("no such host")
		}
	}

	tests := []struct {
		name              string
		rawURL            string
		allowlist         []string
		allowPrivateHosts bool
		wantErr           bool
	}{
		{
			name:              "reject_private_ip_default",
			rawURL:            "http://127.0.0.1:8081/api/v1/status",
			allowPrivateHosts: false,
			wantErr:           true,
		},
		{
			name:              "allow_private_ip_when_enabled",
			rawURL:            "http://127.0.0.1:8081/api/v1/status",
			allowPrivateHosts: true,
			wantErr:           false,
		},
		{
			name:              "allow_public_host",
			rawURL:            "https://api.example.com/api/v1/status",
			allowPrivateHosts: false,
			wantErr:           false,
		},
		{
			name:              "reject_host_not_in_allowlist",
			rawURL:            "https://api.example.com/api/v1/status",
			allowlist:         []string{"corp.example.com"},
			allowPrivateHosts: false,
			wantErr:           true,
		},
		{
			name:              "allow_host_in_allowlist_suffix",
			rawURL:            "https://api.example.com/api/v1/status",
			allowlist:         []string{"example.com"},
			allowPrivateHosts: false,
			wantErr:           false,
		},
		{
			name:              "reject_hostname_resolving_private",
			rawURL:            "https://internal.example.com/api/v1/status",
			allowPrivateHosts: false,
			wantErr:           true,
		},
		{
			name:              "reject_unsupported_scheme",
			rawURL:            "file:///etc/passwd",
			allowPrivateHosts: false,
			wantErr:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("parse url %q: %v", tt.rawURL, err)
			}
			err = validateOutboundTargetURL(context.Background(), parsed, normalizeAllowedHosts(tt.allowlist), tt.allowPrivateHosts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateOutboundTargetURL error mismatch: got=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
