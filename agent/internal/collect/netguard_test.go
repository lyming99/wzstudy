package collect

import (
	"net"
	"testing"
)

func TestAllowedIPRejectsNonPublicTargets(t *testing.T) {
	cases := []struct {
		address      string
		allowPrivate bool
		want         bool
	}{
		{"127.0.0.1", false, false},
		{"127.0.0.1", true, true},
		{"10.1.2.3", false, false},
		{"10.1.2.3", true, true},
		{"172.16.0.9", false, false},
		{"192.168.1.10", false, false},
		{"169.254.169.254", false, false},
		{"100.64.0.1", false, true},
		{"0.0.0.0", false, false},
		{"::1", false, false},
		{"::1", true, true},
		{"::ffff:127.0.0.1", false, false},
		{"fc00::1", false, false},
		{"fe80::1", false, false},
		{"ff02::1", false, false},
		{"8.8.8.8", false, true},
		{"2606:4700:4700::1111", false, true},
	}
	for _, testCase := range cases {
		ip := net.ParseIP(testCase.address)
		if ip == nil {
			t.Fatalf("bad fixture %s", testCase.address)
		}
		if got := allowedIP(ip, testCase.allowPrivate); got != testCase.want {
			t.Errorf("allowedIP(%s, %v)=%v want %v", testCase.address, testCase.allowPrivate, got, testCase.want)
		}
	}
}

func TestValidateTargetRejectsCredentialsAndSchemes(t *testing.T) {
	for _, raw := range []string{"ftp://example.com/a", "file:///etc/passwd", "https://user:pass@example.com/", "javascript:alert(1)"} {
		if _, err := validateTarget(raw); err == nil {
			t.Errorf("%s must be rejected", raw)
		}
	}
	if _, err := validateTarget("https://example.com/a"); err != nil {
		t.Fatalf("valid target rejected: %v", err)
	}
}
