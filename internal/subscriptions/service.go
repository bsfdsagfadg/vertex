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

type Service struct {
	fetch FetchFunc

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

func (s *Service) Start(parent context.Context) error {
	if err := config.LoadSubscriptions(); err != nil {
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
	sub, ok := config.GetSubscription(id)
	if !ok {
		return config.ErrSubscriptionNotFound
	}
	userAgent, err := config.ResolveSubscriptionUserAgent(sub)
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
	current, ok := config.GetSubscription(id)
	if !ok || current.Generation != sub.Generation || current.Revision != sub.Revision {
		return ErrStaleResult
	}
	if err := nodes.ReplaceSubscriptionNodes(id, parsedNodes, sub.AdoptManual); err != nil {
		_, _ = config.UpdateSubscriptionStatus(id, sub.Generation, sub.Revision, sub.LastUpdateTime, "替换订阅节点失败: "+err.Error())
		return err
	}
	updated, err := config.UpdateSubscriptionStatus(id, sub.Generation, sub.Revision, time.Now().Unix(), "")
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
	updated, err := config.UpdateSubscriptionStatus(sub.ID, sub.Generation, sub.Revision, sub.LastUpdateTime, updateErr.Error())
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
	conf := config.GetSubscriptionConfig()
	triggered := 0
	for _, sub := range conf.Subscriptions {
		if s.Trigger(sub.ID) {
			triggered++
		}
	}
	return triggered
}

func (s *Service) triggerDue(now time.Time) {
	conf := config.GetSubscriptionConfig()
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
	return config.UpdateSubscription(sub)
}

func (s *Service) DeleteSubscription(id string, deleteNodes bool) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if _, ok := config.GetSubscription(id); !ok {
		return config.ErrSubscriptionNotFound
	}
	deleted, err := config.DeleteSubscription(id)
	if err != nil {
		return err
	}
	if err = nodes.RemoveSubscriptionSource(id, deleteNodes); err == nil {
		return nil
	}
	if restoreErr := config.UpdateSubscription(deleted); restoreErr != nil {
		return fmt.Errorf("remove subscription nodes: %v; restore subscription: %w", err, restoreErr)
	}
	return err
}

func (s *Service) SaveCustomUA(ua config.CustomUA) (config.CustomUA, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	return config.SaveCustomUA(ua)
}

func (s *Service) DeleteCustomUA(id string) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	return config.DeleteCustomUA(id)
}
