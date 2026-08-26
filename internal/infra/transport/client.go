package transport

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"sync"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

type Header = http.Header
type Response = http.Response

type Session struct {
	client   tls_client.HttpClient
	ProxyURI string
	cleanup  func()
}

func (s *Session) Do(ctx context.Context, method, url string, header http.Header, body io.Reader) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)

	}
	req = req.WithContext(ctx)
	if header != nil {
		req.Header = header
	}

	return s.client.Do(req)
}

func (s *Session) DoAndRead(ctx context.Context, method, url string, header http.Header, body io.Reader) (int, []byte, error) {
	resp, err := s.Do(ctx, method, url, header, body)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp.StatusCode, nil, fmt.Errorf("error: %w", readErr)

	}
	return resp.StatusCode, data, nil
}

type StreamResponse struct { //nolint:govet
	StatusCode int
	Body       io.ReadCloser
	closeOnce  sync.Once
}

func (sr *StreamResponse) Close() {
	if sr.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, sr.Body)
	sr.closeOnce.Do(func() {
		_ = sr.Body.Close()
	})
}

// Abort 强制中断流连接并关闭 Body，绝不执行 drain 排空。
// 适用于取消、空闲超时和异常中断路径。
func (sr *StreamResponse) Abort() {
	if sr.Body == nil {
		return
	}
	sr.closeOnce.Do(func() {
		_ = sr.Body.Close()
	})
}

func (s *Session) DoStream(ctx context.Context, method, url string, header http.Header, body io.Reader) (*StreamResponse, error) {
	resp, err := s.Do(ctx, method, url, header, body)
	if err != nil {
		return nil, err
	}
	return &StreamResponse{StatusCode: resp.StatusCode, Body: resp.Body}, nil
}

func (s *Session) Close() {
	if s.client != nil {
		s.client.CloseIdleConnections()
	}
	if s.cleanup != nil {
		s.cleanup()
	}
}

type NetworkClient struct {
	dialer ProxyDialer
}

func NewNetworkClient(dialer ProxyDialer) *NetworkClient {
	return &NetworkClient{dialer: dialer}
}

func (c *NetworkClient) Dialer() ProxyDialer {
	return c.dialer
}

//nolint:gochecknoglobals
var browserProfiles = []profiles.ClientProfile{
	profiles.Chrome_144, profiles.Chrome_146,
}

func pickProfile() profiles.ClientProfile {
	return browserProfiles[rand.Intn(len(browserProfiles))]
}

// CreateSession 创建一个新 Session。
//
//   - secondHopURI 为空时：全局前置代理非空则经回环 SOCKS 第一跳，否则直连。
//   - secondHopURI 非空时：创建临时第二跳 sing-box（有前置时 detour 经第一跳）。
//     禁止对第二跳 URI 直接使用 WithProxyUrl 绕过第一跳。
func (c *NetworkClient) CreateSession(timeoutSec int, secondHopURI string, reqID string) (*Session, error) {
	prof := pickProfile()

	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(timeoutSec),
		tls_client.WithClientProfile(prof),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
	}

	var cleanup func()
	var err error

	if secondHopURI != "" {
		opts, cleanup, err = c.injectSecondHopDialer(opts, secondHopURI, reqID)
	} else {
		opts, err = c.injectEntryProxy(opts)
	}

	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, err
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("error: %w", err)

	}
	return &Session{client: client, ProxyURI: secondHopURI, cleanup: cleanup}, nil
}

// injectEntryProxy 配置 tls-client 经回环 SOCKS 走前置代理池（按请求轮询）。
func (c *NetworkClient) injectEntryProxy(opts []tls_client.HttpClientOption) ([]tls_client.HttpClientOption, error) {
	if c.dialer == nil {
		return opts, nil
	}
	addr := c.dialer.GetNextEntrySocksAddr()
	if addr == "" {
		return opts, nil
	}
	proxyURL := "socks5://" + addr
	return append(opts, tls_client.WithProxyUrl(proxyURL)), nil
}

// injectSecondHopDialer 配置 tls-client 通过临时第二跳 sing-box 拨号。
// 第二跳 box 内部有前置时 detour 经回环 SOCKS。
// 无论 URI 协议类型（VLESS/VMess/SS/HTTP/SOCKS等），统一使用 CreateDialer，
// 禁止对任何第二跳 URI 直接使用 tls_client.WithProxyUrl 绕过第一跳。
func (c *NetworkClient) injectSecondHopDialer(opts []tls_client.HttpClientOption, secondHopURI string, reqID string) ([]tls_client.HttpClientOption, func(), error) {
	dialCtx, cleanup, err := c.dialer.CreateDialer(secondHopURI, reqID)
	if err != nil {
		return nil, nil, fmt.Errorf("第二跳 Dialer 启动失败: %w", err)
	}
	opts = append(opts, tls_client.WithDialContext(dialCtx))
	return opts, cleanup, nil
}
