package exitpool

import (
	"log"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/node/nodestore"
)

// ---- 健康度更新与测速结果落库 ----

type healthEvent struct {
	uri string
	h   NodeHealth
}

// StartHealthWorker 启动健康度异步写 worker（每 Manager 一条，随队列存活）。
func (m *Manager) StartHealthWorker() {
	go func() {
		for ev := range m.queue {
			m.updateSingleNodeHealthUnsafe(ev.uri, &ev.h)
		}
	}()
}

func (m *Manager) pushHealthEvent(uri string, h *NodeHealth) {
	if h == nil {
		return
	}
	select {
	case m.queue <- healthEvent{uri: uri, h: *h}:
	default:
		log.Printf("[Health] 警告: 健康度异步写队列已满，丢弃事件 %s", uri)
	}
}

func (m *Manager) updateSingleNodeHealthUnsafe(uri string, h *NodeHealth) {
	if m.database == nil || h == nil {
		return
	}
	_ = nodestore.UpsertHealth(m.database, "node_health", nodestore.HealthRow{
		RawURI:              uri,
		SuccessCount:        h.SuccessCount,
		FailCount:           h.FailCount,
		ConsecutiveFailures: h.ConsecutiveFailures,
		LastTestMs:          h.LastTestMs,
		LastTestError:       h.LastTestError,
		LastSuccessAt:       h.LastSuccessAt,
		LastFailAt:          h.LastFailAt,
		CooldownUntil:       h.CooldownUntil,
	})
}

func (m *Manager) updateSingleNodeDisabledUnsafe(uri string, disabled bool) {
	_ = nodestore.UpsertDisabled(m.database, "nodes", uri, disabled)
}

func (m *Manager) EnableNode(uri string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	found := false
	for i, n := range m.nodeList {
		if n.RawURI == uri {
			m.nodeList[i].Disabled = false
			if h, exists := m.healthMap[uri]; exists {
				h.CooldownUntil = 0
				h.LastSubHealthyAt = 0
				m.pushHealthEvent(uri, h)
			}
			m.updateSingleNodeDisabledUnsafe(uri, false)
			found = true
			break
		}
	}
	return found
}

func (m *Manager) RecordTest(uri string, ok bool, ms float64, errStr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	h, exists := m.healthMap[uri]
	if !exists {
		h = &NodeHealth{} //nolint:exhaustruct
		m.healthMap[uri] = h
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
		h.LastFailAt = time.Now().Unix()

		errLower := strings.ToLower(errStr)
		if strings.Contains(errLower, "dial") || strings.Contains(errLower, "refused") ||
			strings.Contains(errLower, "i/o timeout") || strings.Contains(errLower, "deadline exceeded") ||
			strings.Contains(errLower, "connection") {
			// 同步内存中的禁用状态，确保 LoadNodes / SelectForParallel 无需重启即可感知
			for i := range m.nodeList {
				if m.nodeList[i].RawURI == uri {
					m.nodeList[i].Disabled = true
					break
				}
			}
			m.updateSingleNodeDisabledUnsafe(uri, true)
		} else {
			h.LastSubHealthyAt = time.Now().Unix()
		}
	}
	m.pushHealthEvent(uri, h)
}

// RecordRateLimit 记录 429 冷却并递增计次，使重复 429 节点进入 Tier 2 亚健康。
func (m *Manager) RecordRateLimit(uri string, cooldownSec int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	h, exists := m.healthMap[uri]
	if !exists {
		h = &NodeHealth{} //nolint:exhaustruct
		m.healthMap[uri] = h
	}
	now := time.Now().Unix()
	h.LastSubHealthyAt = now
	h.Last429At = now
	h.RateLimitCount++
	h.LastTestError = "429 Rate Limit"
	h.LastFailAt = now
	h.CooldownUntil = time.Now().Unix() + int64(cooldownSec)
	m.pushHealthEvent(uri, h)
}

// ---- 节点选择与排序 ----

func getNodeTier(n Node, h *NodeHealth) int {
	if n.Disabled {
		return 3
	}
	if h != nil && h.LastSubHealthyAt > 0 {
		return 2
	}
	return 1
}

type tierCandidate struct {
	node     Node
	inFlight int32
}

// SelectForParallel 按健康分层严格选点：Tier 2 亚健康节点处于 429 冷却期时被跳过。
func (m *Manager) SelectForParallel(k int, debugMode bool) []Node {
	return m.selectForParallelCore(k, debugMode, false)
}

// SelectForParallelRelaxed 宽松选点：忽略 CooldownUntil 冷却（冷却只是保护措施，
// 竞速引擎在严格通道供给不足的非常时期按优先级强行补位）。
// Disabled 节点仍然排除；Tier 排序、InFlight/LastSelectedAt 权衡与 Phase 3 保护逻辑与严格路径一致。
func (m *Manager) SelectForParallelRelaxed(k int, debugMode bool) []Node {
	return m.selectForParallelCore(k, debugMode, true)
}

