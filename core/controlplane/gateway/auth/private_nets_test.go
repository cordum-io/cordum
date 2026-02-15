package auth

import (
	"net"
	"testing"
)

func TestPrivateIPNetsInitialized(t *testing.T) {
	if PrivateIPNets == nil {
		t.Fatal("PrivateIPNets is nil — IIFE init failed")
	}
	// 8 CIDRs: 127/8, 10/8, 172.16/12, 192.168/16, 169.254/16, ::1/128, fe80::/10, fc00::/7
	if got := len(PrivateIPNets); got != 8 {
		t.Fatalf("expected 8 private nets, got %d", got)
	}
}

func TestIsPrivateNet(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.1.1", true},
		{"::1", true},
		{"8.8.8.8", false},
		{"93.184.216.34", false},
	}
	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if got := IsPrivateNet(ip); got != tt.want {
			t.Errorf("IsPrivateNet(%s) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func TestIsPrivateNetNilIP(t *testing.T) {
	if !IsPrivateNet(nil) {
		t.Fatal("expected nil IP to be treated as private")
	}
}
