// Package config defines runtime environment settings once for all services.
package config

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	SiteURL              string
	DataDir              string
	HTTPAddr             string
	TrustedProxies       []netip.Prefix
	ModelProvider        string
	ModelBaseURL         string
	ModelModel           string
	ModelAPIKey          string
	PurchaseAllowedHosts []string
	GuideIPRPM           int
	GuideMaxConcurrency  int
	BuildTimeout         time.Duration
	BuildNodeHeapMB      int
	TemplateDir          string
	AdminStaticDir       string
	SiteConfigPath       string
	SecureCookies        bool
}

// Load reads configuration before taking the DATA_DIR lock. HTTP is accepted
// only for loopback development; deployed origins require HTTPS.
func Load() (Config, error) {
	return load(os.Getenv)
}
func load(get func(string) string) (Config, error) {
	c := Config{}
	value := func(k, def string) string {
		if v := get(k); v != "" {
			return v
		}
		return def
	}
	c.SiteURL = value("SITE_URL", "http://127.0.0.1:8080")
	u, err := url.Parse(c.SiteURL)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return c, fmt.Errorf("invalid SITE_URL origin")
	}
	loopback := u.Hostname() == "localhost"
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return c, fmt.Errorf("SITE_URL requires HTTPS except loopback development")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return c, fmt.Errorf("invalid SITE_URL port")
		}
	}
	c.SiteURL = u.Scheme + "://" + u.Host
	c.SecureCookies = u.Scheme == "https"
	c.HTTPAddr = value("HTTP_ADDR", "127.0.0.1:8080")
	if _, _, err = net.SplitHostPort(c.HTTPAddr); err != nil {
		return c, fmt.Errorf("invalid HTTP_ADDR")
	}
	c.DataDir = value("DATA_DIR", "./data")
	c.ModelProvider = value("MODEL_PROVIDER", "openai")
	if c.ModelProvider != "openai" {
		return c, fmt.Errorf("unsupported MODEL_PROVIDER")
	}
	c.ModelBaseURL = get("MODEL_BASE_URL")
	c.ModelModel = get("MODEL_MODEL")
	c.ModelAPIKey = get("MODEL_API_KEY")
	if c.ModelBaseURL != "" {
		model, err := url.Parse(c.ModelBaseURL)
		if err != nil || model.Host == "" || model.User != nil || (model.Scheme != "https" && model.Scheme != "http") || model.RawQuery != "" || model.Fragment != "" {
			return c, fmt.Errorf("invalid MODEL_BASE_URL")
		}
	}
	for _, raw := range split(get("TRUSTED_PROXIES")) {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			if ip, e := netip.ParseAddr(raw); e == nil {
				prefix = netip.PrefixFrom(ip, ip.BitLen())
			} else {
				return c, fmt.Errorf("invalid TRUSTED_PROXIES")
			}
		}
		c.TrustedProxies = append(c.TrustedProxies, prefix.Masked())
	}
	for _, host := range split(get("PURCHASE_ALLOWED_HOSTS")) {
		parsed, err := url.Parse("https://" + host)
		if err != nil || parsed.Hostname() != host || parsed.Port() != "" || parsed.User != nil || strings.ContainsAny(host, "/*?#@\\:") || strings.HasSuffix(host, ".") {
			return c, fmt.Errorf("invalid PURCHASE_ALLOWED_HOSTS exact hostname")
		}
		c.PurchaseAllowedHosts = append(c.PurchaseAllowedHosts, strings.ToLower(host))
	}
	positive := func(k string, def int) (int, error) {
		n, err := strconv.Atoi(value(k, strconv.Itoa(def)))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%s must be a positive integer", k)
		}
		return n, nil
	}
	if c.GuideIPRPM, err = positive("GUIDE_IP_RPM", 10); err != nil {
		return c, err
	}
	if c.GuideMaxConcurrency, err = positive("GUIDE_MAX_CONCURRENCY", 4); err != nil {
		return c, err
	}
	if c.BuildNodeHeapMB, err = positive("BUILD_NODE_HEAP_MB", 512); err != nil {
		return c, err
	}
	if c.BuildTimeout, err = time.ParseDuration(value("BUILD_TIMEOUT", "300s")); err != nil || c.BuildTimeout <= 0 {
		return c, fmt.Errorf("invalid BUILD_TIMEOUT")
	}
	for _, p := range []struct {
		key, def string
		target   *string
	}{
		{"TEMPLATE_DIR", ".", &c.TemplateDir},
		{"ADMIN_STATIC_DIR", "./admin/dist", &c.AdminStaticDir},
		{"SITE_CONFIG_PATH", "./src/generated/site-config.json", &c.SiteConfigPath},
	} {
		*p.target, err = filepath.Abs(value(p.key, p.def))
		if err != nil {
			return c, fmt.Errorf("invalid %s", p.key)
		}
	}
	return c, nil
}
func split(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func (c Config) ModelConfigured() bool {
	return c.ModelBaseURL != "" && c.ModelModel != "" && c.ModelAPIKey != ""
}
func (c Config) trusted(ip netip.Addr) bool {
	for _, p := range c.TrustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP ignores forwarding headers from untrusted peers, then walks a valid
// X-Forwarded-For chain from the right to the first untrusted address.
func (c Config) ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	peer = peer.Unmap()
	if !c.trusted(peer) {
		return peer.String()
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	ips := make([]netip.Addr, len(chain))
	for i, v := range chain {
		ips[i], err = netip.ParseAddr(strings.TrimSpace(v))
		if err != nil {
			return peer.String()
		}
		ips[i] = ips[i].Unmap()
	}
	for i := len(ips) - 1; i >= 0 && c.trusted(peer); i-- {
		peer = ips[i]
	}
	return peer.String()
}
