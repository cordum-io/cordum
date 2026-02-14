package packs

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/redis/go-redis/v9"
)

// PrivateIPNets are RFC 1918 / RFC 4193 / link-local / loopback ranges.
var PrivateIPNets = func() []*net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",    // IPv4 loopback
		"10.0.0.0/8",     // RFC 1918
		"172.16.0.0/12",  // RFC 1918
		"192.168.0.0/16", // RFC 1918
		"169.254.0.0/16", // link-local / AWS metadata
		"::1/128",        // IPv6 loopback
		"fe80::/10",      // IPv6 link-local
		"fc00::/7",       // IPv6 unique-local (RFC 4193)
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("bad private CIDR: " + cidr)
		}
		nets = append(nets, n)
	}
	return nets
}()

// PrivateHostnames are hostnames that always resolve to private/internal addresses.
var PrivateHostnames = map[string]bool{
	"localhost":                 true,
	"metadata.google.internal": true,
}

// IsPrivateNet returns true if the IP falls within a private/loopback/link-local range.
func IsPrivateNet(ip net.IP) bool {
	if ip == nil {
		return true
	}
	for _, n := range PrivateIPNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// SeedDefaultPackCatalogs initializes the default pack catalog in config.
func SeedDefaultPackCatalogs(ctx context.Context, svc *configsvc.Service) error {
	if svc == nil {
		return nil
	}
	disabled := strings.TrimSpace(os.Getenv(EnvPackCatalogDisableDefault))
	if disabled != "" {
		switch strings.ToLower(disabled) {
		case "1", "true", "yes":
			return nil
		}
	}
	catalogURL := strings.TrimSpace(os.Getenv(EnvPackCatalogURL))
	if catalogURL == "" {
		catalogURL = DefaultPackCatalogURL
	}
	if catalogURL == "" {
		return nil
	}
	title := strings.TrimSpace(os.Getenv(EnvPackCatalogTitle))
	if title == "" {
		title = DefaultPackCatalogTitle
	}
	catalogID := strings.TrimSpace(os.Getenv(EnvPackCatalogID))
	if catalogID == "" {
		catalogID = DefaultPackCatalogID
	}

	doc, err := svc.Get(ctx, configsvc.Scope(PackCatalogScope), PackCatalogID)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return err
		}
		doc = &configsvc.Document{
			Scope:   configsvc.Scope(PackCatalogScope),
			ScopeID: PackCatalogID,
			Data:    map[string]any{},
		}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	if existing, ok := doc.Data["catalogs"]; ok && existing != nil {
		switch typed := existing.(type) {
		case []any:
			if len(typed) > 0 {
				return nil
			}
		case []map[string]any:
			if len(typed) > 0 {
				return nil
			}
		default:
			return nil
		}
	}

	doc.Data["catalogs"] = []map[string]any{
		{
			"id":      catalogID,
			"title":   title,
			"url":     catalogURL,
			"enabled": true,
		},
	}
	return svc.Set(ctx, doc)
}

// CompareVersions compares two version strings. Returns -1, 0, or 1.
func CompareVersions(a, b string) int {
	pa, oka := ParseVersion(a)
	pb, okb := ParseVersion(b)
	if oka && okb {
		max := len(pa)
		if len(pb) > max {
			max = len(pb)
		}
		for i := 0; i < max; i++ {
			ai := 0
			bi := 0
			if i < len(pa) {
				ai = pa[i]
			}
			if i < len(pb) {
				bi = pb[i]
			}
			if ai > bi {
				return 1
			}
			if ai < bi {
				return -1
			}
		}
		return 0
	}
	na := NormalizeVersion(a)
	nb := NormalizeVersion(b)
	if na == nb {
		return 0
	}
	if na > nb {
		return 1
	}
	return -1
}

// NormalizeVersion strips whitespace and the leading "v" prefix.
func NormalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	return version
}

// ParseVersion parses a dotted numeric version string.
func ParseVersion(version string) ([]int, bool) {
	version = NormalizeVersion(version)
	if version == "" {
		return nil, false
	}
	if strings.ContainsAny(version, "+-") {
		return nil, false
	}
	parts := strings.Split(version, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}

// ResolvePackURL resolves a potentially relative pack URL against its catalog base URL.
func ResolvePackURL(packURL, catalogURL string) string {
	packURL = strings.TrimSpace(packURL)
	if packURL == "" {
		return packURL
	}
	parsed, err := url.Parse(packURL)
	if err != nil || parsed.Scheme != "" {
		return packURL // already absolute or unparseable
	}
	base, err := url.Parse(strings.TrimSpace(catalogURL))
	if err != nil || base.Scheme == "" {
		return packURL
	}
	return base.ResolveReference(parsed).String()
}

// HostFromURL extracts the lowercase hostname from a URL.
func HostFromURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return ""
	}
	return host
}
