package transport

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/bsfdsagfadg/vertex/internal/config"
)

// Header 是 fhttp.Header 的别名，让 recaptcha/vertex 能构造请求头而不直接 import fhttp。
type Header = http.Header

// Response 是 fhttp.Response 的别名。
type Response = http.Response

// Session 封装一个独立的 tls-client，服务于单次逻辑请求。
type Session struct {
	client   tls_client.HttpClient
	ProxyURI string
	// entryURI 是本会话第一跳使用的候选入口（来自 proxy_url_candidates）。
	// 仅当该入口确实来自候选池时非空，用于运行期失败熔断上报。
	entryURI string
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
	resp, err := s.client.Do(req) //nolint:wrapcheck
	if err != nil {
		// 全链路诊断：失败日志标注经入口候选（脱敏）或直连，便于定位"入口失效 vs 目标站被墙"。
		// 注意：请求失败不再归因入口健康（v4 方案）——链式 entry→node→target 中 node 段失败
		// 是坏节点问题，与入口能否拨通无关；入口健康由 P9 独立拨测维护。失败只由调用方按节点记账。
		if s.entryURI != "" {
			log.Printf("[Transport] 请求失败 [经入口候选 %s]: %v", entryURILogLabel(s.entryURI), err)
		} else {
			log.Printf("[Transport] 请求失败 [直连]: %v", err)
		}
		return nil, err
	}
	return resp, nil
}

// entryURILogLabel 脱敏入口 URI 仅供日志输出：只打印 #name（存在时）或 scheme://host，
// 不携带 userinfo 里的口令等敏感信息。
func entryURILogLabel(entryURI string) string {
	u, err := url.Parse(entryURI)
	if err != nil || u.Host == "" {
		return "unknown"
	}
	if name := u.Fragment; name != "" {
		if decoded, derr := url.QueryUnescape(name); derr == nil {
			name = decoded
		}
		return "#" + name
	}
	return u.Scheme + "://" + u.Host
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

func NewNetworkClient(debugMode bool, entryProxyURI ...func() string) *NetworkClient {
	client := &NetworkClient{debugMode: debugMode}
	if len(entryProxyURI) > 0 {
		client.entryProxyURI = entryProxyURI[0]
	}
	return client
}

//nolint:gochecknoglobals // Round-Robin 轮询游标（原子递增，无需持久化）
var proxyCandidateRR uint64

// 运行期熔断机制已整体废弃（v4 方案）：候选入口健康 = 能拨通出站代理形成链式代理，
// 与业务请求成败无关，由 P9 独立拨测维护（面板手动 + 后台周期探测）。
// 请求级失败一律不归因入口（坏节点不再连坐入口），因此已删除
// candidateFailThreshold/candidateFailWindow/candidateCooldown 常量与
// candidateEntryStates/reportEntryProxyFailure/reportEntryProxySuccess。
// 候选筛选仅剩 !Disabled 一条规则。

// GetNextProxyCandidate 从配置的 proxy_url_candidates 中轮询挑选一个启用且不在冷却期的候选前置代理 URI。
// 候选 = 用户显式配置的高复用必经入口（对齐 master 常驻语义）：
// 启用且不在 60s 冷却期内即可参与轮询——不要求测速通过（LastTestOK 不再作筛选）、
// 不按测速新鲜度剔除陈旧候选、也不再受运行期熔断影响。
// 仅当候选池为空（未配置候选、全部被禁用或均在冷却期）时返回 ""，调用方回退默认 cfg.ProxyURL 或直连。
func GetNextProxyCandidate() string {
	candidates := config.Load().ProxyURLCandidates
	active := make([]string, 0, len(candidates))
	now := time.Now().Unix()
	for _, c := range candidates {
		if c.RawURI == "" || c.Disabled || c.CooldownUntil > now {
			continue
		}
		active = append(active, c.RawURI)
	}
	if len(active) == 0 {
		if len(candidates) > 0 {
			log.Printf("[Transport] 入口候选池为空（全部禁用或冷却中），回退默认入口/直连")
		}
		return ""
	}
	idx := atomic.AddUint64(&proxyCandidateRR, 1)
	return active[(idx-1)%uint64(len(active))]
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
	// proxyURI=="" 且 entryProxyURI==""：纯直连。
	if proxyURI == "" && entryProxyURI == "" {
		return opts, nil
	}
	// 用户自定义的外部标准代理，直接使用 URL
	if proxyURI != "" && (entryProxyURI == "" || entryProxyURI == proxyURI) {
		if strings.HasPrefix(proxyURI, "http://") || strings.HasPrefix(proxyURI, "https://") || strings.HasPrefix(proxyURI, "socks5://") {
			return append(opts, tls_client.WithProxyUrl(proxyURI)), nil
		}
	}

	// proxyURI=="" 但入口候选非空：以入口自身作为唯一跳点构建 DialContext（入口优先）。
	if proxyURI == "" {
		dialCtx, err := getOrStartProxyDialer(entryProxyURI, reqID, debugMode, "")
		if err != nil {
			return nil, fmt.Errorf("入口候选 Dialer 启动失败: %w", err)
		}
		return append(opts, tls_client.WithDialContext(dialCtx)), nil
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
	entryFromCandidate := false
	if proxyURI != "" && c.entryProxyURI != nil {
		entryProxyURI = strings.TrimSpace(c.entryProxyURI())
	}
	// 第一跳（Entry Proxy）优先从候选池轮询挑选启用候选（配置即必用，不要求测速通过）；
	// 候选池为空时回退默认 cfg.ProxyURL，再为空则为直连。
	if candidate := GetNextProxyCandidate(); candidate != "" {
		entryProxyURI = candidate
		entryFromCandidate = true
	}
	return c.createSession(timeoutSec, proxyURI, entryProxyURI, reqID, entryFromCandidate)
}

// CreateSessionWithoutEntryProxy 创建只经过指定代理的隔离会话，用于验证入口代理候选本身。
func (c *NetworkClient) CreateSessionWithoutEntryProxy(timeoutSec int, proxyURI string, reqID string) (*Session, error) {
	return c.createSession(timeoutSec, proxyURI, "", reqID, false)
}

func (c *NetworkClient) createSession(timeoutSec int, proxyURI, entryProxyURI, reqID string, entryFromCandidate bool) (*Session, error) {
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
	sessEntryURI := ""
	if entryFromCandidate {
		sessEntryURI = entryProxyURI
	}
	return &Session{client: client, ProxyURI: proxyURI, entryURI: sessEntryURI}, nil
}
