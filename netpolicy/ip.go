package netpolicy

import (
	"net"
	"net/netip"
)

// blockedCIDRs contains IP ranges that must be blocked when BlockPrivateIPs is enabled
// to prevent SSRF against loopback, private networks, cloud metadata, and link-local targets.
var blockedCIDRs = []netip.Prefix{
	// IPv4 Loopback (127.0.0.0/8)
	netip.MustParsePrefix("127.0.0.0/8"),
	// IPv4 Private networks (RFC 1918)
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	// IPv4 Link-local and Cloud metadata (AWS, GCP, Azure: 169.254.169.254)
	netip.MustParsePrefix("169.254.0.0/16"),
	// IPv4 Shared address space (Carrier-grade NAT RFC 6598)
	netip.MustParsePrefix("100.64.0.0/10"),
	// IPv4 Current network / broadcast / multicast
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("255.255.255.255/32"),

	// IPv6 Loopback
	netip.MustParsePrefix("::1/128"),
	// IPv6 Unique local address (private)
	netip.MustParsePrefix("fc00::/7"),
	// IPv6 Link-local unicast
	netip.MustParsePrefix("fe80::/10"),
	// IPv6 Multicast
	netip.MustParsePrefix("ff00::/8"),
	// IPv6 Unspecified
	netip.MustParsePrefix("::/128"),
}

// IsBlockedIP checks whether the given IP is a private, loopback, link-local,
// cloud-metadata, or special-purpose IP address.
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}

	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}

	// Unwrap IPv4-mapped IPv6 addresses (e.g. ::ffff:127.0.0.1 or ::ffff:169.254.169.254)
	addr = addr.Unmap()

	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}

	for _, prefix := range blockedCIDRs {
		if prefix.Contains(addr) {
			return true
		}
	}

	return false
}
