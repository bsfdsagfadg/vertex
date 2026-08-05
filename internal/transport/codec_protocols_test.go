package transport

import (
	"encoding/base64"
	"testing"
)

func TestParseURIHy2KeepsPortRange(t *testing.T) {
	raw := "hy2://secret@203.10.99.51:20000?sni=www.bing.com&insecure=1&ports=20000-55000#demo"

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	// C15：hy2 端口归一为 "a:b" 格式
	if len(n.ServerPorts) != 1 || n.ServerPorts[0] != "20000:55000" {
		t.Fatalf("expected ServerPorts [20000:55000], got %#v", n.ServerPorts)
	}
	if n.TLS == nil || n.TLS.ServerName != "www.bing.com" {
		t.Fatalf("expected sni www.bing.com, got %#v", n.TLS)
	}
	if n.TLS.Insecure != true {
		t.Fatalf("expected insecure=true, got %#v", n.TLS)
	}
	if n.Password != "secret" {
		t.Fatalf("expected password secret, got %q", n.Password)
	}
}

func TestParseURISSR(t *testing.T) {
	ssrDecoded := "1.2.3.4:1234:origin:aes-256-cfb:tls1.2_ticket_auth:dGVzdHBhc3M=?remarks=my-ssr"
	raw := "ssr://" + base64.StdEncoding.EncodeToString([]byte(ssrDecoded))

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "ssr" {
		t.Fatalf("expected type ssr, got %q", n.Type)
	}
	if n.Name != "my-ssr" {
		t.Fatalf("expected name my-ssr, got %q", n.Name)
	}
	if n.Server != "1.2.3.4" {
		t.Fatalf("expected server 1.2.3.4, got %q", n.Server)
	}
	if n.Port != 1234 {
		t.Fatalf("expected port 1234, got %d", n.Port)
	}
	if n.Cipher != "aes-256-cfb" {
		t.Fatalf("expected cipher aes-256-cfb, got %q", n.Cipher)
	}
	if n.Password != "testpass" {
		t.Fatalf("expected password testpass, got %q", n.Password)
	}
	if n.Protocol != "origin" {
		t.Fatalf("expected protocol origin, got %q", n.Protocol)
	}
	if n.Obfs != "tls1.2_ticket_auth" {
		t.Fatalf("expected obfs tls1.2_ticket_auth, got %q", n.Obfs)
	}
}

func TestParseURISSRParamKeyCompat(t *testing.T) {
	// C5：三键兼容读（obfsparam|obfs_param|obfs-param、protoparam|protocol_param|protocol-param）
	tests := []struct {
		name       string
		paramQuery string
		wantObfs   string
		wantProto  string
	}{
		{"underscore keys", "?obfs_param=op2&protocol_param=pp2", "op2", "pp2"},
		{"canonical keys", "?obfsparam=op1&protoparam=pp1", "op1", "pp1"},
		{"dash keys", "?obfs-param=op3&protocol-param=pp3", "op3", "pp3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded := "1.2.3.4:1234:origin:aes-256-cfb:plain:dGVzdHBhc3M=" + tt.paramQuery
			raw := "ssr://" + base64.StdEncoding.EncodeToString([]byte(decoded))
			n, err := ParseURI(raw)
			if err != nil {
				t.Fatalf("ParseURI returned error: %v", err)
			}
			if n.ObfsParam != tt.wantObfs {
				t.Fatalf("ObfsParam = %q, want %q", n.ObfsParam, tt.wantObfs)
			}
			if n.ProtocolParam != tt.wantProto {
				t.Fatalf("ProtocolParam = %q, want %q", n.ProtocolParam, tt.wantProto)
			}
		})
	}
}

func TestParseURIShadowsocksr(t *testing.T) {
	ssrDecoded := "10.0.0.1:443:auth_aes128_md5:chacha20-ietf:http_simple:MTIzNDU2Nzg5MA==?remarks=ssr-node"
	raw := "shadowsocksr://" + base64.StdEncoding.EncodeToString([]byte(ssrDecoded))

	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "ssr" {
		t.Fatalf("expected type ssr, got %q", n.Type)
	}
	if n.Name != "ssr-node" {
		t.Fatalf("expected name ssr-node, got %q", n.Name)
	}
	if n.Password != "1234567890" {
		t.Fatalf("expected password 1234567890, got %q", n.Password)
	}
}

