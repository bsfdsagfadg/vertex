package transport

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/strutil"
)


// ShadowsocksDriver handles Shadowsocks (ss://) protocol URIs.
type ShadowsocksDriver struct{}

func (d *ShadowsocksDriver) Scheme() string {
	return "ss"
}

func (d *ShadowsocksDriver) ParseURI(rawURI string) (map[string]any, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("ss parse error: %w", err)
	}
	if u.User == nil || u.Hostname() == "" {
		return parseLegacySS(rawURI)
	}

	method, password, err := decodeSSUserInfo(u.User)
	if err != nil {
		return nil, err
	}
	port, err := parseProxyPort(u, 0)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("ss parse failed: invalid host:port")
	}
	name := nameFromURL(u)

	out := map[string]any{
		"name":     name,
		"type":     "ss",
		"server":   u.Hostname(),
		"port":     port,
		"cipher":   normalizeSSCipher(method),
		"password": password,
	}
	if queryFlag(u.Query(), "udp", "udp-relay") {
		out["udp"] = true
	}
	applySSPlugin(out, u.Query().Get("plugin"))
	return out, nil
}

func (d *ShadowsocksDriver) FormatURI(cfg map[string]any) (string, error) {
	server, _ := cfg["server"].(string)
	if server == "" {
		return "", fmt.Errorf("ss format failed: missing server")
	}
	port := strutil.ToInt(cfg["port"], 8388)
	cipher, _ := cfg["cipher"].(string)
	password, _ := cfg["password"].(string)
	name, _ := cfg["name"].(string)

	rawCreds := cipher + ":" + password
	encodedUser := base64.RawURLEncoding.EncodeToString([]byte(rawCreds))

	q := make(url.Values)
	if udp, ok := cfg["udp"].(bool); ok && udp {
		q.Set("udp", "true")
	}
	if pluginStr := formatSSPlugin(cfg); pluginStr != "" {
		q.Set("plugin", pluginStr)
	}

	return buildStandardURI("ss", encodedUser, server, port, q, name), nil
}
func decodeSSUserInfo(user *url.Userinfo) (string, string, error) {
	if user == nil {
		return "", "", fmt.Errorf("ss parse failed: missing userinfo")
	}
	if password, ok := user.Password(); ok {
		return user.Username(), password, nil
	}
	return decodeSSCredentials(user.Username())
}

