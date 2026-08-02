// Package recaptcha 实现 Google reCAPTCHA Enterprise 匿名 token 的现抓现用。
//
// 流程：anchor iframe GET 抠出 base token，再 reload POST
// 拿到最终 token（rresp）。token 用于 batchGraphql 的 recaptchaToken 字段。
package recaptcha

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// recaptcha 相关硬编码常量（逐字节保持既定常量）。
const (
	recaptchaBase      = "https://www.google.com"
	siteKey            = "6LdCjtspAAAAAMcV4TGdWLJqRTEk1TfpdLqEnKdj"
	recaptchaCo        = "aHR0cHM6Ly9jb25zb2xlLmNsb3VkLmdvb2dsZS5jb206NDQz"
	recaptchaHl        = "zh-CN"
	recaptchaVFallback = "jdMmXeCQEkPbnFDy9T04NbgJ"
	recaptchaVh        = "6581054572"
	randomCharset      = "abcdefghijklmnopqrstuvwxyz0123456789"
)

var (
	versionMu sync.Mutex
	cachedVer string
	// 从 enterprise.js 提取 reCAPTCHA release 版本号（Google 定期滚动，不能硬编码）。
	versionRe = regexp.MustCompile(`releases/([A-Za-z0-9_-]{20,})`)

	// 从 anchor HTML 抠 base token。用正则而非 HTML 解析器（已实测可行、无需额外依赖）。
	tokenRe = regexp.MustCompile(`id="recaptcha-token"[^>]*value="([^"]+)"`)
	// 从 reload 响应抠最终 token。
	rrespRe = regexp.MustCompile(`rresp","(.*?)"`)
)

func randomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = randomCharset[rand.Intn(len(randomCharset))]
	}
	return string(b)
}

