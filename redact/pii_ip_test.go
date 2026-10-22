package redact

import "testing"

func ipOnlyConfig() *PIIConfig {
	return &PIIConfig{Enabled: true, Categories: map[PIICategory]bool{PIIIP: true}}
}

func TestDetectPII_IPRedacted(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, input, want string }{
		{"private ipv4", "internal db at 10.24.3.17 is down", "internal db at [REDACTED_IP] is down"},
		{"private ipv4 in JSON value", `{"host":"192.168.1.40"}`, `{"host":"[REDACTED_IP]"}`},
		{"cgnat", "tailscale peer 100.96.0.4", "tailscale peer [REDACTED_IP]"},
		{"link-local metadata", "curl http://169.254.169.254/latest", "curl http://[REDACTED_IP]/latest"},
		{"port kept", "listening on 10.0.0.5:8080", "listening on [REDACTED_IP]:8080"},
		{"public with port", "203.0.113.9:443 timed out", "[REDACTED_IP]:443 timed out"},
		{"public in URL", "GET https://198.51.100.7/health", "GET https://[REDACTED_IP]/health"},
		{"public after userinfo", "ssh deploy@203.0.113.9", "ssh deploy@[REDACTED_IP]"},
		{"public with context word", "dial tcp 52.66.93.46: i/o timeout", "dial tcp [REDACTED_IP]: i/o timeout"},
		{"public cidr", "allow 50.219.144.234/32", "allow [REDACTED_IP]/32"},
		{"lsof arrow", "TCP 192.168.26.92:59027->140.82.113.25:443", "TCP [REDACTED_IP]:59027->[REDACTED_IP]:443"},
		{"ipv6 full", "server 2001:0db8:85a3:0000:0000:8a2e:0370:7334 up", "server [REDACTED_IP] up"},
		{"ipv6 compressed with context", "ping 2001:db8::8a2e:370:7334", "ping [REDACTED_IP]"},
		{"ipv6 unique local short", "addr fd12::1", "addr [REDACTED_IP]"},
		{"ipv6 link-local short", "via fe80::1", "via [REDACTED_IP]"},
		{"ipv6 bracketed URL host", "http://[fd00::5]:8080/x", "http://[[REDACTED_IP]]:8080/x"},
		{"ipv4-mapped ipv6", "peer ::ffff:10.0.0.1", "peer [REDACTED_IP]"},
		{"ipv6 before a colon", "connect to fe80::1: unreachable", "connect to [REDACTED_IP]: unreachable"},
		{"ipv6 full before a colon", "fd12::1:2:3: timeout", "[REDACTED_IP]: timeout"},
		{"ipv6 ending in ::", "allow 2606:4700:cf1::/48", "allow [REDACTED_IP]/48"},
		{"bracketed ipv6 endpoint", "connect [fd00::5]:8080", "connect [[REDACTED_IP]]:8080"},
		{"bracketed link-local endpoint", "dial tcp [fe80::1]:443", "dial tcp [[REDACTED_IP]]:443"},
		{"ipv6 after separator colon", "host:fe80::1", "host:[REDACTED_IP]"},
		{"ipv6 with eight colons", "server 2001:db8:1:2:3:4:5::", "server [REDACTED_IP]"},
		{"all-letter unique local", "host fdab::abcd", "host [REDACTED_IP]"},
		{"all-letter unique local three groups", "host fdab::dead:beef", "host [REDACTED_IP]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := redactPIIOnly(ipOnlyConfig(), tt.input); got != tt.want {
				t.Errorf("redactPIIOnly(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDetectPII_IPNotRedacted(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, input string }{
		{"loopback", "server on 127.0.0.1:8080"},
		{"unspecified", "bind 0.0.0.0:3000"},
		{"zero network", "host 0.1.2.3"},
		{"broadcast", "broadcast host 255.255.255.255"},
		{"ipv6 loopback", "listen on [::1]:8080"},
		{"ipv4-mapped loopback", "host ::ffff:127.0.0.1"},
		{"version tuple", "('3.13.10.14', 800), ('1.17.2.3', 143)"},
		{"bare 4-part version", "upgraded to 1.22.3.4 today"},
		{"v-prefixed version", "release v1.2.3.4 shipped"},
		{"letter-prefixed version", "go1.2.3.4 and python3.1.2.3"},
		{"version with suffix", "tag 1.2.3.4-rc1"},
		{"user agent", "Chrome/154.0.0.0 Safari/537.36"},
		{".NET version", `Version=1.0.0.0, Culture=neutral`},
		{"OID", "oid 1.3.6.1.4.1.311"},
		{"octet out of range", "server 300.1.2.3"},
		{"leading zero", "host 010.0.0.1"},
		{"bare public without context", "results: 52.66.93.46 13.234.90.180"},
		{"rust path", "u64::from(x) and i32::MAX"},
		{"short code path", "add::1:2 and c::d"},
		{"python slice", "a[1::2] and b[10::-1]"},
		{"slice after call", "f()[1::2] and x[0][1::2]"},
		{"hex-letter code path", "c::d and dead::beef"},
		{"hex word before colon", "ab:fe80::1"},
		{"time", "at 12:30:45 today"},
		{"file line col", "main.go:12:34: error"},
		{"mac address", "ether aa:bb:cc:dd:ee:ff"},
		{"json escape before ipv6", `"\bdead::1:2"`},
		{"uuid", "id 123e4567-e89b-12d3-a456-426614174000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := redactPIIOnly(ipOnlyConfig(), tt.input); got != tt.input {
				t.Errorf("redactPIIOnly(%q) = %q, want unchanged", tt.input, got)
			}
		})
	}
}

