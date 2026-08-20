package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

var (
	ErrAlreadyRunning  = errors.New("subscription update already running")
	ErrServiceStopping = errors.New("subscription service is stopping")
	ErrStaleResult     = errors.New("subscription changed while update was running")
)

type FetchFunc func(ctx context.Context, rawURL, userAgent string) ([]nodes.Node, error)

type NodeStore interface {
	ReplaceSubscriptionNodes(context.Context, string, []nodes.Node, bool) error
	RemoveSubscriptionSource(context.Context, string, bool) error
}

type Service struct {
	fetch FetchFunc
	store Store
	nodes NodeStore

	mutationMu sync.Mutex
	runningMu  sync.Mutex
	running    map[string]struct{}

	lifecycleMu sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	stopping    bool
	stopDone    chan struct{}
	wg          sync.WaitGroup
}

func New(fetch FetchFunc) *Service {
	return &Service{fetch: fetch, running: make(map[string]struct{})}
}

func NewWithStore(fetch FetchFunc, store Store) *Service {
	return &Service{fetch: fetch, store: store, running: make(map[string]struct{})}
}

func NewWithStores(fetch FetchFunc, store Store, nodeStore NodeStore) *Service {
	return &Service{fetch: fetch, store: store, nodes: nodeStore, running: make(map[string]struct{})}
}

func (s *Service) Start(parent context.Context) error {
	if s.store != nil {
		if _, err := s.store.List(parent); err != nil {
			return err
		}
	} else if err := config.LoadSubscriptions(); err != nil {
		return err
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopping {
		return ErrServiceStopping
	}
	if s.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	s.ctx = ctx
	s.cancel = cancel
	s.stopping = false
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				s.triggerDue(now)
			}
		}
	}()
	return nil
}

// Started reports whether the periodic service lifecycle is active.
func (s *Service) Started() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.cancel != nil && !s.stopping
}

func (s *Service) Stop() {
	s.lifecycleMu.Lock()
	if s.stopping {
		done := s.stopDone
		s.lifecycleMu.Unlock()
		if done != nil {
			<-done
		}
		return
	}
	cancel := s.cancel
	s.stopping = true
	done := make(chan struct{})
	s.stopDone = done
	s.ctx = nil
	s.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	s.lifecycleMu.Lock()
	s.cancel = nil
	s.stopping = false
	s.stopDone = nil
	close(done)
	s.lifecycleMu.Unlock()
}

func (s *Service) beginUpdate(id string) bool {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	if _, exists := s.running[id]; exists {
		return false
	}
	s.running[id] = struct{}{}
	return true
}

func (s *Service) finishUpdate(id string) {
	s.runningMu.Lock()
	delete(s.running, id)
	s.runningMu.Unlock()
}

func (s *Service) RunningIDs() []string {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	ids := make([]string, 0, len(s.running))
	for id := range s.running {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Service) Update(ctx context.Context, id string) error {
	if !s.beginUpdate(id) {
		return ErrAlreadyRunning
	}
	defer s.finishUpdate(id)
	return s.updateReserved(ctx, id)
}

func (s *Service) updateReserved(ctx context.Context, id string) error {
	sub, ok, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return config.ErrSubscriptionNotFound
	}
	userAgent, err := s.resolveUserAgent(ctx, sub)
	if err != nil {
		return s.recordFailure(sub, err)
	}
	if s.fetch == nil {
		return s.recordFailure(sub, errors.New("subscription fetcher is unavailable"))
	}
	parsedNodes, err := s.fetch(ctx, sub.URL, userAgent)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return s.recordFailure(sub, err)
	}
	if len(parsedNodes) == 0 {
		return s.recordFailure(sub, errors.New("未解析到有效节点"))
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	current, ok, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if !ok || current.Generation != sub.Generation || current.Revision != sub.Revision {
		return ErrStaleResult
	}
	if err := s.replaceSubscriptionNodes(ctx, id, parsedNodes, sub.AdoptManual); err != nil {
		_, _ = s.updateStatus(ctx, id, sub.Generation, sub.Revision, sub.LastUpdateTime, "替换订阅节点失败: "+err.Error())
		return err
	}
	updated, err := s.updateStatus(ctx, id, sub.Generation, sub.Revision, time.Now().Unix(), "")
	if err != nil {
		return err
	}
	if !updated {
		return ErrStaleResult
	}
	return nil
}

func (s *Service) recordFailure(sub config.Subscription, updateErr error) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	updated, err := s.updateStatus(context.Background(), sub.ID, sub.Generation, sub.Revision, sub.LastUpdateTime, updateErr.Error())
	if err != nil {
		return fmt.Errorf("%v; save status: %w", updateErr, err)
	}
	if !updated {
		return ErrStaleResult
	}
	return updateErr
}

