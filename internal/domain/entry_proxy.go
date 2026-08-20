package domain

// EntryProxyCandidate represents a candidate proxy through which target nodes can be dialed.
type EntryProxyCandidate struct {
	RawURI              string  `json:"raw_uri" db:"raw_uri"`
	NormalizedURI       string  `json:"normalized_uri,omitempty" db:"normalized_uri"`
	Name                string  `json:"name" db:"name"`
	Type                string  `json:"type" db:"type"`
	Disabled            bool    `json:"disabled" db:"disabled"`
	CooldownUntil       int64   `json:"cooldown_until" db:"cooldown_until"`
	LastTestOK          bool    `json:"last_test_ok" db:"last_test_ok"`
	LastTestMs          float64 `json:"last_test_ms" db:"last_test_ms"`
	LastTestAt          int64   `json:"last_test_at" db:"last_test_at"`
	LastTestError       string  `json:"last_test_error" db:"last_test_error"`
	ConsecutiveFailures int     `json:"consecutive_failures" db:"consecutive_failures"`
}
