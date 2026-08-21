package netx

import (
	"bufio"
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultTermuxPrefix = "/data/data/com.termux/files/usr"

var (
	//nolint:gochecknoglobals // Global cache for Termux nameservers
	termuxNameserverOnce sync.Once
	//nolint:gochecknoglobals // Global cache for Termux nameservers
	termuxNameserverAddrs []string
)

// NewHTTPClient returns a standard HTTP client with a Termux-aware DNS fallback.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{ //nolint:exhaustruct
		Timeout:   timeout,
		Transport: newHTTPTransport(),
	}
}

func newHTTPTransport() *http.Transport {
	base, _ := http.DefaultTransport.(*http.Transport)
	if base == nil {
		base = &http.Transport{} //nolint:exhaustruct
	}
	transport := base.Clone()

	dialer := &net.Dialer{ //nolint:exhaustruct
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	if resolver := newTermuxResolver(); resolver != nil {
		dialer.Resolver = resolver
	}
	transport.DialContext = dialer.DialContext
	return transport
}

func newTermuxResolver() *net.Resolver {
	if !isTermux() {
		return nil
	}
	addrs := loadTermuxNameserverAddrs()
	if len(addrs) == 0 {
		return nil
	}

	return &net.Resolver{ //nolint:exhaustruct
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 5 * time.Second} //nolint:exhaustruct
			var lastErr error
			for _, addr := range addrs {
				conn, err := dialer.DialContext(ctx, network, addr)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}

func isTermux() bool {
	if os.Getenv("TERMUX_VERSION") != "" {
		return true
	}
	if _, err := os.Stat("/data/data/com.termux"); err == nil {
		return true
	}
	return false
}

func loadTermuxNameserverAddrs() []string {
	termuxNameserverOnce.Do(func() {
		prefix := os.Getenv("PREFIX")
		if prefix == "" {
			prefix = defaultTermuxPrefix
		}

		paths := []string{
			filepath.Join(prefix, "etc", "resolv.conf"),
			"/etc/resolv.conf",
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			termuxNameserverAddrs = appendUnique(termuxNameserverAddrs, parseResolvConfNameservers(string(data))...)
		}

		for _, key := range []string{
			"net.dns1",
			"net.dns2",
			"net.dns3",
			"net.dns4",
			"persist.sys.net.dns1",
			"persist.sys.net.dns2",
		} {
			if addr := normalizeNameserverAddr(readAndroidSystemProperty(key)); addr != "" {
				termuxNameserverAddrs = appendUnique(termuxNameserverAddrs, addr)
			}
		}

		if len(termuxNameserverAddrs) > 0 {
			log.Printf("[Net] Termux DNS fallback enabled: %s", strings.Join(termuxNameserverAddrs, ", "))
		} else {
			log.Printf("[Net] Termux DNS fallback unavailable: no usable nameserver found")
		}
	})

	out := make([]string, len(termuxNameserverAddrs))
	copy(out, termuxNameserverAddrs)
	return out
}

func parseResolvConfNameservers(content string) []string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var addrs []string
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.IndexAny(line, "#;"); idx >= 0 {
			line = line[:idx]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "nameserver") {
			continue
		}
		if addr := normalizeNameserverAddr(fields[1]); addr != "" {
			addrs = appendUnique(addrs, addr)
		}
	}
	return addrs
}

func normalizeNameserverAddr(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if ip := net.ParseIP(strings.Trim(raw, "[]")); ip != nil {
		if ip.IsLoopback() || ip.IsUnspecified() {
			return ""
		}
		return net.JoinHostPort(ip.String(), "53")
	}

	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return ""
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	if port == "" {
		port = "53"
	}
	return net.JoinHostPort(ip.String(), port)
}

func readAndroidSystemProperty(key string) string {
	out, err := exec.Command("getprop", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func appendUnique(dst []string, values ...string) []string {
	for _, value := range values {
		if value == "" {
			continue
		}
		exists := false
		for _, item := range dst {
			if item == value {
				exists = true
				break
			}
		}
		if !exists {
			dst = append(dst, value)
		}
	}
	return dst
}