func (s *Service) Trigger(id string) bool {
	if id == "" {
		return false
	}
	if !s.beginUpdate(id) {
		return false
	}
	s.lifecycleMu.Lock()
	if s.stopping {
		s.lifecycleMu.Unlock()
		s.finishUpdate(id)
		return false
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	s.wg.Add(1)
	s.lifecycleMu.Unlock()
	go func() {
		defer s.wg.Done()
		defer s.finishUpdate(id)
		if err := s.updateReserved(ctx, id); err != nil && !errors.Is(err, ErrStaleResult) {
			log.Printf("[Subscriptions] 更新 %s 失败: %v", id, err)
		}
	}()
	return true
}

func (s *Service) TriggerAll() int {
	conf, err := s.List(context.Background())
	if err != nil {
		log.Printf("[Subscriptions] 列出订阅失败: %v", err)
		return 0
	}
	triggered := 0
	for _, sub := range conf.Subscriptions {
		if s.Trigger(sub.ID) {
			triggered++
		}
	}
	return triggered
}

func (s *Service) triggerDue(now time.Time) {
	conf, err := s.List(context.Background())
	if err != nil {
		log.Printf("[Subscriptions] 定时读取订阅失败: %v", err)
		return
	}
	for _, sub := range conf.Subscriptions {
		if sub.UpdateInterval <= 0 {
			continue
		}
		if now.Unix() >= sub.LastUpdateTime+int64(sub.UpdateInterval*60) {
			if !s.Trigger(sub.ID) {
				log.Printf("[Subscriptions] 跳过仍在运行的订阅更新: %s", sub.ID)
			}
		}
	}
}

func (s *Service) SaveSubscription(sub config.Subscription) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if s.store != nil {
		return s.store.Save(context.Background(), sub)
	}
	return config.UpdateSubscription(sub)
}

func (s *Service) DeleteSubscription(id string, deleteNodes bool) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if _, ok, getErr := s.get(context.Background(), id); getErr != nil {
		return getErr
	} else if !ok {
		return config.ErrSubscriptionNotFound
	}
	deleted, err := s.delete(context.Background(), id)
	if err != nil {
		return err
	}
	if err = s.removeSubscriptionSource(context.Background(), id, deleteNodes); err == nil {
		return nil
	}
	if restoreErr := s.save(context.Background(), deleted); restoreErr != nil {
		return fmt.Errorf("remove subscription nodes: %v; restore subscription: %w", err, restoreErr)
	}
	return err
}

func (s *Service) SaveCustomUA(ua config.CustomUA) (config.CustomUA, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if s.store != nil {
		return s.store.SaveUserAgent(context.Background(), ua)
	}
	return config.SaveCustomUA(ua)
}

func (s *Service) DeleteCustomUA(id string) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if s.store != nil {
		return s.store.DeleteUserAgent(context.Background(), id)
	}
	return config.DeleteCustomUA(id)
}

func (s *Service) List(ctx context.Context) (config.SubscriptionConfig, error) {
	if s.store != nil {
		return s.store.List(ctx)
	}
	if err := config.LoadSubscriptions(); err != nil {
		return config.SubscriptionConfig{}, err
	}
	return config.GetSubscriptionConfig(), nil
}

func (s *Service) Get(ctx context.Context, id string) (config.Subscription, bool, error) {
	return s.get(ctx, id)
}

func (s *Service) FindCustomUAByName(ctx context.Context, name string) (config.CustomUA, bool, error) {
	if s.store != nil {
		return s.store.FindUserAgentByName(ctx, name)
	}
	value, ok := config.FindCustomUAByName(name)
	return value, ok, nil
}

func (s *Service) get(ctx context.Context, id string) (config.Subscription, bool, error) {
	if s.store != nil {
		return s.store.Get(ctx, id)
	}
	value, ok := config.GetSubscription(id)
	return value, ok, nil
}

func (s *Service) resolveUserAgent(ctx context.Context, subscription config.Subscription) (string, error) {
	if s.store != nil {
		return s.store.ResolveUserAgent(ctx, subscription)
	}
	return config.ResolveSubscriptionUserAgent(subscription)
}

func (s *Service) updateStatus(ctx context.Context, id, generation string, revision uint64, lastUpdate int64, lastError string) (bool, error) {
	if s.store != nil {
		return s.store.UpdateStatus(ctx, id, generation, revision, lastUpdate, lastError)
	}
	return config.UpdateSubscriptionStatus(id, generation, revision, lastUpdate, lastError)
}

func (s *Service) delete(ctx context.Context, id string) (config.Subscription, error) {
	if s.store != nil {
		return s.store.Delete(ctx, id)
	}
	return config.DeleteSubscription(id)
}

func (s *Service) save(ctx context.Context, subscription config.Subscription) error {
	if s.store != nil {
		return s.store.Save(ctx, subscription)
	}
	return config.UpdateSubscription(subscription)
}

func (s *Service) replaceSubscriptionNodes(ctx context.Context, id string, values []nodes.Node, adoptManual bool) error {
	if s.nodes != nil {
		return s.nodes.ReplaceSubscriptionNodes(ctx, id, values, adoptManual)
	}
	return nodes.ReplaceSubscriptionNodes(id, values, adoptManual)
}

func (s *Service) removeSubscriptionSource(ctx context.Context, id string, deleteNodes bool) error {
	if s.nodes != nil {
		return s.nodes.RemoveSubscriptionSource(ctx, id, deleteNodes)
	}
	return nodes.RemoveSubscriptionSource(id, deleteNodes)
}