func fetchVersionFromSession(ctx context.Context, sess *transport.Session) (string, bool) {
	jsURL := recaptchaBase + "/recaptcha/enterprise.js"
	header := transport.XHRHeaders("", "*/*", recaptchaBase, recaptchaBase, "cross-site")
	status, body, err := sess.DoAndRead(ctx, "GET", jsURL, header, nil)
	if err != nil || status != 200 {
		return "", false
	}
	m := versionRe.FindSubmatch(body)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

func currentVersion(ctx context.Context, sess *transport.Session) (string, bool) {
	versionMu.Lock()
	if cachedVer != "" {
		ver := cachedVer
		versionMu.Unlock()
		return ver, true
	}
	versionMu.Unlock()

	ver, ok := fetchVersionFromSession(ctx, sess)
	if ok {
		versionMu.Lock()
		cachedVer = ver
		versionMu.Unlock()
		return ver, true
	}
	return "", false
}

func invalidateVersion() {
	versionMu.Lock()
	defer versionMu.Unlock()
	cachedVer = ""
}

// FetchRecaptchaToken 获取 Google reCAPTCHA token（隔离特征）。
//
// 最多 3 次重试，每次新建一个 short Timeout Session
// （即用即毁，FRESH_CONNECT 语义）。全部失败返回 ("", nil) —— 返回空值表示失败，
// 调用方按"空则换新/重试"处理。返回非空字符串即成功。
func FetchRecaptchaToken(ctx context.Context, net *transport.NetworkClient, proxyURI string, debugMode bool) (string, error) {
	// 【核心修改：解析并缓存节点友好名称】
	nodeName := nodes.GetNodeName(proxyURI)
	if proxyURI == "" {
		nodeName = "直连 (Direct)"
	}

	start := time.Now()
	for retry := 0; retry < 3; retry++ {
		// 【核心修改：将具体的节点名称明确输出在日志归属中】
		if debugMode {
			log.Printf("[Recaptcha] [节点: %s] 开始获取 reCAPTCHA token (尝试 %d/3)", nodeName, retry+1)
		}
		if token, ok := fetchOnce(ctx, net, proxyURI); ok {
			elapsed := time.Since(start)
			if debugMode {
				log.Printf("[Recaptcha] [节点: %s] 成功获取 reCAPTCHA token, 耗时: %d ms", nodeName, elapsed.Milliseconds())
			}
			return token, nil
		}
		if retry < 2 {
			delay := time.Duration((retry+1)*200) * time.Millisecond
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	elapsed := time.Since(start)
	if debugMode {
		log.Printf("[Recaptcha] [节点: %s] 3次尝试后获取 reCAPTCHA token 失败, 耗时: %d ms", nodeName, elapsed.Milliseconds())
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", fmt.Errorf("节点 %s 3次重试后仍无法获取 recaptcha token", nodeName)
}

// FetchRecaptchaTokenWithSession 在有 Session 的上下文中执行 anchor→reload 全流程。
// 这是共享核心操作，生产路径和节点测速路径均调用。
// 错误均为非 nil（网络失败、正则解析失败、reload 非 200），调用方按自身语义处理。
func FetchRecaptchaTokenWithSession(ctx context.Context, sess *transport.Session) (string, error) {
	ver, ok := currentVersion(ctx, sess)
	if !ok {
		ver = recaptchaVFallback // 动态拉取失败降级到硬编码（比硬失败更稳）
	}

	cb := randomString(10)
	anchorURL := fmt.Sprintf(
		"%s/recaptcha/enterprise/anchor?ar=1&k=%s&co=%s&hl=%s&v=%s&size=invisible&anchor-ms=20000&execute-ms=15000&cb=%s",
		recaptchaBase, siteKey, recaptchaCo, recaptchaHl, ver, cb,
	)

	_, anchorBody, err := sess.DoAndRead(ctx, "GET", anchorURL, transport.AnchorHeaders(), nil)
	if err != nil {
		return "", fmt.Errorf("GET anchor 失败: %w", err)
	}
	m := tokenRe.FindSubmatch(anchorBody)
	if m == nil {
		bodyStr := string(anchorBody)
		if len(bodyStr) > 500 {
			bodyStr = bodyStr[:500] + "..."
		}
		log.Printf("[Recaptcha] anchor token正则匹配失败, body前缀: %s", bodyStr)
		return "", fmt.Errorf("从 anchor HTML 解析 recaptcha-token 失败")
	}
	baseToken := string(m[1])

	form := url.Values{
		"v":      {ver},
		"reason": {"q"},
		"k":      {siteKey},
		"c":      {baseToken},
		"co":     {recaptchaCo},
		"hl":     {recaptchaHl},
		"size":   {"invisible"},
		"vh":     {recaptchaVh},
		"chr":    {""},
		"bg":     {""},
	}
	reloadURL := recaptchaBase + "/recaptcha/enterprise/reload?k=" + siteKey
	header := transport.XHRHeaders(
		"application/x-www-form-urlencoded;charset=UTF-8", "*/*",
		recaptchaBase, anchorURL, "same-origin",
	)

	status, reloadBody, err := sess.DoAndRead(ctx, "POST", reloadURL, header, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("POST reload 失败: %w", err)
	}
	if status != 200 {
		bodyStr := string(reloadBody)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200]
		}
		log.Printf("[Recaptcha] Reload 失败, HTTP 状态码: %d, 响应体前200字符: %s", status, bodyStr)
		return "", fmt.Errorf("reload 返回非 200 状态码: %d", status)
	}
	rm := rrespRe.FindSubmatch(reloadBody)
	if rm == nil {
		bodyStr := string(reloadBody)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200]
		}
		log.Printf("[Recaptcha] Reload 响应解析 rresp 失败, HTTP 状态码: %d, 响应体前200字符: %s", status, bodyStr)
		return "", fmt.Errorf("从 reload 响应解析 rresp 失败")
	}
	return string(rm[1]), nil
}

func fetchOnce(ctx context.Context, net *transport.NetworkClient, proxyURI string) (string, bool) {
	sess, err := net.CreateSession(15, proxyURI, "recaptcha")
	if err != nil {
		return "", false
	}
	defer sess.Close()

	token, err := FetchRecaptchaTokenWithSession(ctx, sess)
	if err != nil {
		invalidateVersion()
		return "", false
	}
	return token, true
}
