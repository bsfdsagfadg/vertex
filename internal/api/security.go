package api

import (
	"errors"
	"net"
	"net/url"
	"strings"
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
