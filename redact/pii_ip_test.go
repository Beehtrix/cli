package redact

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

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
		{"public with port", "34.117.59.81:443 timed out", "[REDACTED_IP]:443 timed out"},
		{"public in URL", "GET https://34.117.59.81/health", "GET https://[REDACTED_IP]/health"},
		{"public after userinfo", "ssh deploy@13.234.90.180", "ssh deploy@[REDACTED_IP]"},
		{"public with context word", "dial tcp 52.66.93.46: i/o timeout", "dial tcp [REDACTED_IP]: i/o timeout"},
		{"public cidr", "allow 50.219.144.234/32", "allow [REDACTED_IP]/32"},
		{"lsof arrow", "TCP 192.168.26.92:59027->140.82.113.25:443", "TCP [REDACTED_IP]:59027->[REDACTED_IP]:443"},
		{"ipv6 full", "server 2600:1f18:04c2:9300:0000:8a2e:0370:7334 up", "server [REDACTED_IP] up"},
		{"ipv6 compressed with context", "ping 2600:1f18::8a2e:370:7334", "ping [REDACTED_IP]"},
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
		{"ipv6 with eight colons", "server 2600:1f18:1:2:3:4:5::", "server [REDACTED_IP]"},
		{"all-letter unique local", "host fdab::abcd", "host [REDACTED_IP]"},
		{"all-letter unique local three groups", "host fdab::dead:beef", "host [REDACTED_IP]"},
		{"host beside a block constant", "allow 10.0.0.0/8, deny 10.0.0.5", "allow 10.0.0.0/8, deny [REDACTED_IP]"},
		{"subnet inside a private block", `cidr_block = "10.24.0.0/16"`, `cidr_block = "[REDACTED_IP]/16"`},
		{"word that only ends in version", "dns conversion 52.66.93.46", "dns conversion [REDACTED_IP]"},
		{"context word on the address's line", "bumped lib to 3.21.12.1\nssh 52.66.93.46", "bumped lib to 3.21.12.1\nssh [REDACTED_IP]"},
		{"ipv6 after a word ending in hex letters", "iface:fe80::1", "iface:[REDACTED_IP]"},
		{"ipv6 after a label", "node:fd12::5 interface:fd00::7", "node:[REDACTED_IP] interface:[REDACTED_IP]"},
		{"ipv6 before a word", "connect to fe80::1:could not reach", "connect to [REDACTED_IP]:could not reach"},
		{"UNC path", `\\192.168.1.40\share`, `\\[REDACTED_IP]\share`},
		{"net use", `net use Z: \\10.24.3.17\c$`, `net use Z: \\[REDACTED_IP]\c$`},
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
		{"code paths after words", "Vec::default and Self::Abc and code::fd12"},
		{"hex word before colon", "ab:fe80::1"},
		{"time", "at 12:30:45 today"},
		{"file line col", "main.go:12:34: error"},
		{"mac address", "ether aa:bb:cc:dd:ee:ff"},
		{"json escape before ipv6", `"\bdead::1:2"`},
		{"uuid", "id 123e4567-e89b-12d3-a456-426614174000"},
		{".NET assembly version", "System.Runtime, Version=10.0.0.0, Culture=neutral"},
		{"version attribute", `<PackageReference Include="Foo" Version="10.0.0.1" />`},
		{"assembly version attribute", `[assembly: AssemblyVersion("10.1.2.3")]`},
		{"version key in JSON text", `"version": "10.1.2.3"`},
		{"version label beside a network word", "server version 4.12.1.3"},
		{"major version shaped like a network", "upgraded to 10.0.0.0"},
		{"private block constants", `"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"`},
		{"shared and link-local block constants", "100.64.0.0/10 and 169.254.0.0/16"},
		{"ipv6 block constants", "fc00::/7, fd00::/8 and fe80::/10"},
		{"documentation ipv4", "curl https://203.0.113.9/health; ssh deploy@198.51.100.7; host 192.0.2.10"},
		{"documentation ipv6", "server 2001:db8::8a2e:370:7334 and 3fff::1"},
		{"context word on another line", "git remote -v\nbumped protobuf to 3.21.12.1"},
		{"context word cut at the left edge", "ghost " + strings.Repeat("b", 54) + " 1.17.2.3"},
		{"context word cut at the right edge", "1.17.2.3 " + strings.Repeat("a", 26) + " ipsum"},
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
		{`{"host":"34.117.59.81"}`, `{"host":"[REDACTED_IP]"}`},
		{`{"remote_addr":"34.117.59.81:443"}`, `{"remote_addr":"[REDACTED_IP]:443"}`},
		{`{"peerIP":"2600:1f18::7"}`, `{"peerIP":"[REDACTED_IP]"}`},
		{`{"clientIP":"34.117.59.81"}`, `{"clientIP":"[REDACTED_IP]"}`},
		{`{"HOST_IP":"34.117.59.81"}`, `{"HOST_IP":"[REDACTED_IP]"}`},
		{`{"GlobalIPv6Address":"2600:1f18::7"}`, `{"GlobalIPv6Address":"[REDACTED_IP]"}`},
		// A key with a credential stem still gets the IP token, not a whole-value placeholder.
		{`{"api-host":"34.117.59.81"}`, `{"api-host":"[REDACTED_IP]"}`},
		// No network key: a bare public address in prose is the documented gap.
		{`{"content":"34.117.59.81"}`, `{"content":"34.117.59.81"}`},
		// The key does not override the token checks.
		{`{"host":"127.0.0.1"}`, `{"host":"127.0.0.1"}`},
		{`{"host":"203.0.113.9"}`, `{"host":"203.0.113.9"}`},
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

	if got := redactIPsUnderNetworkKey("host", "34.117.59.81", nil); got != "34.117.59.81" {
		t.Errorf("redactIPsUnderNetworkKey = %q, want unchanged with ip off", got)
	}
}

