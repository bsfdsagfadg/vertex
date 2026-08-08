// Package recaptcha 实现 Google reCAPTCHA Enterprise 匿名 token 的现抓现用。
//
// 流程：anchor iframe GET 抠出 base token，再 reload POST
// 拿到最终 token（rresp）。token 用于 batchGraphql 的 recaptchaToken 字段。
package recaptcha

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
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
	// 从 anchor HTML 抠 base token。用正则而非 HTML 解析器（已实测可行、无需额外依赖）。
	tokenRe = regexp.MustCompile(`id="recaptcha-token"[^>]*value="([^"]+)"`)
	// 从 reload 响应抠最终 token。
	rrespRe = regexp.MustCompile(`rresp","(.*?)"`)
	// 从 enterprise.js 提取 reCAPTCHA release 版本号（Google 定期滚动，不能硬编码）。
	versionRe = regexp.MustCompile(`releases/([A-Za-z0-9_-]{20,})`)

	versionMu sync.Mutex //nolint:gochecknoglobals
	cachedVer string     //nolint:gochecknoglobals
)

// versionUA 拉取 enterprise.js 时使用的浏览器 UA（与 transport 包保持一致）。
const versionUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"

// fetchVersionFromJS 从 enterprise.js 提取当前 reCAPTCHA release 版本号。
//
// 版本号 Google 会定期滚动：硬编码旧版本会让 reload 换发的 token 第一次被
// batchGraphql 评估时失败（"Failed to verify action"），同 token 重试一次才过。
// 动态拉取当前版本后首帧即可通过（实测）。
func fetchVersionFromSession(ctx context.Context, sess *transport.Session) (string, error) {
	h := transport.Header{
		"user-agent":      {versionUA},
		"accept":          {"*/*"},
		"accept-language": {"zh-CN,zh;q=0.9,en;q=0.8"},
	}
	status, body, err := sess.DoAndRead(ctx, "GET", recaptchaBase+"/recaptcha/enterprise.js", h, nil)
	if err != nil {
		return "", fmt.Errorf("获取 reCAPTCHA 版本失败: %w", err)
	}
	if status != 200 {
		return "", fmt.Errorf("获取 reCAPTCHA 版本返回 HTTP %d", status)
	}
	m := versionRe.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("无法从 enterprise.js 解析 reCAPTCHA 版本")
	}
	return string(m[1]), nil
}

// currentVersion 返回缓存的 reCAPTCHA 版本号，未缓存则现场拉取。
func currentVersion(ctx context.Context, sess *transport.Session) (string, error) {
	versionMu.Lock()
	if cachedVer != "" {
		version := cachedVer
		versionMu.Unlock()
		return version, nil
	}
	versionMu.Unlock()

	version, err := fetchVersionFromSession(ctx, sess)
	if err != nil {
		return "", err
	}
	versionMu.Lock()
	if cachedVer == "" {
		cachedVer = version
	} else {
		version = cachedVer
	}
	versionMu.Unlock()
	return version, nil
}

// invalidateVersion 清除版本缓存：token 获取失败时调用，强制下一次重新拉取版本
// （旧版本号过期是 token 失败的首要原因）。
func invalidateVersion() {
	versionMu.Lock()
	cachedVer = ""
	versionMu.Unlock()
}

func randomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = randomCharset[rand.IntN(len(randomCharset))]
	}
	return string(b)
}

