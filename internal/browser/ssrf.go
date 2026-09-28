package browser

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
)

// carrierGradeNAT is RFC 6598 shared address space: never publicly routable.
var carrierGradeNAT = func() *net.IPNet {
	_, cidr, err := net.ParseCIDR("100.64.0.0/10")
	if err != nil {
		panic("browser: invalid carrier-grade NAT CIDR: " + err.Error())
	}
	return cidr
}()

// numericIPv4Like matches host strings that look like a numeric IPv4 address
// in decimal, octal, or hex notation (e.g. "2130706433", "0177.0.0.1",
// "0x7f.0.0.1"). Browsers (including Chromium) accept these obfuscated forms
// and interpret them as IPs, while net.ParseIP does not — so they must be
// rejected explicitly instead of falling through to a DNS lookup.
var numericIPv4Like = regexp.MustCompile(`^(0[xX][0-9a-fA-F]+|0[0-7]*|[0-9]+)(\.(0[xX][0-9a-fA-F]+|0[0-7]*|[0-9]+)){0,3}$`)

// CheckPublicURL is a best-effort SSRF guard for agent-driven navigation.
// The model chooses URLs, so before Chromium ever connects we refuse hosts
// that are literal non-public IPs or that resolve (right now) to
// non-public IPs: loopback, private, link-local, multicast, unspecified,
// and carrier-grade NAT ranges. This blocks the direct cases — cloud
// metadata endpoints (169.254.169.254), localhost services, intranet hosts.
//
// Residual risk (documented, not fixed here): this checks a DNS snapshot
// taken at call time, but Chromium re-resolves when it connects, so a
// hostile DNS with very short TTLs (DNS rebinding) could still slip a
// private IP past the check. A complete fix needs an egress proxy that
// pins the resolved address. The snapshot blocks every non-rebinding
// attack, which is the realistic threat for this tool.
func CheckPublicURL(ctx context.Context, rawURL string) error {
	return checkPublicURL(ctx, rawURL, net.DefaultResolver.LookupIP)
}

func checkPublicURL(ctx context.Context, rawURL string, lookup func(context.Context, string, string) ([]net.IP, error)) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("browser: invalid URL %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("browser: only http(s) URLs are allowed")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("browser: URL %q has no host", rawURL)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("browser: refusing to navigate to non-public IP %s", host)
		}
		return nil
	}
	// Obfuscated numeric IP that net.ParseIP does not understand but
	// Chromium would interpret as an address: refuse outright.
	if numericIPv4Like.MatchString(host) {
		return fmt.Errorf("browser: refusing to navigate to obfuscated numeric IP %q", host)
	}
	ips, err := lookup(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("browser: DNS lookup failed for %s: %w", host, err)
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("browser: refusing to navigate to %s (resolves to non-public IP %s)", host, ip)
		}
	}
	return nil
}

// isPublicIP reports whether ip is safe for agent-driven navigation:
// routable on the public internet, not loopback/private/link-local.
func isPublicIP(ip net.IP) bool {
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() ||
		ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	// Carrier-grade NAT (RFC 6598) is never publicly routable.
	if carrierGradeNAT.Contains(ip) {
		return false
	}
	return true
}
