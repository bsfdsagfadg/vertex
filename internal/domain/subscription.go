package domain

// Subscription represents a remote node subscription source.
type Subscription struct {
	ID             string `json:"id" db:"id"`
	Name           string `json:"name" db:"name"`
	URL            string `json:"url" db:"url"`
	UserAgent      string `json:"user_agent" db:"user_agent"`
	CustomUAID     string `json:"custom_ua_id" db:"custom_ua_id"`
	UpdateInterval int    `json:"update_interval" db:"update_interval"`
	AdoptManual    bool   `json:"adopt_manual" db:"adopt_manual"`
	LastUpdateTime int64  `json:"last_update_time" db:"last_update_time"`
	LastError      string `json:"last_error" db:"last_error"`
	Revision       int64  `json:"revision" db:"revision"`
	Generation     string `json:"generation" db:"generation"`
}

// CustomUA represents a reusable User-Agent template.
type CustomUA struct {
	ID        string `json:"id" db:"id"`
	Name      string `json:"name" db:"name"`
	UserAgent string `json:"user_agent" db:"user_agent"`
}
