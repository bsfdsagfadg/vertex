package transport

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"strings"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// Header 是 fhttp.Header 的别名，让 recaptcha/vertex 能构造请求头而不直接 import fhttp。
type Header = http.Header

// Response 是 fhttp.Response 的别名。
type Response = http.Response

// Session 封装一个独立的 tls-client，服务于单次逻辑请求。
type Session struct {
	client   tls_client.HttpClient
	ProxyURI string
	cleanup  func()
}

func (s *Session) Do(ctx context.Context, method, url string, header http.Header, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)

	}
	req = req.WithContext(ctx)
	if header != nil {
		req.Header = header
	}
	return s.client.Do(req) //nolint:wrapcheck
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
}

func (sr *StreamResponse) Close() {
	if sr.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, sr.Body)
	_ = sr.Body.Close()
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
	debugMode bool
	dialer    ProxyDialer
}

func NewNetworkClient(debugMode bool, dialer ProxyDialer) *NetworkClient {
	return &NetworkClient{debugMode: debugMode, dialer: dialer}
}

//nolint:gochecknoglobals // Read-only list of browser profiles
var browserProfiles = []profiles.ClientProfile{
	profiles.Chrome_124, profiles.Chrome_131,
}

func pickProfile() profiles.ClientProfile {
	return browserProfiles[rand.Intn(len(browserProfiles))]
}

// injectProxy 统一处理网络代理挂载，如果代理初始化失败，返回 error
// cleanup 返回的清理函数，需要调用方在不再使用连接时调用。
func (c *NetworkClient) injectProxy(opts []tls_client.HttpClientOption, proxyURI string, reqID string) ([]tls_client.HttpClientOption, func(), error) {
	if proxyURI == "" {
		return opts, nil, nil
	}
	// 用户自定义的外部标准代理，直接使用 URL
	if strings.HasPrefix(proxyURI, "http://") || strings.HasPrefix(proxyURI, "https://") || strings.HasPrefix(proxyURI, "socks5://") {
		return append(opts, tls_client.WithProxyUrl(proxyURI)), nil, nil
	}

	// 订阅节点，获取并挂载内部 Dialer
	dialCtx, cleanup, err := c.dialer.CreateDialer(proxyURI, reqID)
	if err != nil {
		return nil, nil, fmt.Errorf("节点内部 Dialer 启动失败: %w", err)
	}

	opts = append(opts, tls_client.WithDialContext(dialCtx))
	return opts, cleanup, nil
}

// CreateSession 创建一个新 Session：随机 Chrome 指纹 + 可选代理 + 独立 cookie jar。
func (c *NetworkClient) CreateSession(timeoutSec int, proxyURI string, reqID string) (*Session, error) {
	prof := pickProfile()
	log.Printf("[Transport] reqID: %s, Assigned TLS Profile: %s", reqID, prof.GetClientHelloStr())

	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(timeoutSec),
		tls_client.WithClientProfile(prof),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
	}

	// 使用 injectProxy 挂载代理，失败则直接熔断，坚决不走静默直连！
	var cleanup func()
	var err error
	opts, cleanup, err = c.injectProxy(opts, proxyURI, reqID)
	if err != nil {
		return nil, err
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("error: %w", err)

	}
	return &Session{client: client, ProxyURI: proxyURI, cleanup: cleanup}, nil
}
