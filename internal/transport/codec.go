package transport

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

// SupportedSchemes lists all supported proxy protocol schemes.
var SupportedSchemes = map[string]bool{ //nolint:gochecknoglobals
	"vless": true, "trojan": true, "vmess": true, "ss": true,
	"hysteria2": true, "hy2": true, "tuic": true, "socks5": true,
	"socks5h": true, "socks": true, "http": true, "https": true,
	"ssr": true, "shadowsocksr": true, "hysteria": true, "anytls": true,
	"clash": true,
}

// IsSupportedScheme reports whether scheme is a recognized proxy protocol.
func IsSupportedScheme(scheme string) bool {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if SupportedSchemes[scheme] {
		return true
	}
	_, ok := GetDriver(scheme)
	return ok
}

// CanonicalURI returns the normalized, deduplicated URI identity without fragment names.
func CanonicalURI(rawURI string) (string, error) {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return "", fmt.Errorf("empty URI")
	}
	if strings.HasPrefix(strings.ToLower(rawURI), "vmess://") {
		body := rawURI[len("vmess://"):]
		if idx := strings.Index(body, "#"); idx >= 0 {
			body = body[:idx]
		}
		query := ""
		if idx := strings.Index(body, "?"); idx >= 0 {
			query = body[idx:]
			body = body[:idx]
		}
		if decoded, err := strutil.DecodeBase64Loose(body); err == nil {
			var payload map[string]any
			if json.Unmarshal(decoded, &payload) == nil {
				delete(payload, "ps")
				if normalized, err := json.Marshal(payload); err == nil {
					return "vmess:" + string(normalized) + query, nil
				}
			}
		}
	}

	withoutName := rawURI
	if idx := strings.Index(withoutName, "#"); idx >= 0 {
		withoutName = withoutName[:idx]
	}
	parsed, err := url.Parse(withoutName)
	if err != nil || parsed.Scheme == "" {
		return withoutName, nil
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Fragment = ""
	parsed.RawFragment = ""
	if parsed.RawQuery != "" {
		parsed.RawQuery = parsed.Query().Encode()
	}
	return parsed.String(), nil
}

// ParseURI parses various proxy protocol URIs by delegating to registered drivers.
func ParseURI(uri string) (map[string]any, error) {
	scheme, err := ExtractScheme(uri)
	if err != nil {
		safeURI := uri
		if len(safeURI) > 10 {
			safeURI = safeURI[:10]
		}
		return nil, fmt.Errorf("unsupported or complex protocol: %s", safeURI)
	}
	driver, ok := GetDriver(scheme)
	if !ok {
		safeURI := uri
		if len(safeURI) > 10 {
			safeURI = safeURI[:10]
		}
		return nil, fmt.Errorf("unsupported or complex protocol: %s", safeURI)
	}
	return driver.ParseURI(uri)
}

// FormatURI formats a proxy configuration map back into a URI string.
func FormatURI(cfg map[string]any) (string, error) {
	typ, _ := cfg["type"].(string)
	typ = strings.ToLower(strings.TrimSpace(typ))
	if typ == "" {
		return "", fmt.Errorf("missing proxy type in config")
	}
	driver, ok := GetDriver(typ)
	if !ok {
		return "", fmt.Errorf("unsupported proxy type: %s", typ)
	}
	return driver.FormatURI(cfg)
}
