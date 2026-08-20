package domain

import (
	"time"
)

const (
	SourceLegacy       = "legacy"
	SourceManual       = "manual"
	SourceSubscription = "subscription"
)

// Node represents a proxy backend node.
type Node struct {
	Type     string `json:"type" db:"type"`
	Name     string `json:"name" db:"name"`
	RawURI   string `json:"raw_uri" db:"raw_uri"`
	Disabled bool   `json:"disabled" db:"disabled"`
}

// NodeSource tracks where a node originated from (manual entry, subscription ID, or legacy file).
type NodeSource struct {
	Type string `json:"type" db:"source_type"`
	ID   string `json:"id,omitempty" db:"source_id"`
}

// NodeHealth tracks operational health, latency metrics, and cooldown status for a node.
type NodeHealth struct {
	RawURI              string  `json:"-" db:"raw_uri"`
	SuccessCount        int     `json:"success_count" db:"success_count"`
	FailCount           int     `json:"fail_count" db:"fail_count"`
	ConsecutiveFailures int     `json:"consecutive_failures" db:"consecutive_failures"`
	LastTestMs          float64 `json:"last_test_ms" db:"last_test_ms"`
	LastTestError       string  `json:"last_test_error" db:"last_test_error"`
	LastSuccessAt       int64   `json:"last_success_at" db:"last_success_at"`
	LastFailAt          int64   `json:"last_fail_at" db:"last_fail_at"`
	CooldownUntil       int64   `json:"cooldown_until" db:"cooldown_until"`
	Last429At           int64   `json:"last_429_at" db:"last_429_at"`
	RateLimitCount      int     `json:"rate_limit_count" db:"rate_limit_count"`
	RecentUseCount      int     `json:"recent_use_count" db:"-"`
	LastSelectedAt      int64   `json:"last_selected_at" db:"-"`
	LastSubHealthyAt    int64   `json:"last_sub_healthy_at" db:"last_sub_healthy_at"`
	InFlight            int32   `json:"-" db:"-"`
}

// IsHealthy returns true if the node is neither disabled nor in cooldown.
func (h *NodeHealth) IsHealthy() bool {
	if h == nil {
		return true
	}
	now := time.Now().Unix()
	return h.CooldownUntil <= now && h.LastSubHealthyAt == 0
}

// IsSubHealthy returns true if the node is currently marked sub-healthy or temporarily cooling down.
func (h *NodeHealth) IsSubHealthy() bool {
	if h == nil {
		return false
	}
	now := time.Now().Unix()
	return h.CooldownUntil > now || h.LastSubHealthyAt > 0
}

// DedupPreview reports deduplication statistics.
type DedupPreview struct {
	Groups         int `json:"groups"`
	DuplicateCount int `json:"duplicate_count"`
}
