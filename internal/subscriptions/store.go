package subscriptions

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/repository"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
)

// Store is the instance-owned subscription control-plane boundary.
type Store interface {
	List(context.Context) (config.SubscriptionConfig, error)
	Get(context.Context, string) (config.Subscription, bool, error)
	Save(context.Context, config.Subscription) error
	Delete(context.Context, string) (config.Subscription, error)
	UpdateStatus(context.Context, string, string, uint64, int64, string) (bool, error)
	ResolveUserAgent(context.Context, config.Subscription) (string, error)
	FindUserAgentByName(context.Context, string) (config.CustomUA, bool, error)
	SaveUserAgent(context.Context, config.CustomUA) (config.CustomUA, error)
	DeleteUserAgent(context.Context, string) error
}

type RepositoryStore struct {
	repository *repository.SQLite
	mu         sync.Mutex
}

func NewRepositoryStore(store *repository.SQLite) (*RepositoryStore, error) {
	if store == nil {
		return nil, fmt.Errorf("subscription repository is nil")
	}
	return &RepositoryStore{repository: store}, nil
}

func (s *RepositoryStore) List(ctx context.Context) (config.SubscriptionConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(ctx)
}

func (s *RepositoryStore) Get(ctx context.Context, id string) (config.Subscription, bool, error) {
	conf, err := s.List(ctx)
	if err != nil {
		return config.Subscription{}, false, err
	}
	for _, subscription := range conf.Subscriptions {
		if subscription.ID == id {
			return subscription, true, nil
		}
	}
	return config.Subscription{}, false, nil
}

func (s *RepositoryStore) Save(ctx context.Context, subscription config.Subscription) error {
	return s.mutate(ctx, func(conf *config.SubscriptionConfig) error {
		subscription.Name = strings.TrimSpace(subscription.Name)
		subscription.URL = strings.TrimSpace(subscription.URL)
		if subscription.Name == "" || subscription.URL == "" {
			return fmt.Errorf("subscription name and URL are required")
		}
		if subscription.CustomUAID != "" && !containsUserAgent(conf.CustomUAs, subscription.CustomUAID) {
			return config.ErrCustomUANotFound
		}
		if subscription.CustomUAID != "" {
			subscription.UserAgent = ""
		}
		for index, current := range conf.Subscriptions {
			if current.ID != subscription.ID {
				continue
			}
			subscription.LastUpdateTime = current.LastUpdateTime
			subscription.LastError = current.LastError
			subscription.Revision = current.Revision + 1
			subscription.Generation = current.Generation
			conf.Subscriptions[index] = subscription
			return nil
		}
		if subscription.ID == "" {
			subscription.ID = "sub_" + strutil.RandomHex(8)
		}
		subscription.Revision = 1
		subscription.Generation = "subgen_" + strutil.RandomHex(8)
		conf.Subscriptions = append(conf.Subscriptions, subscription)
		return nil
	})
}

func (s *RepositoryStore) Delete(ctx context.Context, id string) (config.Subscription, error) {
	var deleted config.Subscription
	err := s.mutate(ctx, func(conf *config.SubscriptionConfig) error {
		kept := make([]config.Subscription, 0, len(conf.Subscriptions))
		for _, subscription := range conf.Subscriptions {
			if subscription.ID == id {
				deleted = subscription
				continue
			}
			kept = append(kept, subscription)
		}
		if deleted.ID == "" {
			return config.ErrSubscriptionNotFound
		}
		conf.Subscriptions = kept
		return nil
	})
	return deleted, err
}

func (s *RepositoryStore) UpdateStatus(ctx context.Context, id, generation string, revision uint64, lastUpdate int64, lastError string) (bool, error) {
	updated := false
	err := s.mutate(ctx, func(conf *config.SubscriptionConfig) error {
		for index := range conf.Subscriptions {
			subscription := &conf.Subscriptions[index]
			if subscription.ID != id || (generation != "" && subscription.Generation != generation) || (revision != 0 && subscription.Revision != revision) {
				continue
			}
			subscription.LastUpdateTime = lastUpdate
			subscription.LastError = lastError
			updated = true
			break
		}
		return nil
	})
	return updated, err
}

func (s *RepositoryStore) ResolveUserAgent(ctx context.Context, subscription config.Subscription) (string, error) {
	if subscription.CustomUAID == "" {
		return subscription.UserAgent, nil
	}
	conf, err := s.List(ctx)
	if err != nil {
		return "", err
	}
	for _, userAgent := range conf.CustomUAs {
		if userAgent.ID == subscription.CustomUAID {
			return userAgent.UserAgent, nil
		}
	}
	return "", config.ErrCustomUANotFound
}

func (s *RepositoryStore) FindUserAgentByName(ctx context.Context, name string) (config.CustomUA, bool, error) {
	conf, err := s.List(ctx)
	if err != nil {
		return config.CustomUA{}, false, err
	}
	name = strings.TrimSpace(name)
	for _, userAgent := range conf.CustomUAs {
		if strings.EqualFold(userAgent.Name, name) {
			return userAgent, true, nil
		}
	}
	return config.CustomUA{}, false, nil
}