func (m *Manager) selectForParallelCore(k int, debugMode bool, ignoreCooldown bool) []Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	now := time.Now().Unix()

	var tier1 []tierCandidate
	var tier2 []tierCandidate

	for i, n := range m.nodeList {
		if n.Disabled {
			continue
		}
		if m.identity != nil && !m.identity.Supported(n.RawURI) {
			m.nodeList[i].Disabled = true
			m.updateSingleNodeDisabledUnsafe(n.RawURI, true)
			continue
		}
		h := m.healthMap[n.RawURI]
		tier := getNodeTier(n, h)
		inFlight := int32(0)
		if h != nil {
			inFlight = atomic.LoadInt32(&h.InFlight)
		}
		switch tier {
		case 1:
			tier1 = append(tier1, tierCandidate{n, inFlight})
		case 2:
			tier2 = append(tier2, tierCandidate{n, inFlight})
		}
	}

	// Sort Tier 1 by InFlight ascending, then by LastSelectedAt ascending (least recently used first), then by URI for deterministic round-robin grouping
	sort.Slice(tier1, func(i, j int) bool {
		if tier1[i].inFlight != tier1[j].inFlight {
			return tier1[i].inFlight < tier1[j].inFlight
		}
		hi := m.healthMap[tier1[i].node.RawURI]
		hj := m.healthMap[tier1[j].node.RawURI]
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
		return tier1[i].node.RawURI < tier1[j].node.RawURI
	})

	var selected []Node
	i := 0
	for i < len(tier1) && len(selected) < k {
		curInFlight := tier1[i].inFlight
		j := i
		for j < len(tier1) && tier1[j].inFlight == curInFlight {
			j++
		}
		group := tier1[i:j]
		offset := int(atomic.AddUint64(&m.rrIndex, 1)) % len(group)
		for l := 0; l < len(group) && len(selected) < k; l++ {
			idx := (offset + l) % len(group)
			selected = append(selected, group[idx].node)
		}
		i = j
	}

	// Phase 2: Tier 2 fallback when Tier 1 is insufficient
	if len(selected) < k {
		sort.Slice(tier2, func(i, j int) bool {
			if tier2[i].inFlight != tier2[j].inFlight {
				return tier2[i].inFlight < tier2[j].inFlight
			}
			hi := m.healthMap[tier2[i].node.RawURI]
			hj := m.healthMap[tier2[j].node.RawURI]
			si := int64(0)
			if hi != nil {
				si = hi.LastSelectedAt
			}
			sj := int64(0)
			if hj != nil {
				sj = hj.LastSelectedAt
			}
			if si != sj {
				return si < sj
			}
			ti := int64(0)
			tj := int64(0)
			if hi != nil {
				ti = hi.LastSubHealthyAt
			}
			if hj != nil {
				tj = hj.LastSubHealthyAt
			}
			return ti < tj
		})

		i := 0
		for i < len(tier2) && len(selected) < k {
			curInFlight := tier2[i].inFlight
			j := i
			for j < len(tier2) && tier2[j].inFlight == curInFlight {
				j++
			}
			group := tier2[i:j]
			offset := int(atomic.AddUint64(&m.rrIndex, 1)) % len(group)
			for l := 0; l < len(group) && len(selected) < k; l++ {
				idx := (offset + l) % len(group)
				h := m.healthMap[group[idx].node.RawURI]
				if !ignoreCooldown && h != nil && h.CooldownUntil > now {
					continue
				}
				selected = append(selected, group[idx].node)
			}
			i = j
		}
	}

	// Phase 3: 5 秒全局保护缓冲（尽力而为）
	findFreshReplacement := func(selected []Node, tier1 []tierCandidate) *Node {
		selectedSet := make(map[string]bool, len(selected))
		for _, s := range selected {
			selectedSet[s.RawURI] = true
		}
		var best *tierCandidate
		for i := range tier1 {
			tc := &tier1[i]
			if selectedSet[tc.node.RawURI] {
				continue
			}
			h := m.healthMap[tc.node.RawURI]
			if h == nil {
				if best == nil || tc.inFlight < best.inFlight {
					best = tc
				}
				continue
			}
			if !ignoreCooldown && h.CooldownUntil > now {
				continue
			}
			if h.LastSelectedAt == 0 || now-h.LastSelectedAt >= 5 {
				if best == nil || tc.inFlight < best.inFlight {
					best = tc
				}
			}
		}
		if best != nil {
			return &best.node
		}
		return nil
	}
	for idx := range selected {
		h := m.healthMap[selected[idx].RawURI]
		if h == nil || h.LastSelectedAt == 0 {
			continue
		}
		if now-h.LastSelectedAt >= 5 {
			continue
		}
		replacement := findFreshReplacement(selected, tier1)
		if replacement != nil {
			selected[idx] = *replacement
		}
	}

	for _, s := range selected {
		if h := m.healthMap[s.RawURI]; h != nil {
			h.LastSelectedAt = now
			h.RecentUseCount++
		}
	}

	if debugMode {
		log.Printf("[Nodes] 选择并行节点 (需求: %d, 实际: %d)", k, len(selected))
	}
	return selected
}

func (m *Manager) SortNodesByLatency() {
	m.mu.Lock()
	m.ensureLoaded()

	sort.Slice(m.nodeList, func(i, j int) bool {
		n1 := m.nodeList[i]
		n2 := m.nodeList[j]

		// 禁用的排在最后面
		if n1.Disabled != n2.Disabled {
			return !n1.Disabled
		}

		h1 := m.healthMap[n1.RawURI]
		h2 := m.healthMap[n2.RawURI]

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

	m.saveNodesUnsafe()
	m.mu.Unlock()
}

func (m *Manager) SortNodesByLatencyDesc() {
	m.mu.Lock()
	m.ensureLoaded()

	sort.Slice(m.nodeList, func(i, j int) bool {
		n1 := m.nodeList[i]
		n2 := m.nodeList[j]

		// 禁用的排在最后面
		if n1.Disabled != n2.Disabled {
			return !n1.Disabled
		}

		h1 := m.healthMap[n1.RawURI]
		h2 := m.healthMap[n2.RawURI]

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

	m.saveNodesUnsafe()
	m.mu.Unlock()
}
