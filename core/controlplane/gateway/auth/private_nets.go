package auth

import "net"

// PrivateIPNets are RFC 1918 / RFC 4193 / link-local / loopback ranges
// used for SSRF protection across gateway auth and packs subsystems.
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
			// INVARIANT: CIDRs are hardcoded constants; panic is acceptable
			// as a process-fatal assertion — no user input is involved.
			panic("bad private CIDR: " + cidr)
		}
		nets = append(nets, n)
	}
	return nets
}()

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
