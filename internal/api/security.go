package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/netx"
)

// validateSubscriptionURL rejects destinations that are not suitable for a
// server-side subscription fetch. DNS names are intentionally allowed here so
// normal public subscriptions keep working; literal private destinations and
// well-known metadata names are never valid subscription endpoints.
func validateSubscriptionURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil {
		return errors.New("订阅链接格式无效")
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return errors.New("订阅链接必须使用 HTTP 或 HTTPS")
	}
	if u.Hostname() == "" || u.User != nil {
		return errors.New("订阅链接主机无效")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		host == "metadata.google.internal" || host == "metadata" {
		return errors.New("订阅链接目标地址被禁止")
	}
	if ip := net.ParseIP(host); ip != nil && blockedSubscriptionIP(ip) {
		return errors.New("订阅链接目标地址被禁止")
	}
	return nil
}

func redactSubscriptionURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil {
		return "<invalid-url>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func blockedSubscriptionIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// validateSubscriptionURLResolved closes the DNS-name gap left by the cheap
// syntax check above. A subscription host must resolve to public addresses
// before a direct server-side fetch is attempted.
func validateSubscriptionURLResolved(ctx context.Context, raw string) error {
	if err := validateSubscriptionURL(raw); err != nil {
		return err
	}
	u, _ := url.Parse(strings.TrimSpace(raw))
	_, err := lookupPublicSubscriptionIPs(ctx, u.Hostname())
	if err != nil {
		return fmt.Errorf("订阅链接主机解析失败: %w", err)
	}
	return nil
}

func newSubscriptionHTTPClient(timeout time.Duration) *http.Client {
	client := netx.NewHTTPClient(timeout)
	if base, ok := client.Transport.(*http.Transport); ok {
		transport := base.Clone()
		originalDial := transport.DialContext
		if originalDial == nil {
			originalDial = (&net.Dialer{Timeout: timeout}).DialContext
		}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := lookupPublicSubscriptionIPs(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, ip := range ips {
				conn, dialErr := originalDial(ctx, network, net.JoinHostPort(ip.String(), port))
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
			return nil, lastErr
		}
		client.Transport = transport
	}
	client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		if err := validateSubscriptionURLResolved(req.Context(), req.URL.String()); err != nil {
			return err
		}
		return nil
	}
	return client
}

func lookupPublicSubscriptionIPs(ctx context.Context, host string) ([]net.IP, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("订阅链接主机没有可用地址")
	}
	for _, ip := range ips {
		if blockedSubscriptionIP(ip) {
			return nil, errors.New("订阅链接目标地址被禁止")
		}
	}
	return ips, nil
}
