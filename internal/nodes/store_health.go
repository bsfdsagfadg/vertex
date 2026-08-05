package nodes

import (
	"log"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/db"
)

// ---- 健康度更新与测速结果落库 ----

func updateSingleNodeHealthUnsafe(uri string, h *NodeHealth) {
	if db.GlobalDB == nil || h == nil {
		return
	}
	_, _ = db.GlobalDB.Exec(`INSERT OR REPLACE INTO node_health 
		(raw_uri, success_count, fail_count, consecutive_failures, last_test_ms, last_test_error, last_success_at, last_fail_at, cooldown_until) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uri, h.SuccessCount, h.FailCount, h.ConsecutiveFailures, h.LastTestMs, h.LastTestError, h.LastSuccessAt, h.LastFailAt, h.CooldownUntil)
}

func updateSingleNodeDisabledUnsafe(uri string, disabled bool) {
	if db.GlobalDB == nil {
		return
	}
	_, _ = db.GlobalDB.Exec("UPDATE nodes SET disabled = ? WHERE raw_uri = ?", disabled, uri)
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

func RecordTest(uri string, ok bool, ms float64, errStr string) {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
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
		h.LastFailAt = time.Now().Unix()

		errLower := strings.ToLower(errStr)
		if strings.Contains(errLower, "dial") || strings.Contains(errLower, "refused") ||
			strings.Contains(errLower, "i/o timeout") || strings.Contains(errLower, "deadline exceeded") ||
			strings.Contains(errLower, "connection") {
			updateSingleNodeDisabledUnsafe(uri, true)
		} else {
			h.LastSubHealthyAt = time.Now().Unix()
		}
	}
	updateSingleNodeHealthUnsafe(uri, h)
}

func UpdateNodeTestResult(uri string, ok bool, ms float64, errStr string) {
	RecordTest(uri, ok, ms, errStr)
}

// RecordRateLimit 记录 429 冷却并递增计次，使重复 429 节点进入 Tier 2 亚健康
func RecordRateLimit(uri string, cooldownSec int) {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	h, exists := healthMap[uri]
	if !exists {
		h = &NodeHealth{} //nolint:exhaustruct
		healthMap[uri] = h
	}
	now := time.Now().Unix()
	h.LastSubHealthyAt = now
	h.Last429At = now
	h.RateLimitCount++
	h.LastTestError = "429 Rate Limit"
	h.LastFailAt = now
	h.CooldownUntil = time.Now().Unix() + int64(cooldownSec)
	updateSingleNodeHealthUnsafe(uri, h)
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

func SelectForParallel(k int, debugMode bool) []Node {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	now := time.Now().Unix()

	var tier1 []tierCandidate
	var tier2 []tierCandidate

	for i, n := range nodeList {
		if n.Disabled {
			continue
		}
		if IsSupportedFunc != nil && !IsSupportedFunc(n.RawURI) {
			nodeList[i].Disabled = true
			updateSingleNodeDisabledUnsafe(n.RawURI, true)
			continue
		}
		h := healthMap[n.RawURI]
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
		hi := healthMap[tier1[i].node.RawURI]
		hj := healthMap[tier1[j].node.RawURI]
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
		offset := int(atomic.AddUint64(&atomicRoundRobinIndex, 1)) % len(group)
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
			hi := healthMap[tier2[i].node.RawURI]
			hj := healthMap[tier2[j].node.RawURI]
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
			offset := int(atomic.AddUint64(&atomicRoundRobinIndex, 1)) % len(group)
			for l := 0; l < len(group) && len(selected) < k; l++ {
				idx := (offset + l) % len(group)
				h := healthMap[group[idx].node.RawURI]
				if h != nil && h.CooldownUntil > now {
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
			h := healthMap[tc.node.RawURI]
			if h == nil {
				if best == nil || tc.inFlight < best.inFlight {
					best = tc
				}
				continue
			}
			if h.CooldownUntil > now {
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
		h := healthMap[selected[idx].RawURI]
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
		if h := healthMap[s.RawURI]; h != nil {
			h.LastSelectedAt = now
			h.RecentUseCount++
		}
	}

	if debugMode {
		log.Printf("[Nodes] 选择并行节点 (需求: %d, 实际: %d)", k, len(selected))
	}
	return selected
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
