package transport

import (
	"runtime"
	"strconv"
	"sync"
)

// irCacheMax 缓存条目上限：缓存仅解析成功的合法 URI（受节点总数与导入限额约束），
// 上限为纯防御，防恶意海量不重复 URI 注入导致无界增长；超限时整体重建（缓存仅是性能优化）。
const irCacheMax = 16384

// IRCache 是 ParsedNode 中间表示（IR）的解析缓存实例：
// 导入期 Warm/Prewarm 预热、拨号期 GetOrParse 命中、删除期 InvalidateOne/InvalidateBatch 失效、
// 全量重置 Clear。零值不可用，须经 NewIRCache 创建。
type IRCache struct {
	mu sync.RWMutex
	m  map[string]*ParsedNode
}

// NewIRCache 创建空缓存实例。
func NewIRCache() *IRCache {
	return &IRCache{m: make(map[string]*ParsedNode)}
}

// Warm 导入时预热单条；n 为 nil 时不缓存。
func (c *IRCache) Warm(n *ParsedNode) {
	if n == nil {
		return
	}
	c.mu.Lock()
	if len(c.m) >= irCacheMax {
		c.m = make(map[string]*ParsedNode)
	}
	c.m[n.RawURI] = n
	c.mu.Unlock()
}

// Clear 全量清空解析缓存（幂等），供全量重置/测试隔离使用。
func (c *IRCache) Clear() {
	c.mu.Lock()
	c.m = make(map[string]*ParsedNode)
	c.mu.Unlock()
}

// InvalidateOne 节点删除时清理单条缓存。
func (c *IRCache) InvalidateOne(uri string) {
	c.mu.Lock()
	delete(c.m, uri)
	c.mu.Unlock()
}

// InvalidateBatch 批量失效解析缓存（单次写锁内完成全部删除）。
func (c *IRCache) InvalidateBatch(uris []string) {
	if len(uris) == 0 {
		return
	}
	c.mu.Lock()
	for _, u := range uris {
		delete(c.m, u)
	}
	c.mu.Unlock()
}

// Prewarm 并发预热解析缓存：两阶段查漏（单次读锁）→ Worker Pool 无锁并发
// ParseURI → 单次写锁批量提交，避免持有锁时执行 CPU 密集解析。
func (c *IRCache) Prewarm(uris []string) {
	if len(uris) == 0 {
		return
	}
	c.mu.RLock()
	missing := make([]string, 0, len(uris))
	for _, u := range uris {
		if c.m[u] == nil {
			missing = append(missing, u)
		}
	}
	c.mu.RUnlock()
	if len(missing) == 0 {
		return
	}

	workers := runtime.GOMAXPROCS(0)
	if workers > len(missing) {
		workers = len(missing)
	}
	results := make(chan *ParsedNode)
	var collected []*ParsedNode
	collectDone := make(chan struct{})
	go func() {
		for n := range results {
			collected = append(collected, n)
		}
		close(collectDone)
	}()

	var wg sync.WaitGroup
	jobs := make(chan string)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range jobs {
				parsed, err := ParseURI(u)
				if err == nil && parsed != nil {
					results <- parsed
				}
			}
		}()
	}
	for _, u := range missing {
		jobs <- u
	}
	close(jobs)
	wg.Wait()
	close(results)
	<-collectDone

	// 单次写锁批量提交，保持 irCacheMax 上限防御语义（与 Warm 一致）。
	c.mu.Lock()
	if len(c.m) >= irCacheMax || len(collected) >= irCacheMax {
		c.m = make(map[string]*ParsedNode)
	}
	for i, n := range collected {
		if i >= irCacheMax {
			break
		}
		c.m[n.RawURI] = n
	}
	c.mu.Unlock()
}

// CheckSupportedBatch 单次读锁批量查询 URI 的 Supported 状态（未命中者经
// GetOrParse 逐条补算并缓存，解析失败视为不支持）。语义对齐 adminGetNodes
// 原逐条 GetOrParse 判断，但将 N 次锁抢占收敛为单次读锁。
func (c *IRCache) CheckSupportedBatch(uris []string) map[string]bool {
	out := make(map[string]bool, len(uris))
	if len(uris) == 0 {
		return out
	}
	c.mu.RLock()
	var missing []string
	for _, u := range uris {
		if n := c.m[u]; n != nil {
			out[u] = n.Supported
		} else {
			missing = append(missing, u)
		}
	}
	c.mu.RUnlock()
	for _, u := range missing {
		n, err := c.GetOrParse(u)
		out[u] = err == nil && n != nil && n.Supported
	}
	return out
}

// GetOrParse 优先读缓存，未命中则解析并缓存。只缓存成功结果，失败每次重试。
func (c *IRCache) GetOrParse(uri string) (*ParsedNode, error) {
	c.mu.RLock()
	n := c.m[uri]
	c.mu.RUnlock()
	if n != nil {
		return n, nil
	}
	parsed, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}
	c.Warm(parsed)
	return parsed, nil
}

// peek 测试辅助：同包读取缓存（不导出）。
func (c *IRCache) peek(uri string) *ParsedNode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.m[uri]
}

// irIdentityResolver 基于 IRCache 的节点身份解析器，
// 满足 node 域（nodes/entrynodes）的 IdentityResolver 消费方接口。
type irIdentityResolver struct {
	cache *IRCache
}

// NewIdentityResolver 构造基于给定缓存的节点身份解析器。
func NewIdentityResolver(cache *IRCache) *irIdentityResolver {
	return &irIdentityResolver{cache: cache}
}

// Identity 从 IR 计算去重键（type://cred@server:port）。
// 解析失败时返回 (rawURI, false)，调用方回退 rawURI 键。
func (r *irIdentityResolver) Identity(rawURI string) (string, bool) {
	n, err := r.cache.GetOrParse(rawURI)
	if err != nil || n == nil {
		return rawURI, false
	}
	return n.Type + "://" + credIdentity(n) + "@" + n.Server + ":" + strconv.Itoa(n.Port), true
}

// Supported 查询 URI 的能力标注（解析失败视为不支持）。
func (r *irIdentityResolver) Supported(rawURI string) bool {
	n, err := r.cache.GetOrParse(rawURI)
	return err == nil && n != nil && n.Supported
}

// credIdentity 提取各协议的凭据身份段（去重键组成部分）。
func credIdentity(n *ParsedNode) string {
	switch n.Type {
	case "vless", "vmess", "tuic":
		return n.UUID
	case "ss", "ssr":
		return n.Cipher + ":" + n.Password
	case "trojan", "hysteria2", "anytls":
		return n.Password
	case "socks5", "http", "ssh":
		return n.Username + ":" + n.Password
	case "hysteria":
		return n.AuthString
	default:
		return ""
	}
}
