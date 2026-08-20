package repository

import (
	"context"

	"github.com/bsfdsagfadg/vertex/internal/domain"
)

// NodeRepository defines the data access contract for nodes and their sources.
type NodeRepository interface {
	GetAll(ctx context.Context) ([]domain.Node, error)
	GetByURI(ctx context.Context, rawURI string) (*domain.Node, error)
	GetSources(ctx context.Context, rawURI string) ([]domain.NodeSource, error)
	GetAllSources(ctx context.Context) (map[string][]domain.NodeSource, error)
	
	// UpsertNodesWithSource performs transactional diff-upsert for nodes from a given source.
	UpsertNodesWithSource(ctx context.Context, nodes []domain.Node, source domain.NodeSource, adoptManual bool) error
	
	// ReplaceSubscriptionNodes updates subscription nodes transactionally, deleting nodes orphaned by the subscription.
	ReplaceSubscriptionNodes(ctx context.Context, subscriptionID string, newNodes []domain.Node, adoptManual bool) (removedURIs []string, err error)
	
	DeleteByURI(ctx context.Context, rawURI string) error
	DeleteDisabled(ctx context.Context) (removedURIs []string, err error)
	BatchDelete(ctx context.Context, uris []string) error
	BatchSetDisabled(ctx context.Context, uris []string, disabled bool) error
	SetDisabled(ctx context.Context, rawURI string, disabled bool) error
	
	// DedupByURI identifies and removes duplicate nodes, returning statistics.
	Dedup(ctx context.Context) (domain.DedupPreview, error)
}

// HealthRepository defines the data access contract for node health, latencies, and cooldowns.
type HealthRepository interface {
	GetAll(ctx context.Context) (map[string]*domain.NodeHealth, error)
	GetByURI(ctx context.Context, rawURI string) (*domain.NodeHealth, error)
	
	// RecordTest queues or synchronously writes a latency / health result.
	RecordTest(rawURI string, ok bool, latencyMs float64, errText string)
	
	// RecordRateLimit queues or writes a 429 rate limit cooldown.
	RecordRateLimit(rawURI string, cooldownSec int)
	
	// Flush commits all pending asynchronous health updates to the database.
	Flush(ctx context.Context) error
	
	// Close safely stops background persistence workers.
	Close() error
}

// SubscriptionRepository defines the data access contract for subscriptions and custom User-Agents.
type SubscriptionRepository interface {
	GetAll(ctx context.Context) ([]domain.Subscription, error)
	GetByID(ctx context.Context, id string) (*domain.Subscription, error)
	Save(ctx context.Context, sub domain.Subscription) error
	Delete(ctx context.Context, id string) error
	
	// Custom UA methods
	GetAllCustomUAs(ctx context.Context) ([]domain.CustomUA, error)
	GetCustomUAByID(ctx context.Context, id string) (*domain.CustomUA, error)
	SaveCustomUA(ctx context.Context, ua domain.CustomUA) error
	DeleteCustomUA(ctx context.Context, id string) error
}

// EntryProxyRepository defines data access for global entry proxy candidates.
type EntryProxyRepository interface {
	GetAll(ctx context.Context) ([]domain.EntryProxyCandidate, error)
	GetByNormalizedURI(ctx context.Context, normalizedURI string) (*domain.EntryProxyCandidate, error)
	Add(ctx context.Context, candidate domain.EntryProxyCandidate) error
	Remove(ctx context.Context, normalizedURI string) error
	RemoveDisabled(ctx context.Context) ([]string, error)
	UpdateTestResult(ctx context.Context, normalizedURI string, ok bool, latencyMs float64, errText string, cooldownSec int, countScheduledFailure bool, autoDisable bool, failureLimit int) (autoDisabled bool, err error)
	Exists(ctx context.Context, normalizedURI string) (bool, error)
}