func decodeSSCredentials(userInfo string) (string, string, error) {
	if colonIdx := strings.Index(userInfo, ":"); colonIdx != -1 {
		mBytes, errM := base64.StdEncoding.DecodeString(strutil.PadB64(userInfo[:colonIdx]))
		pBytes, errP := base64.StdEncoding.DecodeString(strutil.PadB64(userInfo[colonIdx+1:]))
		if errM == nil && errP == nil {
			return string(mBytes), string(pBytes), nil
		}
		return userInfo[:colonIdx], userInfo[colonIdx+1:], nil
	}

	b, err := base64.StdEncoding.DecodeString(strutil.PadB64(userInfo))
	if err == nil {
		parts := strings.SplitN(string(b), ":", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
	}
	return "", "", fmt.Errorf("ss parse failed: invalid userinfo (cannot decode method or password)")
}

func parseLegacySS(uri string) (map[string]any, error) {
	body := uri
	if strings.HasPrefix(strings.ToLower(body), "ss://") {
		body = body[5:]
	}
	if idx := strings.Index(body, "#"); idx != -1 {
		body = body[:idx]
	}
	if idx := strings.Index(body, "@"); idx != -1 {
		userInfo := body[:idx]
		hp := strings.Split(body[idx+1:], ":")
		if len(hp) < 2 {
			return nil, fmt.Errorf("ss parse failed: invalid host:port")
		}
		port, _ := strconv.Atoi(hp[1])

		var method, password string

		if colonIdx := strings.Index(userInfo, ":"); colonIdx != -1 {
			mBytes, errM := base64.StdEncoding.DecodeString(strutil.PadB64(userInfo[:colonIdx]))
			pBytes, errP := base64.StdEncoding.DecodeString(strutil.PadB64(userInfo[colonIdx+1:]))
			if errM == nil && errP == nil {
				method = string(mBytes)
				password = string(pBytes)
			}
		}

		if method == "" || password == "" {
			b, err := base64.StdEncoding.DecodeString(strutil.PadB64(userInfo))
			if err == nil {
				parts := strings.SplitN(string(b), ":", 2)
				if len(parts) == 2 {
					method = parts[0]
					password = parts[1]
				}
			}
		}

		if method == "" || password == "" {
			return nil, fmt.Errorf("ss parse failed: invalid userinfo (cannot decode method or password)")
		}

		name := ""
		if parts := strings.Split(hp[1], "#"); len(parts) > 1 {
			if dec, err := url.QueryUnescape(parts[1]); err == nil {
				name = dec
			} else {
				name = parts[1]
			}
		}

		return map[string]any{
			"name":     name,
			"type":     "ss",
			"server":   hp[0],
			"port":     port,
			"cipher":   normalizeSSCipher(method),
			"password": password,
		}, nil
	}
	return nil, fmt.Errorf("ss parse failed")
}

func applySSPlugin(out map[string]any, pluginRaw string) {
	pluginRaw = strings.TrimSpace(pluginRaw)
	if pluginRaw == "" {
		return
	}

	segments := strings.Split(pluginRaw, ";")
	plugin := strings.ToLower(strings.TrimSpace(segments[0]))
	rawOpts := map[string]string{}
	for _, segment := range segments[1:] {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		key, value, ok := strings.Cut(segment, "=")
		if !ok {
			rawOpts[strings.ToLower(segment)] = "true"
			continue
		}
		rawOpts[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}

	switch plugin {
	case "simple-obfs", "obfs-local", "obfs":
		out["plugin"] = "obfs"
		opts := map[string]any{}
		if mode := strutil.FirstNonEmpty(rawOpts["obfs"], rawOpts["mode"]); mode != "" {
			opts["mode"] = mode
		}
		if host := strutil.FirstNonEmpty(rawOpts["obfs-host"], rawOpts["host"]); host != "" {
			opts["host"] = host
		}
		if len(opts) > 0 {
			out["plugin-opts"] = opts
		}
	case "v2ray-plugin":
		out["plugin"] = plugin
		opts := map[string]any{}
		mode := rawOpts["mode"]
		if mode == "" {
			mode = "websocket"
		}
		opts["mode"] = mode
		for _, key := range []string{"host", "path", "fingerprint"} {
			if value := rawOpts[key]; value != "" {
				opts[key] = value
			}
		}
		for _, key := range []string{"tls", "mux", "skip-cert-verify"} {
			if value := rawOpts[key]; value == "true" || value == "1" {
				opts[key] = true
			}
		}
		out["plugin-opts"] = opts
	default:
		out["plugin"] = plugin
		if len(rawOpts) > 0 {
			opts := make(map[string]any, len(rawOpts))
			for key, value := range rawOpts {
				opts[key] = value
			}
			out["plugin-opts"] = opts
		}
	}
}

func formatSSPlugin(cfg map[string]any) string {
	plugin, _ := cfg["plugin"].(string)
	plugin = strings.TrimSpace(plugin)
	if plugin == "" {
		return ""
	}
	opts, _ := cfg["plugin-opts"].(map[string]any)
	if len(opts) == 0 {
		return plugin
	}

	var parts []string
	parts = append(parts, plugin)
	switch plugin {
	case "obfs":
		if mode, _ := opts["mode"].(string); mode != "" {
			parts = append(parts, "obfs="+mode)
		}
		if host, _ := opts["host"].(string); host != "" {
			parts = append(parts, "obfs-host="+host)
		}
	case "v2ray-plugin":
		if mode, _ := opts["mode"].(string); mode != "" {
			parts = append(parts, "mode="+mode)
		}
		if host, _ := opts["host"].(string); host != "" {
			parts = append(parts, "host="+host)
		}
		if path, _ := opts["path"].(string); path != "" {
			parts = append(parts, "path="+path)
		}
		if tls, _ := opts["tls"].(bool); tls {
			parts = append(parts, "tls")
		}
		if mux, _ := opts["mux"].(bool); mux {
			parts = append(parts, "mux")
		}
	default:
		for k, v := range opts {
			if b, ok := v.(bool); ok && b {
				parts = append(parts, k)
			} else if s := fmt.Sprintf("%v", v); s != "" {
				parts = append(parts, k+"="+s)
			}
		}
	}
	return strings.Join(parts, ";")
}
