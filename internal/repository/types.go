package repository

type Node struct {
	ID                  string `json:"id" db:"id"`
	RawURI              string `json:"raw_uri" db:"raw_uri"`
	CanonicalIdentity   string `json:"canonical_identity" db:"canonical_identity"`
	EndpointFingerprint string `json:"endpoint_fingerprint" db:"endpoint_fingerprint"`
	Type                string `json:"type" db:"type"`
	Name                string `json:"name" db:"name"`
	Disabled            bool   `json:"disabled" db:"disabled"`
}

type NodeSource struct {
	NodeID     string
	SourceType string
	SourceID   string
}

type NodeHealth struct {
	NodeID              string
	SuccessCount        int
	FailCount           int
	ConsecutiveFailures int
	LastTestMS          float64
	LastTestError       string
	LastSuccessAt       int64
	LastFailAt          int64
	CooldownUntil       int64
	Last429At           int64
	RateLimitCount      int
	LastSubHealthyAt    int64
}

type GlobalProxy struct {
	ID                  string
	RawURI              string
	CanonicalIdentity   string
	EndpointFingerprint string
	Name                string
	Type                string
	Disabled            bool
	Pinned              bool
}

type GlobalProxySource struct {
	GlobalProxyID string `json:"global_proxy_id"`
	SourceType    string `json:"source_type"`
	SourceID      string `json:"source_id,omitempty"`
}

type GlobalProxyHealth struct {
	GlobalProxyID       string
	CooldownUntil       int64
	LastTestOK          bool
	LastTestMS          float64
	LastTestAt          int64
	LastTestError       string
	ConsecutiveFailures int
}

type SubscriptionUserAgent struct {
	ID        string `json:"id" db:"id"`
	Name      string `json:"name" db:"name"`
	UserAgent string `json:"user_agent" db:"user_agent"`
}

type Subscription struct {
	ID             string `json:"id" db:"id"`
	Name           string `json:"name" db:"name"`
	URL            string `json:"url" db:"url"`
	UserAgent      string `json:"user_agent,omitempty" db:"user_agent"`
	CustomUAID     string `json:"custom_ua_id,omitempty" db:"custom_ua_id"`
	UpdateInterval int    `json:"update_interval" db:"update_interval"`
	AdoptManual    bool   `json:"adopt_manual,omitempty" db:"adopt_manual"`
	LastUpdateTime int64  `json:"last_update_time" db:"last_update_time"`
	LastError      string `json:"last_error" db:"last_error"`
	Revision       uint64 `json:"revision" db:"revision"`
	Generation     string `json:"generation" db:"generation"`
}

// ToolState is the decrypted repository boundary used by the protocol layer.
// StateJSON never appears in SQLite in plaintext.
type ToolState struct {
	ExternalCallID    string
	ResponseID        string
	ConversationID    string
	UpstreamOperation string
	StateJSON         []byte
	ExpiresAt         int64
	ConsumedAt        int64
	TranscriptHash    string
	ConsumeHash       string
}

type ResponseResource struct {
	ID                  string
	Status              string
	Model               string
	RequestJSON         []byte
	InputJSON           []byte
	OutputJSON          []byte
	UsageJSON           []byte
	ErrorJSON           []byte
	IncompleteJSON      []byte
	PreviousResponseID  string
	ConversationID      string
	MetadataJSON        []byte
	UpstreamOperationID string
	Store               bool
	Background          bool
	CreatedAt           int64
	CompletedAt         int64
	ExpiresAt           int64
}

type Conversation struct {
	ID           string
	MetadataJSON []byte
	CreatedAt    int64
	UpdatedAt    int64
}

type ConversationItem struct {
	ID             string
	ConversationID string
	Ordinal        int64
	ItemJSON       []byte
	CreatedAt      int64
}

type InteractionResource struct {
	ID                    string
	Status                string
	Model                 string
	Agent                 string
	RequestJSON           []byte
	StepsJSON             []byte
	UsageJSON             []byte
	ErrorJSON             []byte
	PreviousInteractionID string
	LabelsJSON            []byte
	Store                 bool
	Background            bool
	CreatedAt             int64
	UpdatedAt             int64
	ExpiresAt             int64
}

type ResourceEvent struct {
	ResourceKind string
	ResourceID   string
	Sequence     int64
	EventID      string
	EventType    string
	EventJSON    []byte
	CreatedAt    int64
}

type BackgroundJob struct {
	ID           string
	ResourceKind string
	ResourceID   string
	Status       string
	ErrorJSON    []byte
	CreatedAt    int64
	UpdatedAt    int64
	ExpiresAt    int64
}

type IdempotencyRecord struct {
	Endpoint     string
	Key          string
	BodyHash     string
	ResourceKind string
	ResourceID   string
	CreatedAt    int64
	ExpiresAt    int64
}

type LocalFile struct {
	ID           string
	Dialect      string
	Name         string
	DisplayName  string
	Purpose      string
	MimeType     string
	SizeBytes    int64
	SHA256       string
	StoragePath  string
	Status       string
	MetadataJSON []byte
	CreatedAt    int64
	ExpiresAt    int64
}

type CachedContent struct {
	ID                    string
	Model                 string
	ContentsJSON          []byte
	SystemInstructionJSON []byte
	ToolsJSON             []byte
	MetadataJSON          []byte
	CreatedAt             int64
	UpdatedAt             int64
	ExpiresAt             int64
}

type Batch struct {
	ID                string
	Dialect           string
	Endpoint          string
	InputFileID       string
	OutputFileID      string
	ErrorFileID       string
	Status            string
	RequestCountsJSON []byte
	MetadataJSON      []byte
	ErrorJSON         []byte
	CreatedAt         int64
	InProgressAt      int64
	CompletedAt       int64
	CancelledAt       int64
	ExpiresAt         int64
}

type ChatCompletionResource struct {
	ID           string
	Model        string
	Status       string
	RequestJSON  []byte
	ResponseJSON []byte
	MessagesJSON []byte
	MetadataJSON []byte
	CreatedAt    int64
	UpdatedAt    int64
	ExpiresAt    int64
}

type Snapshot struct {
	Nodes              []Node
	NodeSources        []NodeSource
	NodeHealth         []NodeHealth
	GlobalProxies      []GlobalProxy
	GlobalProxySources []GlobalProxySource
	GlobalProxyHealth  []GlobalProxyHealth
	UserAgents         []SubscriptionUserAgent
	Subscriptions      []Subscription
}

type Counts struct {
	Nodes         int `json:"nodes"`
	NodeSources   int `json:"node_sources"`
	NodeHealth    int `json:"node_health"`
	GlobalProxies int `json:"global_proxies"`
	Subscriptions int `json:"subscriptions"`
	UserAgents    int `json:"subscription_user_agents"`
}
