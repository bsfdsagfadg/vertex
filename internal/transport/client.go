package transport

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"strings"
	"sync/atomic"

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
	// 流式响应可能在收到 finish/usage 后主动结束扫描，而上游仍保持连接。
	// 这里不能同步排干 Body，否则 Close 会一直阻塞到上游关闭或 idle timeout。
	// Session 本身是单请求生命周期；直接关闭会让底层连接退出复用池，避免残留数据串流。
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
}

type NetworkClient struct {
	debugMode     bool
	entryProxyURI func() string
}

type entryProxyContextKey struct{}

type pinnedEntryProxy struct {
	uri string
}

type entryProxyPool struct {
	uris []string
	next atomic.Uint64
}

// WithEntryProxy pins a selected entry proxy to one top-level request.
func WithEntryProxy(ctx context.Context, entryURI string) context.Context {
	return context.WithValue(ctx, entryProxyContextKey{}, pinnedEntryProxy{uri: strings.TrimSpace(entryURI)})
}

// WithEntryProxyPool assigns one stable first-hop sequence to a top-level request.
// An empty sequence is an explicit direct route and must not fall back to the
// NetworkClient's dynamic selector.
func WithEntryProxyPool(ctx context.Context, entryURIs []string) context.Context {
	clean := make([]string, 0, len(entryURIs))
	for _, uri := range entryURIs {
		clean = append(clean, strings.TrimSpace(uri))
	}
	return context.WithValue(ctx, entryProxyContextKey{}, &entryProxyPool{uris: clean})
}

func entryProxyFromContext(ctx context.Context) (string, bool) {
	if value, ok := ctx.Value(entryProxyContextKey{}).(pinnedEntryProxy); ok {
		return value.uri, true
	}
	if pool, ok := ctx.Value(entryProxyContextKey{}).(*entryProxyPool); ok {
		if len(pool.uris) == 0 {
			return "", true
		}
		index := pool.next.Add(1) - 1
		return pool.uris[index%uint64(len(pool.uris))], true
	}
	return "", false
}

func NewNetworkClient(debugMode bool, entryProxyURI ...func() string) *NetworkClient {
	client := &NetworkClient{debugMode: debugMode}
	if len(entryProxyURI) > 0 {
		client.entryProxyURI = entryProxyURI[0]
	}
	return client
}

//nolint:gochecknoglobals // Read-only list of browser profiles
var browserProfiles = []profiles.ClientProfile{
	profiles.Chrome_144, profiles.Chrome_146,
}

func pickProfile() profiles.ClientProfile {
	return browserProfiles[rand.Intn(len(browserProfiles))]
}

// injectProxy 统一处理网络代理挂载，如果代理初始化失败，返回 error
func injectProxy(opts []tls_client.HttpClientOption, proxyURI, entryProxyURI, reqID string, debugMode bool) ([]tls_client.HttpClientOption, error) {
	if proxyURI == "" {
		return opts, nil
	}
	// 用户自定义的外部标准代理，直接使用 URL
	if entryProxyURI == "" || entryProxyURI == proxyURI {
		if strings.HasPrefix(proxyURI, "http://") || strings.HasPrefix(proxyURI, "https://") || strings.HasPrefix(proxyURI, "socks5://") {
			return append(opts, tls_client.WithProxyUrl(proxyURI)), nil
		}
	}

	// 第二跳通过 mihomo DialerForAPI 复用第一跳，形成 entry -> candidate 代理链。
	// 两跳相同则只构造一次，避免代理自引用。
	dialCtx, err := getOrStartProxyDialer(proxyURI, reqID, debugMode, entryProxyURI)
	if err != nil {
		return nil, fmt.Errorf("节点内部 Dialer 启动失败: %w", err)
	}

	opts = append(opts, tls_client.WithDialContext(dialCtx))
	return opts, nil
}

// CreateSession 创建一个新 Session：随机 Chrome 指纹 + 可选代理 + 独立 cookie jar。
func (c *NetworkClient) CreateSession(timeoutSec int, proxyURI string, reqID string) (*Session, error) {
	entryProxyURI := ""
	if proxyURI != "" && c.entryProxyURI != nil {
		entryProxyURI = strings.TrimSpace(c.entryProxyURI())
	}
	return c.createSession(timeoutSec, proxyURI, entryProxyURI, reqID)
}

// CreateSessionContext is CreateSession with an explicit request route.
func (c *NetworkClient) CreateSessionContext(ctx context.Context, timeoutSec int, proxyURI string, reqID string) (*Session, error) {
	entryProxyURI, pinned := entryProxyFromContext(ctx)
	if !pinned && proxyURI != "" && c.entryProxyURI != nil {
		entryProxyURI = strings.TrimSpace(c.entryProxyURI())
	}
	return c.createSession(timeoutSec, proxyURI, entryProxyURI, reqID)
}

// CreateSessionWithoutEntryProxy 创建只经过指定代理的隔离会话，用于验证入口代理候选本身。
func (c *NetworkClient) CreateSessionWithoutEntryProxy(timeoutSec int, proxyURI string, reqID string) (*Session, error) {
	return c.createSession(timeoutSec, proxyURI, "", reqID)
}

func (c *NetworkClient) createSession(timeoutSec int, proxyURI, entryProxyURI, reqID string) (*Session, error) {
	prof := pickProfile()
	log.Printf("[Transport] reqID: %s, Assigned TLS Profile: %s", reqID, prof.GetClientHelloStr())

	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(timeoutSec),
		tls_client.WithClientProfile(prof),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
	}

	// 使用 injectProxy 挂载代理，失败则直接熔断，坚决不走静默直连！
	var err error
	opts, err = injectProxy(opts, proxyURI, entryProxyURI, reqID, c.debugMode)
	if err != nil {
		return nil, err
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)

	}
	return &Session{client: client, ProxyURI: proxyURI}, nil
}