func TestParseURIHysteria(t *testing.T) {
	raw := "hysteria://host.example.com:443?auth=mysecret&obfs=xplus&sni=sni.example.com&insecure=1&alpn=h3#hysteria-node"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "hysteria" {
		t.Fatalf("expected type hysteria, got %q", n.Type)
	}
	if n.Name != "hysteria-node" {
		t.Fatalf("expected name hysteria-node, got %q", n.Name)
	}
	if n.Server != "host.example.com" {
		t.Fatalf("expected server host.example.com, got %q", n.Server)
	}
	if n.Port != 443 {
		t.Fatalf("expected port 443, got %d", n.Port)
	}
	if n.AuthString != "mysecret" {
		t.Fatalf("expected auth_str mysecret, got %q", n.AuthString)
	}
	if n.TLS == nil || !n.TLS.Enabled {
		t.Fatalf("expected tls enabled, got %#v", n.TLS)
	}
	if n.TLS.ServerName != "sni.example.com" {
		t.Fatalf("expected sni sni.example.com, got %q", n.TLS.ServerName)
	}
	if n.Obfs != "xplus" {
		t.Fatalf("expected obfs xplus, got %q", n.Obfs)
	}
	if !n.TLS.Insecure {
		t.Fatalf("expected insecure=true")
	}
	if len(n.TLS.ALPN) != 1 || n.TLS.ALPN[0] != "h3" {
		t.Fatalf("expected alpn [h3], got %#v", n.TLS.ALPN)
	}
}

func TestParseURIAnyTLS(t *testing.T) {
	raw := "anytls://mypassword@host.example.com:443#my-anytls"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.Type != "anytls" {
		t.Fatalf("expected type anytls, got %q", n.Type)
	}
	if n.Name != "my-anytls" {
		t.Fatalf("expected name my-anytls, got %q", n.Name)
	}
	if n.Server != "host.example.com" {
		t.Fatalf("expected server host.example.com, got %q", n.Server)
	}
	if n.Port != 443 {
		t.Fatalf("expected port 443, got %d", n.Port)
	}
	if n.Password != "mypassword" {
		t.Fatalf("expected password mypassword, got %q", n.Password)
	}
	if n.TLS == nil || !n.TLS.Enabled {
		t.Fatalf("expected tls enabled, got %#v", n.TLS)
	}
	if n.TLS.ServerName != "host.example.com" {
		t.Fatalf("expected sni host.example.com, got %q", n.TLS.ServerName)
	}
}

func TestParseURITuicPeerPriority(t *testing.T) {
	// tuic：sni 缺失时 peer 优先（对齐 builder）
	raw := "tuic://uuid:pass@x.example.com:443?peer=peer.example.com"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.TLS == nil || n.TLS.ServerName != "peer.example.com" {
		t.Fatalf("expected ServerName peer.example.com, got %#v", n.TLS)
	}
	if n.UUID != "uuid" || n.Password != "pass" {
		t.Fatalf("expected uuid/password, got uuid=%q password=%q", n.UUID, n.Password)
	}
}

func TestParseURITuicCongestionControl(t *testing.T) {
	raw := "tuic://uuid@x.example.com:443?congestion_control=bbr&udp_relay_mode=native"
	n, err := ParseURI(raw)
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}
	if n.CongestionControl != "bbr" {
		t.Fatalf("expected congestion_control bbr, got %q", n.CongestionControl)
	}
	if n.UDPRelayMode != "native" {
		t.Fatalf("expected udp_relay_mode native, got %q", n.UDPRelayMode)
	}
}

func TestParseURISSH(t *testing.T) {
	for _, tt := range []struct {
		name string
		uri  string
		user string
		pass string
		pk   string
		psk  string
	}{
		{"password auth", "ssh://root:secret@203.0.113.9:22#ssh-1", "root", "secret", "", ""},
		{"key auth", "ssh://tunnel@203.0.113.9:2222?pk=QUJDRA%3D%3D&psk=pp", "tunnel", "", "QUJDRA==", "pp"},
		{"default port", "ssh://root@203.0.113.9", "root", "", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			n, err := ParseURI(tt.uri)
			if err != nil {
				t.Fatalf("ParseURI returned error: %v", err)
			}
			if n.Type != "ssh" || n.Server != "203.0.113.9" {
				t.Fatalf("unexpected node: type=%q server=%q", n.Type, n.Server)
			}
			if n.Username != tt.user || n.Password != tt.pass {
				t.Fatalf("expected user=%q pass=%q, got %q/%q", tt.user, tt.pass, n.Username, n.Password)
			}
			if n.SSHPrivateKey != tt.pk || n.SSHPrivateKeyPassphrase != tt.psk {
				t.Fatalf("expected pk=%q psk=%q, got %q/%q", tt.pk, tt.psk, n.SSHPrivateKey, n.SSHPrivateKeyPassphrase)
			}
			if !n.Supported {
				t.Fatalf("expected Supported=true, got %q", n.UnsupportedReason)
			}
		})
	}
}