func TestDetectPII_IPCategoryOff(t *testing.T) {
	t.Parallel()
	cfg := &PIIConfig{Enabled: true, Categories: map[PIICategory]bool{PIIEmail: true}}
	in := "internal db at 10.24.3.17"
	if got := redactPIIOnly(cfg, in); got != in {
		t.Errorf("redactPIIOnly(%q) = %q, want unchanged with ip off", in, got)
	}
}

// Not parallel: sets the package-global PII config.
func TestPIIIntegration_IPThroughStringAndJSONL(t *testing.T) {
	ConfigurePII(PIIConfig{
		Enabled:    true,
		Categories: map[PIICategory]bool{PIIIP: true},
	})
	t.Cleanup(resetPIIConfig)

	if got, want := String("ssh deploy@10.24.3.17 failed"), "ssh deploy@[REDACTED_IP] failed"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	// A JSONL leaf is redacted after decoding and re-encoded, so a newline
	// escape before the address does not hide it.
	got, err := JSONLContent(`{"content":"upgraded to 1.17.2.3\n10.24.3.17"}`)
	if err != nil {
		t.Fatalf("JSONLContent: %v", err)
	}
	if want := `{"content":"upgraded to 1.17.2.3\n[REDACTED_IP]"}`; got != want {
		t.Errorf("JSONLContent() = %q, want %q", got, want)
	}
}

// The custom-pattern workaround for full coverage must not inherit the
// built-in IP rule's context checks just because its label is "ip".
func TestDetectPII_CustomPatternNamedIP(t *testing.T) {
	t.Parallel()
	cfg := &PIIConfig{
		Enabled:        true,
		Categories:     map[PIICategory]bool{},
		CustomPatterns: map[string]string{"ip": `\b\d{1,3}(?:\.\d{1,3}){3}\b`},
	}
	if got, want := redactPIIOnly(cfg, "bare 203.0.113.9"), "bare [REDACTED_IP]"; got != want {
		t.Errorf("redactPIIOnly = %q, want %q", got, want)
	}
}

// Not parallel: sets the package-global PII config.
func TestPIIIntegration_IPUnderNetworkKey(t *testing.T) {
	ConfigurePII(PIIConfig{
		Enabled:    true,
		Categories: map[PIICategory]bool{PIIIP: true},
	})
	t.Cleanup(resetPIIConfig)

	tests := []struct{ in, want string }{
		{`{"host":"203.0.113.9"}`, `{"host":"[REDACTED_IP]"}`},
		{`{"remote_addr":"203.0.113.9:443"}`, `{"remote_addr":"[REDACTED_IP]:443"}`},
		{`{"peerIP":"2001:db8::7"}`, `{"peerIP":"[REDACTED_IP]"}`},
		// No network key: a bare public address in prose is the documented gap.
		{`{"content":"203.0.113.9"}`, `{"content":"203.0.113.9"}`},
		// The key does not override the token checks.
		{`{"host":"127.0.0.1"}`, `{"host":"127.0.0.1"}`},
		{`{"version":"1.17.2.3"}`, `{"version":"1.17.2.3"}`},
	}
	for _, tt := range tests {
		got, err := JSONLContent(tt.in)
		if err != nil {
			t.Fatalf("JSONLContent(%s): %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("JSONLContent(%s) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

// Not parallel: sets the package-global PII config.
func TestRedactIPsUnderNetworkKey_IPCategoryOff(t *testing.T) {
	ConfigurePII(PIIConfig{
		Enabled:    true,
		Categories: map[PIICategory]bool{PIIEmail: true},
	})
	t.Cleanup(resetPIIConfig)

	if got := redactIPsUnderNetworkKey("host", "203.0.113.9"); got != "203.0.113.9" {
		t.Errorf("redactIPsUnderNetworkKey = %q, want unchanged with ip off", got)
	}
}

func TestIsNetworkKey(t *testing.T) {
	t.Parallel()
	for key, want := range map[string]bool{
		"host": true, "hostname": true, "remote_addr": true, "peerIP": true, "server-address": true,
		"content": false, "version": false, "description": false, "shipping": false,
	} {
		if got := isNetworkKey(key); got != want {
			t.Errorf("isNetworkKey(%q) = %v, want %v", key, got, want)
		}
	}
}