func (s *RepositoryStore) SaveUserAgent(ctx context.Context, userAgent config.CustomUA) (config.CustomUA, error) {
	userAgent.Name = strings.TrimSpace(userAgent.Name)
	userAgent.UserAgent = strings.TrimSpace(userAgent.UserAgent)
	if userAgent.Name == "" || userAgent.UserAgent == "" {
		return config.CustomUA{}, fmt.Errorf("custom UA name and content are required")
	}
	err := s.mutate(ctx, func(conf *config.SubscriptionConfig) error {
		for _, existing := range conf.CustomUAs {
			if existing.ID != userAgent.ID && strings.EqualFold(existing.Name, userAgent.Name) {
				return config.ErrCustomUANameConflict
			}
		}
		if userAgent.ID == "" {
			userAgent.ID = "ua_" + strutil.RandomHex(8)
			conf.CustomUAs = append(conf.CustomUAs, userAgent)
			return nil
		}
		for index, existing := range conf.CustomUAs {
			if existing.ID != userAgent.ID {
				continue
			}
			changed := existing.Name != userAgent.Name || existing.UserAgent != userAgent.UserAgent
			conf.CustomUAs[index] = userAgent
			if changed {
				for subIndex := range conf.Subscriptions {
					if conf.Subscriptions[subIndex].CustomUAID == userAgent.ID {
						conf.Subscriptions[subIndex].Revision++
					}
				}
			}
			return nil
		}
		return config.ErrCustomUANotFound
	})
	return userAgent, err
}

func (s *RepositoryStore) DeleteUserAgent(ctx context.Context, id string) error {
	return s.mutate(ctx, func(conf *config.SubscriptionConfig) error {
		for _, subscription := range conf.Subscriptions {
			if subscription.CustomUAID == id {
				return config.ErrCustomUAInUse
			}
		}
		kept := make([]config.CustomUA, 0, len(conf.CustomUAs))
		found := false
		for _, userAgent := range conf.CustomUAs {
			if userAgent.ID == id {
				found = true
				continue
			}
			kept = append(kept, userAgent)
		}
		if !found {
			return config.ErrCustomUANotFound
		}
		conf.CustomUAs = kept
		return nil
	})
}

func (s *RepositoryStore) mutate(ctx context.Context, update func(*config.SubscriptionConfig) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	conf, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}
	if err := update(&conf); err != nil {
		return err
	}
	return s.saveLocked(ctx, conf)
}

func (s *RepositoryStore) loadLocked(ctx context.Context) (config.SubscriptionConfig, error) {
	subscriptions, userAgents, err := s.repository.LoadSubscriptions(ctx)
	if err != nil {
		return config.SubscriptionConfig{}, err
	}
	conf := config.SubscriptionConfig{Subscriptions: make([]config.Subscription, 0, len(subscriptions)), CustomUAs: make([]config.CustomUA, 0, len(userAgents))}
	for _, value := range subscriptions {
		conf.Subscriptions = append(conf.Subscriptions, config.Subscription{
			ID: value.ID, Name: value.Name, URL: value.URL, UserAgent: value.UserAgent, CustomUAID: value.CustomUAID,
			UpdateInterval: value.UpdateInterval, AdoptManual: value.AdoptManual, LastUpdateTime: value.LastUpdateTime,
			LastError: value.LastError, Revision: value.Revision, Generation: value.Generation,
		})
	}
	for _, value := range userAgents {
		conf.CustomUAs = append(conf.CustomUAs, config.CustomUA{ID: value.ID, Name: value.Name, UserAgent: value.UserAgent})
	}
	return conf, nil
}

func (s *RepositoryStore) saveLocked(ctx context.Context, conf config.SubscriptionConfig) error {
	subscriptions := make([]repository.Subscription, 0, len(conf.Subscriptions))
	for _, value := range conf.Subscriptions {
		subscriptions = append(subscriptions, repository.Subscription{
			ID: value.ID, Name: value.Name, URL: value.URL, UserAgent: value.UserAgent, CustomUAID: value.CustomUAID,
			UpdateInterval: value.UpdateInterval, AdoptManual: value.AdoptManual, LastUpdateTime: value.LastUpdateTime,
			LastError: value.LastError, Revision: value.Revision, Generation: value.Generation,
		})
	}
	userAgents := make([]repository.SubscriptionUserAgent, 0, len(conf.CustomUAs))
	for _, value := range conf.CustomUAs {
		userAgents = append(userAgents, repository.SubscriptionUserAgent{ID: value.ID, Name: value.Name, UserAgent: value.UserAgent})
	}
	return s.repository.ReplaceSubscriptions(ctx, subscriptions, userAgents)
}

func containsUserAgent(values []config.CustomUA, id string) bool {
	for _, value := range values {
		if value.ID == id {
			return true
		}
	}
	return false
}