func TestIsNetworkKey(t *testing.T) {
	t.Parallel()
	for key, want := range map[string]bool{
		"host": true, "hostname": true, "remote_addr": true, "peerIP": true, "server-address": true,
		"IP": true, "HOST_IP": true, "DB_HOST": true, "SERVER_ADDR": true, "clientIP": true,
		"podIP": true, "IPAddress": true, "hostName": true,
		"IPv4": true, "IPv6": true, "IPv4Address": true, "GlobalIPv6Address": true, "Ipv6Address": true,
		"content": false, "version": false, "description": false, "shipping": false,
		"tooltip": false, "zipCode": false,
	} {
		if got := isNetworkKey(key); got != want {
			t.Errorf("isNetworkKey(%q) = %v, want %v", key, got, want)
		}
	}
}

// TestIPLines runs the detector over testdata/ip_lines.txt and logs its
// precision and recall there; the file header describes the format. Run with
// -v to see the rates.
func TestIPLines(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "ip_lines.txt"))
	if err != nil {
		t.Fatalf("read table: %v", err)
	}
	var lines, limits, hits, misses, falsePositives int
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line, limit := strings.CutPrefix(line, "limit: ")
		input, marked, ok := parseIPLine(strings.ReplaceAll(line, `\n`, "\n"))
		if !ok {
			t.Fatalf("line %d has an unclosed «: %s", i+1, line)
		}
		found := detectPII(ipOnlyConfig(), input)
		hit, miss, extra := scoreIPLine(marked, found)
		lines++
		hits += hit
		misses += miss
		falsePositives += extra
		wrong := miss+extra > 0
		switch {
		case limit && !wrong:
			t.Errorf("line %d is tagged as a limit but is now handled; drop the tag: %s", i+1, line)
		case limit:
			limits++
		case wrong:
			t.Errorf("line %d: %d missed, %d false positives\n input: %q\n   got: %q", i+1, miss, extra, input, applyRegions(input, found))
		}
	}
	t.Logf("%d lines (%d known limits): %d addresses redacted, %d missed, %d false positives; precision %.1f%%, recall %.1f%%",
		lines, limits, hits, misses, falsePositives,
		percent(hits, hits+falsePositives), percent(hits, hits+misses))
}

// parseIPLine strips the «» markers from line and returns the marked spans
// as offsets into the stripped text.
func parseIPLine(line string) (string, []region, bool) {
	var b strings.Builder
	var marked []region
	for {
		open := strings.Index(line, "«")
		if open < 0 {
			break
		}
		closeAt := strings.Index(line[open:], "»")
		if closeAt < 0 {
			return "", nil, false
		}
		b.WriteString(line[:open])
		start := b.Len()
		b.WriteString(line[open+len("«") : open+closeAt])
		marked = append(marked, region{start, b.Len()})
		line = line[open+closeAt+len("»"):]
	}
	b.WriteString(line)
	return b.String(), marked, true
}

// scoreIPLine counts marked spans the detector redacted exactly (hit), marked
// spans it did not (miss), and redactions no span marks (extra). Overlapping
// detections, such as the IPv4 tail of ::ffff:10.0.0.1, count as one.
func scoreIPLine(marked []region, found []taggedRegion) (hit, miss, extra int) {
	slices.SortFunc(found, func(a, b taggedRegion) int { return a.start - b.start })
	var merged []region
	for _, r := range found {
		if n := len(merged); n > 0 && r.start < merged[n-1].end {
			merged[n-1].end = max(merged[n-1].end, r.end)
			continue
		}
		merged = append(merged, r.region)
	}
	got := map[region]bool{}
	for _, r := range merged {
		got[r] = true
	}
	for _, m := range marked {
		if got[m] {
			hit++
			delete(got, m)
		} else {
			miss++
		}
	}
	return hit, miss, len(got)
}

func percent(n, total int) float64 {
	if total == 0 {
		return 100
	}
	return 100 * float64(n) / float64(total)
}
