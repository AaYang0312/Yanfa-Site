package config

import (
	"net/http/httptest"
	"testing"
)

func TestOriginsAndTrustedProxyBoundary(t *testing.T) {
	cases := []struct {
		origin        string
		valid, secure bool
	}{
		{"http://127.0.0.1:18003", true, false}, {"http://[::1]:18003", true, false}, {"https://admin.example", true, true},
		{"http://admin.example", false, false}, {"https://admin.example/path", false, false}, {"https://user:pass@admin.example", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.origin, func(t *testing.T) {
			cfg, err := load(func(key string) string {
				if key == "SITE_URL" {
					return tc.origin
				}
				return ""
			})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if err == nil && cfg.SecureCookies != tc.secure {
				t.Fatal("incorrect cookie security")
			}
		})
	}
	cfg, err := load(func(key string) string {
		if key == "TRUSTED_PROXIES" {
			return "127.0.0.1/32,10.0.0.0/8"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "http://example/", nil)
	req.RemoteAddr = "198.51.100.9:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := cfg.ClientIP(req); got != "198.51.100.9" {
		t.Fatalf("untrusted peer spoofed IP %s", got)
	}
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.9, 10.0.0.5")
	if got := cfg.ClientIP(req); got != "198.51.100.9" {
		t.Fatalf("incorrect proxy boundary %s", got)
	}
	req.Header.Set("X-Forwarded-For", "malformed, 10.0.0.5")
	if got := cfg.ClientIP(req); got != "127.0.0.1" {
		t.Fatalf("malformed chain accepted %s", got)
	}
}
