package importer

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/infra/transport"
)

func TestParseImportedNodesSupportsV2RayNInnerURI(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"ConfigType":     5,
		"Remarks":        "demo",
		"Address":        "cf.example.com",
		"Port":           443,
		"Password":       "12345678-1234-1234-1234-123456789012",
		"StreamSecurity": "tls",
		"Sni":            "edge.example.com",
		"Fingerprint":    "chrome",
		"Network":        "ws",
		"ProtoExtraObj":  map[string]any{"VlessEncryption": "none"},
		"TransportExtraObj": map[string]any{
			"Path": "/ws",
			"Host": "edge.example.com",
		},
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	text := "v2rayn://vless/" + base64.RawURLEncoding.EncodeToString(payload)
	imported := ParseImportedNodes(text)
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}

	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out.Type != "vless" || out.TLS == nil || out.TLS.ServerName != "edge.example.com" {
		t.Fatalf("unexpected imported node: %#v", out)
	}
	if out.Transport == nil || out.Transport.Type != "ws" || out.Transport.Path != "/ws" {
		t.Fatalf("ws-opts not preserved: %#v", out.Transport)
	}
}

func TestParseImportedNodesSupportsSIP008(t *testing.T) {
	text := `{"servers":[{"remarks":"ss demo","server":"1.2.3.4","server_port":8388,"method":"aes-128-gcm","password":"secret"}]}`

	imported := ParseImportedNodes(text)
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}

	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out.Type != "ss" || out.Port != 8388 {
		t.Fatalf("unexpected imported node: %#v", out)
	}
}

func TestParseImportedNodesSupportsV2RayOutbounds(t *testing.T) {
	text := `{
  "outbounds": [
    {
      "tag": "demo",
      "protocol": "vmess",
      "settings": {
        "vnext": [
          {
            "address": "v2ray.cool",
            "port": 443,
            "users": [
              {
                "id": "a3482e88-686a-4a58-8126-99c9df64b7bf",
                "security": "auto",
                "alterId": 0
              }
            ]
          }
        ]
      },
      "streamSettings": {
        "network": "ws",
        "security": "tls",
        "tlsSettings": {
          "serverName": "edge.example.com",
          "fingerprint": "chrome",
          "allowInsecure": true,
          "alpn": "h2"
        },
        "wsSettings": {
          "path": "/ws",
          "headers": {
            "Host": "edge.example.com"
          }
        }
      }
    }
  ]
}`

	imported := ParseImportedNodes(text)
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}

	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out.Type != "vmess" || out.TLS == nil || out.TLS.ServerName != "edge.example.com" {
		t.Fatalf("unexpected imported node: %#v", out)
	}
	if out.Transport == nil || out.Transport.Type != "ws" {
		t.Fatalf("ws transport missing: %#v", out.Transport)
	}
	headers := out.Transport.Headers["Host"]
	if len(headers) == 0 || headers[0] != "edge.example.com" {
		t.Fatalf("unexpected ws headers: %#v", out.Transport)
	}
}

// tuicNodeRawURI 构造 v2rayn://tuic 导入文本。
func tuicNodeRawURI(username string) string {
	payload := map[string]any{
		"ConfigType": 8,
		"Remarks":    "tuic demo",
		"Address":    "cf.example.com",
		"Port":       443,
		"Password":   "secret-token",
		"Udp":        true,
	}
	if username != "" {
		payload["Username"] = username
	}
	b, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return "v2rayn://tuic/" + base64.RawURLEncoding.EncodeToString(b)
}

func TestParseImportedNodesTUICWithoutUsername(t *testing.T) {
	imported := ParseImportedNodes(tuicNodeRawURI(""))
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}

	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out.Type != "tuic" {
		t.Fatalf("unexpected imported node: %#v", out)
	}
	// 无用户名时 uuid 与 token 均取 password，URI 形如 tuic://secret-token:secret-token@host:port
	if out.UUID != "secret-token" || out.Password != "secret-token" {
		t.Fatalf("unexpected tuic userinfo: %q:%q", out.UUID, out.Password)
	}
}

func TestParseImportedNodesTUICWithUsername(t *testing.T) {
	imported := ParseImportedNodes(tuicNodeRawURI("user-uuid"))
	if len(imported) != 1 {
		t.Fatalf("expected 1 node, got %d", len(imported))
	}

	out, err := transport.ParseURI(imported[0].RawURI)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if out.Type != "tuic" {
		t.Fatalf("unexpected imported node: %#v", out)
	}
	if out.UUID != "user-uuid" || out.Password != "secret-token" {
		t.Fatalf("unexpected tuic userinfo: %q:%q", out.UUID, out.Password)
	}
}