// FetchRecaptchaToken 获取 Google reCAPTCHA token（隔离特征）。
//
// 最多 3 次重试，每次新建一个 short Timeout Session
// （即用即毁，FRESH_CONNECT 语义）。返回非空字符串表示成功；全部失败返回显式错误。
func FetchRecaptchaToken(ctx context.Context, net *transport.NetworkClient, proxyURI string, debugMode bool) (string, error) {
	// 【核心修改：解析并缓存节点友好名称】
	nodeName := nodes.GetNodeName(proxyURI)
	if proxyURI == "" {
		nodeName = "直连 (Direct)"
	}

	start := time.Now()
	var lastErr error
	for retry := 0; retry < 3; retry++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// 【核心修改：将具体的节点名称明确输出在日志归属中】
		if debugMode {
			log.Printf("[Recaptcha] [节点: %s] 开始获取 reCAPTCHA token (尝试 %d/3)", nodeName, retry+1)
		}
		token, err := fetchOnce(ctx, net, proxyURI)
		if err == nil && token != "" {
			elapsed := time.Since(start)
			if debugMode {
				log.Printf("[Recaptcha] [节点: %s] 成功获取 reCAPTCHA token, 耗时: %d ms", nodeName, elapsed.Milliseconds())
			}
			return token, nil
		}
		lastErr = err
		invalidateVersion()
		if retry < 2 {
			timer := time.NewTimer(time.Duration(retry+1) * 200 * time.Millisecond)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return "", ctx.Err()
			case <-timer.C:
			}
		}
	}
	elapsed := time.Since(start)
	if debugMode {
		log.Printf("[Recaptcha] [节点: %s] 3次尝试后获取 reCAPTCHA token 失败, 耗时: %d ms", nodeName, elapsed.Milliseconds())
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("未知 reCAPTCHA 错误")
	}
	return "", fmt.Errorf("节点 %s 3 次重试后仍无法获取 reCAPTCHA token: %w", nodeName, lastErr)
}

func fetchOnce(ctx context.Context, net *transport.NetworkClient, proxyURI string) (string, error) {
	// rT 的获取路线由调用方明确指定；不能再次从全局入口选择器挂载第二个入口。
	reqID := transport.RequestIDFromContext(ctx)
	if reqID == "" {
		reqID = "recaptcha"
	}
	sess, err := net.CreateSessionWithoutEntryProxy(15, proxyURI, reqID)
	if err != nil {
		return "", fmt.Errorf("创建 reCAPTCHA Session 失败: %w", err)
	}
	defer sess.Close()
	return FetchRecaptchaTokenWithSession(ctx, sess)
}

// FetchRecaptchaTokenWithSession 在同一 Session 中完成版本、anchor 与 reload 请求。
func FetchRecaptchaTokenWithSession(ctx context.Context, sess *transport.Session) (string, error) {
	version, err := currentVersion(ctx, sess)
	if err != nil {
		version = recaptchaVFallback
	}
	cb := randomString(10)
	anchorURL := fmt.Sprintf(
		"%s/recaptcha/enterprise/anchor?ar=1&k=%s&co=%s&hl=%s&v=%s&size=invisible&anchor-ms=20000&execute-ms=15000&cb=%s",
		recaptchaBase, siteKey, recaptchaCo, recaptchaHl, version, cb,
	)

	status, anchorBody, err := sess.DoAndRead(ctx, "GET", anchorURL, transport.AnchorHeaders(), nil)
	if err != nil {
		return "", fmt.Errorf("GET anchor 失败: %w", err)
	}
	if status != 200 {
		return "", fmt.Errorf("anchor 返回 HTTP %d", status)
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
		"v":      {version},
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
		bodyText := truncateLogBody(reloadBody, 200)
		log.Printf("[Recaptcha] Reload 失败, HTTP 状态码: %d, 响应体前缀: %s", status, bodyText)
		return "", fmt.Errorf("reload 返回 HTTP %d", status)
	}
	rm := rrespRe.FindSubmatch(reloadBody)
	if rm == nil {
		log.Printf("[Recaptcha] Reload 响应解析失败, 响应体前缀: %s", truncateLogBody(reloadBody, 200))
		return "", fmt.Errorf("从 reload 响应解析 rresp 失败")
	}
	return string(rm[1]), nil
}

func truncateLogBody(body []byte, maxBytes int) string {
	if len(body) <= maxBytes {
		return string(body)
	}
	return string(body[:maxBytes]) + "..."
}
