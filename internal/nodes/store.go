package nodes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/db"
)

type Node struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	RawURI   string `json:"raw_uri"`
	Disabled bool   `json:"disabled"`
}

type NodeHealth struct { //nolint:govet
	SuccessCount        int     `json:"success_count"`
	FailCount           int     `json:"fail_count"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	LastTestMs          float64 `json:"last_test_ms"`
	LastTestError       string  `json:"last_test_error"`
	LastSuccessAt       int64   `json:"last_success_at"`
	LastFailAt          int64   `json:"last_fail_at"`
	CooldownUntil       int64   `json:"cooldown_until"`
	Last429At           int64   `json:"last_429_at"`
	RateLimitCount      int     `json:"rate_limit_count"`
	RecentUseCount      int     `json:"recent_use_count"`
	LastSelectedAt      int64   `json:"last_selected_at"`
	LastSubHealthyAt    int64   `json:"last_sub_healthy_at"` // 记录上一次处于亚健康状态的时间
	InFlight            int32   `json:"-"`                   // 当前并发连接数，不持久化
}

var (
	mu                 sync.Mutex                                 //nolint:gochecknoglobals
	nodeList           []Node                                     //nolint:gochecknoglobals
	healthMap          = make(map[string]*NodeHealth)             //nolint:gochecknoglobals
	nodeSources        = make(map[string]map[NodeSource]struct{}) //nolint:gochecknoglobals
	loaded             bool                                       //nolint:gochecknoglobals
	DeleteNodeCallback func(uri string)                           //nolint:gochecknoglobals
)

func ensureLoaded() {
	if loaded {
		return
	}
	loaded = true

	if db.GlobalDB == nil {
		return
	}

	// Load nodes
	rows, err := db.GlobalDB.Query("SELECT raw_uri, type, name, disabled FROM nodes")
	if err == nil {
		defer func() {
			_ = rows.Close()
		}()
		nodes := []Node{}
		for rows.Next() {
			var n Node
			if err := rows.Scan(&n.RawURI, &n.Type, &n.Name, &n.Disabled); err == nil {
				nodes = append(nodes, n)
			}
		}
		nodeList = nodes
	}

	sourceRows, err := db.GlobalDB.Query("SELECT raw_uri, source_type, source_id FROM node_sources")
	if err == nil {
		defer func() {
			_ = sourceRows.Close()
		}()
		for sourceRows.Next() {
			var rawURI string
			var source NodeSource
			if err := sourceRows.Scan(&rawURI, &source.Type, &source.ID); err == nil {
				addNodeSourceUnsafe(rawURI, source)
			}
		}
	}

	// Load health
	hRows, err := db.GlobalDB.Query("SELECT raw_uri, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until, last_429_at, rate_limit_count, last_sub_healthy_at FROM node_health")
	if err == nil {
		defer func() {
			_ = hRows.Close()
		}()
		for hRows.Next() {
			var uri string
			h := &NodeHealth{} //nolint:exhaustruct
			if err := hRows.Scan(&uri, &h.SuccessCount, &h.FailCount, &h.ConsecutiveFailures, &h.LastTestMs, &h.LastTestError, &h.LastSuccessAt, &h.LastFailAt, &h.CooldownUntil, &h.Last429At, &h.RateLimitCount, &h.LastSubHealthyAt); err == nil {
				healthMap[uri] = h
			}
		}
	}

	pruneHealthUnsafe()
}

func LoadNodes() []Node {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	log.Printf("[Nodes] 获取所有节点 (数量: %d)", len(nodeList))
	// Return an isolated snapshot. Callers frequently sort or annotate the
	// result; exposing the backing slice would race with concurrent updates and
	// allow accidental mutation of the store's authoritative state.
	snapshot := make([]Node, len(nodeList))
	copy(snapshot, nodeList)
	return snapshot
}

func LoadHealth() map[string]*NodeHealth {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	return cloneHealthMapUnsafe()
}

// writeAtomicJSON has been removed because it is unused

func saveNodesUnsafe() {
	if err := saveNodeStateUnsafe(); err != nil {
		log.Printf("[ERROR] Failed to save nodes: %v", err)
	}
}

type healthUpdate struct {
	uri string
	h   NodeHealth
}

// healthQueue coalesces updates by URI before handing them to the database.
// This keeps memory and goroutine usage bounded when SQLite is slow: callers
// never spawn a goroutine to wait for a full queue, and repeated updates for a
// URI replace its older pending value.
type healthQueue struct {
	mu      sync.Mutex
	pending map[string]NodeHealth
	wake    chan struct{}
}

const maxPendingHealthUpdates = 4096

var (
	healthQueueInst *healthQueue //nolint:gochecknoglobals
	healthOnce      sync.Once    //nolint:gochecknoglobals
)

func initHealthQueue() {
	q := &healthQueue{pending: make(map[string]NodeHealth), wake: make(chan struct{}, 1)}
	healthQueueInst = q
	go q.run()
}

func (q *healthQueue) enqueue(update healthUpdate) {
	q.mu.Lock()
	if _, exists := q.pending[update.uri]; !exists && len(q.pending) >= maxPendingHealthUpdates {
		// Apply bounded backpressure by dropping this new URI. Existing entries
		// (and therefore the latest state for already queued URIs) are retained.
		q.mu.Unlock()
		return
	}
	q.pending[update.uri] = update.h
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *healthQueue) drain() map[string]NodeHealth {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return nil
	}
	batch := q.pending
	q.pending = make(map[string]NodeHealth)
	return batch
}

func (q *healthQueue) requeue(batch map[string]NodeHealth) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for uri, h := range batch {
		// Preserve a newer value that arrived while the failed batch was being
		// written; only restore entries that are still absent.
		if _, exists := q.pending[uri]; !exists && len(q.pending) < maxPendingHealthUpdates {
			q.pending[uri] = h
		}
	}
}

func (q *healthQueue) run() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-q.wake:
		case <-ticker.C:
		}
		batch := q.drain()
		if len(batch) == 0 {
			continue
		}
		if !persistHealthBatch(batch) {
			// Keep updates bounded while the database is unavailable. The latest
			// value remains in memory and will be retried on a later wake/tick.
			q.requeue(batch)
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func persistHealthBatch(batch map[string]NodeHealth) bool {
	if db.GlobalDB == nil {
		// Database shutdown is terminal for this process; dropping the drained
		// batch avoids retaining an unbounded retry backlog after CloseDB.
		return true
	}
	tx, err := db.GlobalDB.Begin()
	if err != nil {
		log.Printf("[ERROR] Failed to begin health save transaction: %v", err)
		return false
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO node_health
		(raw_uri, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until, last_429_at, rate_limit_count, last_sub_healthy_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		log.Printf("[ERROR] Failed to prepare health save statement: %v", err)
		return false
	}
	for uri, h := range batch {
		if _, err := stmt.Exec(uri, h.SuccessCount, h.FailCount, h.ConsecutiveFailures, h.LastTestMs, h.LastTestError, h.LastSuccessAt, h.LastFailAt, h.CooldownUntil, h.Last429At, h.RateLimitCount, h.LastSubHealthyAt); err != nil {
			// A node may have been deleted while this update was queued. Skip
			// that stale row rather than rolling back the whole batch and retrying
			// it forever; transient transaction failures are still surfaced by
			// Begin/Commit and requeued by the worker.
			log.Printf("[WARN] Dropping stale health update for %q: %v", uri, err)
			if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
				_ = stmt.Close()
				_ = tx.Rollback()
				return false
			}
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
		log.Printf("[ERROR] Failed to commit health save transaction: %v", err)
		return false
	}
	return true
}

func saveHealthUnsafe() {
	if db.GlobalDB == nil {
		return
	}
	healthOnce.Do(initHealthQueue)
	q := healthQueueInst
	for uri, h := range healthMap {
		if h != nil {
			q.enqueue(healthUpdate{uri: uri, h: *h})
		}
	}
}

func updateSingleNodeHealthUnsafe(uri string, h *NodeHealth) {
	if db.GlobalDB == nil || h == nil {
		return
	}
	healthOnce.Do(initHealthQueue)
	healthQueueInst.enqueue(healthUpdate{uri: uri, h: *h})
}

func updateSingleNodeDisabledUnsafe(uri string, disabled bool) {
	if db.GlobalDB == nil {
		return
	}
	_, _ = db.GlobalDB.Exec("UPDATE nodes SET disabled = ? WHERE raw_uri = ?", disabled, uri)
}

type TestProgress struct {
	Running     bool   `json:"running"`
	Paused      bool   `json:"paused"`
	Terminated  bool   `json:"terminated"`
	Total       int    `json:"total"`
	Done        int    `json:"done"`
	OkCount     int    `json:"ok_count"`
	FailCount   int    `json:"fail_count"`
	CurrentNode string `json:"current_node"`
}

var (
	//nolint:gochecknoglobals // Test progress lock
	progressMu sync.RWMutex
	//nolint:gochecknoglobals // Test progress state
	globalProgress TestProgress
	//nolint:gochecknoglobals // Test progress control cond
	testControlCond = sync.NewCond(&progressMu)
)

func GetTestProgress() TestProgress {
	progressMu.RLock()
	defer progressMu.RUnlock()
	return globalProgress
}

func IsTestRunning() bool {
	progressMu.RLock()
	defer progressMu.RUnlock()
	return globalProgress.Running
}

func StartTestProgress(total int) bool {
	progressMu.Lock()
	defer progressMu.Unlock()
	if globalProgress.Running {
		return false
	}
	globalProgress = TestProgress{
		Running:     true,
		Paused:      false,
		Terminated:  false,
		Total:       total,
		Done:        0,
		OkCount:     0,
		FailCount:   0,
		CurrentNode: "准备中...",
	}
	return true
}

func UpdateTestProgress(nodeName string, ok bool) {
	progressMu.Lock()
	defer progressMu.Unlock()
	if !globalProgress.Running || globalProgress.Terminated {
		return
	}
	globalProgress.Done++
	if ok {
		globalProgress.OkCount++
	} else {
		globalProgress.FailCount++
	}
	globalProgress.CurrentNode = nodeName
}

func FinishTestProgress() {
	progressMu.Lock()
	defer progressMu.Unlock()
	globalProgress.Running = false
	globalProgress.Paused = false
	if globalProgress.Terminated {
		globalProgress.CurrentNode = "已终止"
	} else {
		globalProgress.CurrentNode = "测试完成"
	}
	testControlCond.Broadcast()
}

func PauseTestProgress() {
	progressMu.Lock()
	defer progressMu.Unlock()
	if globalProgress.Running && !globalProgress.Terminated {
		globalProgress.Paused = true
		globalProgress.CurrentNode = "已暂停..."
	}
}

func ResumeTestProgress() {
	progressMu.Lock()
	defer progressMu.Unlock()
	if globalProgress.Running && globalProgress.Paused {
		globalProgress.Paused = false
		globalProgress.CurrentNode = "恢复测试中..."
		testControlCond.Broadcast()
	}
}

func TerminateTestProgress() {
	progressMu.Lock()
	defer progressMu.Unlock()
	if globalProgress.Running {
		globalProgress.Terminated = true
		globalProgress.Paused = false
		globalProgress.CurrentNode = "正在终止..."
		testControlCond.Broadcast()
	}
}

func CheckTestControl() bool {
	progressMu.Lock()
	defer progressMu.Unlock()
	for globalProgress.Running && globalProgress.Paused && !globalProgress.Terminated {
		testControlCond.Wait()
	}
	return !globalProgress.Running || globalProgress.Terminated
}

// CheckTestControlContext is the cancellation-aware variant used by bounded
// batch workers. It deliberately avoids waiting on the legacy condition
// variable while paused, so a parent timeout can always release the worker.
func CheckTestControlContext(ctx context.Context) bool {
	for {
		progressMu.Lock()
		stop := !globalProgress.Running || globalProgress.Terminated
		paused := globalProgress.Paused && !globalProgress.Terminated && globalProgress.Running
		progressMu.Unlock()
		if stop {
			return true
		}
		if !paused {
			return false
		}
		t := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !t.Stop() {
				<-t.C
			}
			return true
		case <-t.C:
		}
	}
}

func nodeExistsUnsafe(uri string) bool {
	for _, n := range nodeList {
		if n.RawURI == uri {
			return true
		}
	}
	return false
}

func pruneHealthUnsafe() {
	for uri := range healthMap {
		found := false
		for _, n := range nodeList {
			if n.RawURI == uri {
				found = true
				break
			}
		}
		if !found {
			delete(healthMap, uri)
		}
	}
}

func DeleteNode(uri string) {
	mu.Lock()
	ensureLoaded()
	var kept []Node
	for _, n := range nodeList {
		if n.RawURI != uri {
			kept = append(kept, n)
		}
	}
	nodeList = kept
	delete(nodeSources, uri)
	delete(healthMap, uri)
	globalStickyPool.Evict(uri)
	saveNodesUnsafe()
	saveHealthUnsafe()
	cb := DeleteNodeCallback
	mu.Unlock() // 必须先解锁，避免底层的销毁回调查找节点名称时发生死锁
	if cb != nil {
		cb(uri)
	}
}

func DeleteDisabled() int {
	mu.Lock()
	ensureLoaded()
	var kept []Node
	removed := 0
	var removedURIs []string
	for _, n := range nodeList {
		if !n.Disabled {
			kept = append(kept, n)
		} else {
			removed++
			removedURIs = append(removedURIs, n.RawURI)
			delete(nodeSources, n.RawURI)
			delete(healthMap, n.RawURI)
			globalStickyPool.Evict(n.RawURI)
		}
	}
	nodeList = kept
	saveNodesUnsafe()
	saveHealthUnsafe()
	cb := DeleteNodeCallback
	mu.Unlock()

	if cb != nil {
		for _, u := range removedURIs {
			cb(u)
		}
	}
	return removed
}

func BatchUpdateNodesDisabled(uris []string, disabled bool) {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	targets := make(map[string]bool)
	for _, u := range uris {
		targets[u] = true
	}
	for i, n := range nodeList {
		if targets[n.RawURI] {
			nodeList[i].Disabled = disabled
		}
	}
	if db.GlobalDB != nil && len(uris) > 0 {
		tx, err := db.GlobalDB.Begin()
		if err == nil {
			stmt, _ := tx.Prepare("UPDATE nodes SET disabled = ? WHERE raw_uri = ?")
			if stmt != nil {
				for _, u := range uris {
					_, _ = stmt.Exec(disabled, u)
				}
				_ = stmt.Close()
			}
			_ = tx.Commit()
		}
	}
}

func BatchDeleteNodes(uris []string) {
	mu.Lock()
	ensureLoaded()
	targets := make(map[string]bool)
	for _, u := range uris {
		targets[u] = true
		delete(nodeSources, u)
		delete(healthMap, u)
		globalStickyPool.Evict(u)
	}
	var kept []Node
	for _, n := range nodeList {
		if !targets[n.RawURI] {
			kept = append(kept, n)
		}
	}
	nodeList = kept
	saveNodesUnsafe()
	saveHealthUnsafe()
	cb := DeleteNodeCallback
	mu.Unlock() // 防止在批量删除时引发卡死死锁

	if cb != nil {
		for _, u := range uris {
			cb(u)
		}
	}
}

func SortNodesByLatency() {
	mu.Lock()
	ensureLoaded()

	sort.Slice(nodeList, func(i, j int) bool {
		n1 := nodeList[i]
		n2 := nodeList[j]

		// 禁用的排在最后面
		if n1.Disabled != n2.Disabled {
			return !n1.Disabled
		}

		h1 := healthMap[n1.RawURI]
		h2 := healthMap[n2.RawURI]

		val1 := math.MaxFloat64
		if h1 != nil {
			if h1.ConsecutiveFailures > 0 {
				val1 = 1e6 + float64(h1.ConsecutiveFailures)*1000
			} else if h1.LastTestMs > 0 {
				val1 = h1.LastTestMs
			}
		}

		val2 := math.MaxFloat64
		if h2 != nil {
			if h2.ConsecutiveFailures > 0 {
				val2 = 1e6 + float64(h2.ConsecutiveFailures)*1000
			} else if h2.LastTestMs > 0 {
				val2 = h2.LastTestMs
			}
		}

		// 延迟一致的按名字自然排序
		if val1 == val2 {
			return n1.Name < n2.Name
		}
		return val1 < val2
	})

	saveNodesUnsafe()
	mu.Unlock()
}

func SortNodesByLatencyDesc() {
	mu.Lock()
	ensureLoaded()

	sort.Slice(nodeList, func(i, j int) bool {
		n1 := nodeList[i]
		n2 := nodeList[j]

		// 禁用的排在最后面
		if n1.Disabled != n2.Disabled {
			return !n1.Disabled
		}

		h1 := healthMap[n1.RawURI]
		h2 := healthMap[n2.RawURI]

		val1 := math.MaxFloat64
		if h1 != nil {
			if h1.ConsecutiveFailures > 0 {
				val1 = 1e6 + float64(h1.ConsecutiveFailures)*1000
			} else if h1.LastTestMs > 0 {
				val1 = h1.LastTestMs
			}
		}

		val2 := math.MaxFloat64
		if h2 != nil {
			if h2.ConsecutiveFailures > 0 {
				val2 = 1e6 + float64(h2.ConsecutiveFailures)*1000
			} else if h2.LastTestMs > 0 {
				val2 = h2.LastTestMs
			}
		}

		// 延迟一致的按名字自然排序
		if val1 == val2 {
			return n1.Name < n2.Name
		}
		// 这里改为降序，val1 > val2
		return val1 > val2
	})

	saveNodesUnsafe()
	mu.Unlock()
}

func GetNodeName(uri string) string {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	for _, n := range nodeList {
		if n.RawURI == uri {
			return n.Name
		}
	}
	return "Unknown"
}

func EnableNode(uri string) bool {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	found := false
	for i, n := range nodeList {
		if n.RawURI == uri {
			nodeList[i].Disabled = false
			if h, exists := healthMap[uri]; exists {
				h.CooldownUntil = 0
				h.LastSubHealthyAt = 0
				updateSingleNodeHealthUnsafe(uri, h)
			}
			updateSingleNodeDisabledUnsafe(uri, false)
			found = true
			break
		}
	}
	return found
}

func padB64(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "-", "+"), "_", "/")
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	return s
}

func parseNodeIdentity(rawURI string) (scheme, userinfo, host string, port int, ok bool) {
	if strings.HasPrefix(rawURI, "vmess://") {
		b64Str := rawURI[8:]
		if idx := strings.Index(b64Str, "?"); idx != -1 {
			b64Str = b64Str[:idx]
		}
		if idx := strings.Index(b64Str, "#"); idx != -1 {
			b64Str = b64Str[:idx]
		}
		b64Str = padB64(b64Str)
		if b, err := base64.StdEncoding.DecodeString(b64Str); err == nil {
			var d map[string]any
			if err := json.Unmarshal(b, &d); err == nil {
				id, _ := d["id"].(string)
				add, _ := d["add"].(string)
				portStr := fmt.Sprintf("%v", d["port"])
				p, _ := strconv.Atoi(portStr)
				return "vmess", id, add, p, true
			}
		}
		return "", "", "", 0, false
	}
	if strings.HasPrefix(rawURI, "ss://") {
		body := rawURI[5:]
		if idx := strings.Index(body, "#"); idx != -1 {
			body = body[:idx]
		}
		if idx := strings.Index(body, "@"); idx != -1 {
			b, err := base64.StdEncoding.DecodeString(padB64(body[:idx]))
			if err == nil {
				parts := strings.SplitN(string(b), ":", 2)
				if len(parts) >= 2 {
					hp := strings.Split(body[idx+1:], ":")
					if len(hp) >= 2 {
						p, _ := strconv.Atoi(hp[1])
						return "ss", parts[0] + ":" + parts[1], hp[0], p, true
					}
				}
			}
		}
		return "", "", "", 0, false
	}
	u, err := url.Parse(rawURI)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", "", 0, false
	}
	scheme = u.Scheme
	userinfo = ""
	if u.User != nil {
		userinfo = u.User.Username()
	}
	host = u.Hostname()
	port, _ = strconv.Atoi(u.Port())
	if port == 0 {
		port = 443
	}
	return scheme, userinfo, host, port, true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func RecordTest(uri string, ok bool, ms float64, errStr string) {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	if !nodeExistsUnsafe(uri) {
		// Ignore late results for nodes removed while a test was in flight. In
		// particular this prevents creating orphan health rows that violate the
		// node_health foreign key.
		return
	}
	h, exists := healthMap[uri]
	if !exists {
		h = &NodeHealth{} //nolint:exhaustruct
		healthMap[uri] = h
	}
	h.LastTestMs = ms
	h.LastTestError = errStr
	if ok {
		h.SuccessCount++
		h.ConsecutiveFailures = 0
		h.LastSuccessAt = time.Now().Unix()
		wasSubHealthy := h.LastSubHealthyAt > 0
		h.LastSubHealthyAt = 0
		h.CooldownUntil = 0
		h.Last429At = 0
		h.RateLimitCount = 0
		if wasSubHealthy {
			log.Printf("[Health] 节点 %s 恢复为健康 (延迟: %.0fms)", uri, ms)
		}
	} else {
		h.FailCount++
		h.ConsecutiveFailures++
		now := time.Now().Unix()
		h.LastFailAt = now
		failures := maxInt(1, h.ConsecutiveFailures)
		cooldown := minInt(1800, 30*(1<<minInt(failures-1, 6)))
		h.CooldownUntil = now + int64(cooldown)
		// 运行时网络失败只影响健康层级和冷却。Disabled 仅由管理页显式操作，
		// 避免一次临时拨号错误在数据库中留下永久禁用状态。
		h.LastSubHealthyAt = now
	}
	updateSingleNodeHealthUnsafe(uri, h)
}

func UpdateNodeTestResult(uri string, ok bool, ms float64, errStr string) {
	RecordTest(uri, ok, ms, errStr)
}

// RecordRateLimit 记录 429 冷却并递增计次，使重复 429 节点自然降权
func RecordRateLimit(uri string, cooldownSec int) {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	if !nodeExistsUnsafe(uri) {
		return
	}
	h, exists := healthMap[uri]
	if !exists {
		h = &NodeHealth{} //nolint:exhaustruct
		healthMap[uri] = h
	}
	now := time.Now().Unix()
	h.CooldownUntil = now + int64(cooldownSec)
	h.LastSubHealthyAt = now
	h.Last429At = now
	h.RateLimitCount++
	h.LastTestError = "429 Rate Limit"
	h.LastFailAt = now
	updateSingleNodeHealthUnsafe(uri, h)
}

//nolint:gochecknoglobals
var atomicRoundRobinIndex uint64

func getNodeTier(n Node, h *NodeHealth) int {
	if n.Disabled {
		return 3
	}
	if h != nil && (h.LastSubHealthyAt > 0 || h.CooldownUntil > time.Now().Unix()) {
		return 2
	}
	return 1
}

type tierCandidate struct {
	node     Node
	inFlight int32
	sticky   bool
}

func SelectForParallel(k int, topK int, debugMode bool, stickyBonusEnabled bool) []Node {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	now := time.Now().Unix()

	var tier1 []tierCandidate
	var tier2 []tierCandidate
	cooldownCount := 0

	for _, n := range nodeList {
		if n.Disabled {
			continue
		}
		h := healthMap[n.RawURI]
		if h != nil && h.CooldownUntil > now {
			cooldownCount++
			continue
		}
		tier := getNodeTier(n, h)
		inFlight := int32(0)
		if h != nil {
			inFlight = h.InFlight
		}
		sticky := stickyBonusEnabled && globalStickyPool.IsSticky(n.RawURI)
		switch tier {
		case 1:
			tier1 = append(tier1, tierCandidate{node: n, inFlight: inFlight, sticky: sticky})
		case 2:
			tier2 = append(tier2, tierCandidate{node: n, inFlight: inFlight, sticky: sticky})
		}
	}

	sortTier := func(candidates []tierCandidate) {
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].inFlight != candidates[j].inFlight {
				return candidates[i].inFlight < candidates[j].inFlight
			}
			if candidates[i].sticky != candidates[j].sticky {
				return candidates[i].sticky
			}
			hi := healthMap[candidates[i].node.RawURI]
			hj := healthMap[candidates[j].node.RawURI]
			ti := int64(0)
			if hi != nil {
				ti = hi.LastSelectedAt
			}
			tj := int64(0)
			if hj != nil {
				tj = hj.LastSelectedAt
			}
			if ti != tj {
				return ti < tj
			}
			return candidates[i].node.RawURI < candidates[j].node.RawURI
		})
	}
	sortTier(tier1)
	sortTier(tier2)

	if topK <= 0 {
		topK = 80
	}
	// topK 限制每轮可参与轮询的候选池；不能小于本轮实际需求，
	// 否则配置较小时会无故减少并发候选数量。
	candidateLimit := maxInt(k, topK)
	if len(tier1) > candidateLimit {
		tier1 = tier1[:candidateLimit]
		tier2 = nil
	} else {
		remaining := candidateLimit - len(tier1)
		if len(tier2) > remaining {
			tier2 = tier2[:remaining]
		}
	}

	samePriorityGroup := func(a, b tierCandidate) bool {
		return a.inFlight == b.inFlight && a.sticky == b.sticky
	}

	var selected []Node
	i := 0
	for i < len(tier1) && len(selected) < k {
		j := i
		for j < len(tier1) && samePriorityGroup(tier1[i], tier1[j]) {
			j++
		}
		group := tier1[i:j]
		offset := int(atomic.AddUint64(&atomicRoundRobinIndex, 1)) % len(group)
		for l := 0; l < len(group) && len(selected) < k; l++ {
			idx := (offset + l) % len(group)
			selected = append(selected, group[idx].node)
		}
		i = j
	}

	if len(selected) < k {
		i := 0
		for i < len(tier2) && len(selected) < k {
			j := i
			for j < len(tier2) && samePriorityGroup(tier2[i], tier2[j]) {
				j++
			}
			group := tier2[i:j]
			offset := int(atomic.AddUint64(&atomicRoundRobinIndex, 1)) % len(group)
			for l := 0; l < len(group) && len(selected) < k; l++ {
				idx := (offset + l) % len(group)
				selected = append(selected, group[idx].node)
			}
			i = j
		}
	}

	for _, s := range selected {
		if h := healthMap[s.RawURI]; h != nil {
			h.LastSelectedAt = now
			h.RecentUseCount++
		}
	}

	if debugMode {
		log.Printf("[Nodes] 选择并行节点 (需求: %d, 实际: %d, 冷却跳过: %d)", k, len(selected), cooldownCount)
	}
	return selected
}

func IncInFlight(uri string) {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	if h := healthMap[uri]; h != nil {
		h.InFlight++
	}
}

func DecInFlight(uri string) {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	if h := healthMap[uri]; h != nil {
		if h.InFlight > 0 {
			h.InFlight--
		}
	}
}

func GetAverageLatency() float64 {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	var sum float64
	var count int
	for _, n := range nodeList {
		if n.Disabled {
			continue
		}
		h := healthMap[n.RawURI]
		if h != nil && h.LastTestMs > 0 && h.CooldownUntil <= time.Now().Unix() {
			sum += h.LastTestMs
			count++
		}
	}
	if count == 0 {
		return 500.0
	}
	return sum / float64(count)
}
