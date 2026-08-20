package transport

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)


// ShadowsocksRDriver handles ShadowsocksR (ssr://, shadowsocksr://) protocol URIs.
type ShadowsocksRDriver struct{}

func (d *ShadowsocksRDriver) Scheme() string {
	return "ssr"
}

func (d *ShadowsocksRDriver) ParseURI(rawURI string) (map[string]any, error) {
	prefix := "ssr://"
	if strings.HasPrefix(strings.ToLower(rawURI), "shadowsocksr://") {
		prefix = "shadowsocksr://"
	}
	body := strings.TrimSpace(rawURI[len(prefix):])
	if hash := strings.Index(body, "#"); hash >= 0 {
		body = body[:hash]
	}
	decodedBytes, err := base64.StdEncoding.DecodeString(strutil.PadB64(body))
	if err != nil {
		if bDecoded, errLoose := strutil.DecodeBase64Loose(body); errLoose == nil {
			decodedBytes = bDecoded
		} else {
			return nil, fmt.Errorf("ssr parse failed: decode body: %w", err)
		}
	}
	decoded := string(decodedBytes)
	params := ""
	if queryIndex := strings.Index(decoded, "/?"); queryIndex >= 0 {
		params = decoded[queryIndex+2:]
		decoded = decoded[:queryIndex]
	} else if queryIndex := strings.Index(decoded, "?"); queryIndex >= 0 {
		params = decoded[queryIndex+1:]
		decoded = strings.TrimSuffix(decoded[:queryIndex], "/")
	}
	parts := strings.SplitN(decoded, ":", 6)
	if len(parts) != 6 {
		return nil, fmt.Errorf("ssr parse failed: invalid body")
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil || port <= 0 || port > 65535 || strings.TrimSpace(parts[0]) == "" {
		return nil, fmt.Errorf("ssr parse failed: invalid server or port")
	}
	passwordBytes, err := base64.StdEncoding.DecodeString(strutil.PadB64(strings.TrimRight(parts[5], "/")))
	if err != nil {
		if pDecoded, errLoose := strutil.DecodeBase64Loose(strings.TrimRight(parts[5], "/")); errLoose == nil {
			passwordBytes = pDecoded
		} else {
			return nil, fmt.Errorf("ssr parse failed: decode password: %w", err)
		}
	}
	out := map[string]any{
		"name":      "",
		"type":      "ssr",
		"server":    parts[0],
		"port":      port,
		"protocol":  parts[2],
		"cipher":    parts[3],
		"obfs":      parts[4],
		"password":  string(passwordBytes),
		"udp":       true,
	}
	if params != "" {
		query, _ := url.ParseQuery(params)
		if value := decodeSSRParam(query.Get("obfsparam")); value != "" {
			out["obfs-param"] = value
		}
		if value := decodeSSRParam(query.Get("protoparam")); value != "" {
			out["protocol-param"] = value
		}
		if value := decodeSSRParam(query.Get("remarks")); value != "" {
			out["name"] = value
		}
	}
	return out, nil
}

func (d *ShadowsocksRDriver) FormatURI(cfg map[string]any) (string, error) {
	server, _ := cfg["server"].(string)
	if server == "" {
		return "", fmt.Errorf("ssr format failed: missing server")
	}
	port := strutil.ToInt(cfg["port"], 8388)
	protocol, _ := cfg["protocol"].(string)
	cipher, _ := cfg["cipher"].(string)
	obfs, _ := cfg["obfs"].(string)
	password, _ := cfg["password"].(string)
	name, _ := cfg["name"].(string)

	encodedPass := base64.RawURLEncoding.EncodeToString([]byte(password))

	q := make(url.Values)
	if name != "" {
		q.Set("remarks", base64.RawURLEncoding.EncodeToString([]byte(name)))
	}
	if obfsParam, _ := cfg["obfs-param"].(string); obfsParam != "" {
		q.Set("obfsparam", base64.RawURLEncoding.EncodeToString([]byte(obfsParam)))
	}
	if protoParam, _ := cfg["protocol-param"].(string); protoParam != "" {
		q.Set("protoparam", base64.RawURLEncoding.EncodeToString([]byte(protoParam)))
	}

	body := fmt.Sprintf("%s:%d:%s:%s:%s:%s", server, port, protocol, cipher, obfs, encodedPass)
	if len(q) > 0 {
		body += "/?" + q.Encode()
	}

	return "ssr://" + base64.RawURLEncoding.EncodeToString([]byte(body)), nil
}

func decodeSSRParam(value string) string {
	if value == "" {
		return ""
	}
	if decoded, err := base64.StdEncoding.DecodeString(strutil.PadB64(value)); err == nil {
		return strings.TrimSpace(string(decoded))
	}
	if decoded, err := url.QueryUnescape(value); err == nil {
		return strings.TrimSpace(decoded)
	}
	return strings.TrimSpace(value)
}
