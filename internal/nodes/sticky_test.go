package nodes

import (
	"testing"
	"time"
)

func TestStickyPool_EvictStale(t *testing.T) {
	p := NewStickyNodePool()

	p.Add("uri-recent")
	p.Add("uri-mid")
	p.Add("uri-old")

	// 注入时间戳：通过直接修改 pool map
	now := time.Now()
	p.mu.Lock()
	p.pool["uri-recent"] = now
	p.pool["uri-mid"] = now.Add(-20 * time.Minute)
	p.pool["uri-old"] = now.Add(-40 * time.Minute)
	p.mu.Unlock()

	evicted := p.EvictStale(30 * time.Minute)
	if evicted != 1 {
		t.Errorf("EvictStale returned %d, want 1", evicted)
	}

	if p.IsSticky("uri-old") {
		t.Error("IsSticky('uri-old') should be false (evicted by staleness)")
	}
	if !p.IsSticky("uri-mid") {
		t.Error("IsSticky('uri-mid') should be true (within 30 min threshold)")
	}
	if !p.IsSticky("uri-recent") {
		t.Error("IsSticky('uri-recent') should be true (just added)")
	}

	if got := p.AvailableCount(); got != 2 {
		t.Errorf("AvailableCount() = %d, want 2", got)
	}
}
