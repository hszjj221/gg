package browser

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestCheckPublicURLLiteralIPs(t *testing.T) {
	ctx := context.Background()
	blocked := []string{
		"http://127.0.0.1/",
		"http://[::1]/",
		"http://10.0.0.1/",
		"http://172.16.5.4/",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://0.0.0.0/",
		"http://100.64.0.1/",
		"http://[fe80::1]/",
		"http://[fc00::1]/",
		"http://[::ffff:127.0.0.1]/",
	}
	for _, raw := range blocked {
		if err := CheckPublicURL(ctx, raw); err == nil {
			t.Errorf("%s: expected refusal, got allow", raw)
		}
	}
	allowed := []string{
		"http://8.8.8.8/",
		"https://1.1.1.1/",
	}
	for _, raw := range allowed {
		if err := CheckPublicURL(ctx, raw); err != nil {
			t.Errorf("%s: expected allow, got %v", raw, err)
		}
	}
}

func TestCheckPublicURLRejectsObfuscatedNumericIP(t *testing.T) {
	ctx := context.Background()
	// Decimal, octal, and hex forms of 127.0.0.1 that Chromium interprets
	// as an IP but net.ParseIP does not.
	for _, raw := range []string{
		"http://2130706433/",
		"http://0177.0.0.1/",
		"http://0x7f.0.0.1/",
		"http://0x7f000001/",
	} {
		if err := CheckPublicURL(ctx, raw); err == nil {
			t.Errorf("%s: expected refusal of obfuscated numeric IP", raw)
		}
	}
}

func TestCheckPublicURLRejectsBadScheme(t *testing.T) {
	ctx := context.Background()
	for _, raw := range []string{"ftp://example.com/x", "file:///etc/passwd", "javascript:alert(1)"} {
		if err := CheckPublicURL(ctx, raw); err == nil {
			t.Errorf("%s: expected scheme refusal", raw)
		}
	}
}

func fakeLookup(ips []net.IP, err error) func(context.Context, string, string) ([]net.IP, error) {
	return func(context.Context, string, string) ([]net.IP, error) {
		return ips, err
	}
}

func TestCheckPublicURLDNSResolution(t *testing.T) {
	ctx := context.Background()
	public := []net.IP{net.ParseIP("93.184.216.34")}
	private := []net.IP{net.ParseIP("10.1.2.3")}
	mixed := []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("192.168.0.1")}

	if err := checkPublicURL(ctx, "https://example.com/", fakeLookup(public, nil)); err != nil {
		t.Errorf("public resolution: expected allow, got %v", err)
	}
	if err := checkPublicURL(ctx, "https://intranet.example/", fakeLookup(private, nil)); err == nil {
		t.Error("private resolution: expected refusal, got allow")
	} else if !strings.Contains(err.Error(), "non-public") {
		t.Errorf("private resolution: unexpected error %v", err)
	}
	if err := checkPublicURL(ctx, "https://mixed.example/", fakeLookup(mixed, nil)); err == nil {
		t.Error("mixed resolution: expected refusal when any IP is non-public")
	}
	// Fail closed: DNS errors must not become navigation attempts.
	if err := checkPublicURL(ctx, "https://dns-fail.example/", fakeLookup(nil, errors.New("no such host"))); err == nil {
		t.Error("DNS failure: expected error, got allow")
	}
}
