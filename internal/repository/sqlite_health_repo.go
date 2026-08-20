package repository

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/domain"
	"github.com/jmoiron/sqlx"
)

type healthDelta struct {
	uri         string
	isTest      bool
	testOK      bool
	latencyMs   float64
	errText     string
	is429       bool
	cooldownSec int
}

// SQLiteHealthRepository implements HealthRepository with an asynchronous reliable batch worker.
type SQLiteHealthRepository struct {
	db        *sqlx.DB
	memMu     sync.RWMutex
	memoryMap map[string]*domain.NodeHealth

	ch       chan healthDelta
	flushCh  chan chan error
	stopCh   chan struct{}
	doneCh   chan struct{}
	workerMu sync.Mutex
	closed   bool
}

// NewSQLiteHealthRepository initializes the repository and starts the background flush worker.
func NewSQLiteHealthRepository(db *sqlx.DB) *SQLiteHealthRepository {
	repo := &SQLiteHealthRepository{
		db:        db,
		memoryMap: make(map[string]*domain.NodeHealth),
		ch:        make(chan healthDelta, 4096),
		flushCh:   make(chan chan error),
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
	// Initial load from DB into memory
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = repo.loadInitial(ctx)

	go repo.worker()
	return repo
}

func (r *SQLiteHealthRepository) loadInitial(ctx context.Context) error {
	var list []domain.NodeHealth
	err := r.db.SelectContext(ctx, &list, `
		SELECT raw_uri, success_count, fail_count, consecutive_failures,
		       last_test_ms, last_test_error, last_success_at, last_fail_at,
		       cooldown_until, last_429_at, rate_limit_count, last_sub_healthy_at
		FROM node_health
	`)
	if err != nil {
		return err
	}
	r.memMu.Lock()
	defer r.memMu.Unlock()
	for _, h := range list {
		hc := h
		r.memoryMap[h.RawURI] = &hc
	}
	return nil
}

func (r *SQLiteHealthRepository) GetAll(ctx context.Context) (map[string]*domain.NodeHealth, error) {
	r.memMu.RLock()
	defer r.memMu.RUnlock()
	result := make(map[string]*domain.NodeHealth, len(r.memoryMap))
	for k, v := range r.memoryMap {
		if v != nil {
			cp := *v
			result[k] = &cp
		}
	}
	return result, nil
}

func (r *SQLiteHealthRepository) GetByURI(ctx context.Context, rawURI string) (*domain.NodeHealth, error) {
	r.memMu.RLock()
	defer r.memMu.RUnlock()
	if h, ok := r.memoryMap[rawURI]; ok && h != nil {
		cp := *h
		return &cp, nil
	}
	return nil, nil
}

func (r *SQLiteHealthRepository) RecordTest(rawURI string, ok bool, latencyMs float64, errText string) {
	if rawURI == "" {
		return
	}
	now := time.Now().Unix()
	r.memMu.Lock()
	h, exists := r.memoryMap[rawURI]
	if !exists || h == nil {
		h = &domain.NodeHealth{RawURI: rawURI}
		r.memoryMap[rawURI] = h
	}
	h.LastTestMs = latencyMs
	h.LastTestError = errText
	if ok {
		h.SuccessCount++
		h.ConsecutiveFailures = 0
		h.LastSuccessAt = now
		h.LastSubHealthyAt = 0
		h.CooldownUntil = 0
		h.Last429At = 0
		h.RateLimitCount = 0
	} else {
		h.FailCount++
		h.ConsecutiveFailures++
		h.LastFailAt = now
		failures := max(1, h.ConsecutiveFailures)
		cooldown := min(1800, 30*(1<<min(failures-1, 6)))
		h.CooldownUntil = now + int64(cooldown)
		h.LastSubHealthyAt = now
	}
	r.memMu.Unlock()

	r.enqueue(healthDelta{
		uri:       rawURI,
		isTest:    true,
		testOK:    ok,
		latencyMs: latencyMs,
		errText:   errText,
	})
}

func (r *SQLiteHealthRepository) RecordRateLimit(rawURI string, cooldownSec int) {
	if rawURI == "" {
		return
	}
	now := time.Now().Unix()
	r.memMu.Lock()
	h, exists := r.memoryMap[rawURI]
	if !exists || h == nil {
		h = &domain.NodeHealth{RawURI: rawURI}
		r.memoryMap[rawURI] = h
	}
	h.CooldownUntil = now + int64(cooldownSec)
	h.LastSubHealthyAt = now
	h.Last429At = now
	h.RateLimitCount++
	h.LastTestError = "429 Rate Limit"
	h.LastFailAt = now
	r.memMu.Unlock()

	r.enqueue(healthDelta{
		uri:         rawURI,
		is429:       true,
		cooldownSec: cooldownSec,
	})
}

func (r *SQLiteHealthRepository) enqueue(delta healthDelta) {
	r.workerMu.Lock()
	if r.closed {
		r.workerMu.Unlock()
		return
	}
	r.workerMu.Unlock()

	select {
	case r.ch <- delta:
	default:
		// Reliable non-blocking path: in extreme burst, launch a temporary goroutine to avoid dropping
		go func() {
			r.ch <- delta
		}()
	}
}

func (r *SQLiteHealthRepository) worker() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	defer close(r.doneCh)

	dirty := make(map[string]bool)

	flush := func() error {
		if len(dirty) == 0 {
			return nil
		}
		toPersist := make([]domain.NodeHealth, 0, len(dirty))
		r.memMu.RLock()
		for uri := range dirty {
			if h, ok := r.memoryMap[uri]; ok && h != nil {
				toPersist = append(toPersist, *h)
			}
		}
		r.memMu.RUnlock()
		dirty = make(map[string]bool)

		if len(toPersist) == 0 {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		tx, err := r.db.BeginTxx(ctx, nil)
		if err != nil {
			log.Printf("[HealthRepo] begin tx err: %v", err)
			return err
		}
		defer func() { _ = tx.Rollback() }()

		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO node_health (
				raw_uri, success_count, fail_count, consecutive_failures,
				last_test_ms, last_test_error, last_success_at, last_fail_at,
				cooldown_until, last_429_at, rate_limit_count, last_sub_healthy_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(raw_uri) DO UPDATE SET
				success_count = excluded.success_count,
				fail_count = excluded.fail_count,
				consecutive_failures = excluded.consecutive_failures,
				last_test_ms = excluded.last_test_ms,
				last_test_error = excluded.last_test_error,
				last_success_at = excluded.last_success_at,
				last_fail_at = excluded.last_fail_at,
				cooldown_until = excluded.cooldown_until,
				last_429_at = excluded.last_429_at,
				rate_limit_count = excluded.rate_limit_count,
				last_sub_healthy_at = excluded.last_sub_healthy_at
		`)
		if err != nil {
			log.Printf("[HealthRepo] prepare stmt err: %v", err)
			return err
		}
		defer stmt.Close()

		for _, h := range toPersist {
			_, _ = stmt.ExecContext(ctx,
				h.RawURI, h.SuccessCount, h.FailCount, h.ConsecutiveFailures,
				h.LastTestMs, h.LastTestError, h.LastSuccessAt, h.LastFailAt,
				h.CooldownUntil, h.Last429At, h.RateLimitCount, h.LastSubHealthyAt,
			)
		}
		return tx.Commit()
	}

	for {
		select {
		case delta := <-r.ch:
			dirty[delta.uri] = true
			if len(dirty) >= 100 {
				_ = flush()
			}
		case <-ticker.C:
			_ = flush()
		case reply := <-r.flushCh:
			err := flush()
			reply <- err
		case <-r.stopCh:
			_ = flush()
			return
		}
	}
}

func (r *SQLiteHealthRepository) Flush(ctx context.Context) error {
	reply := make(chan error, 1)
	select {
	case r.flushCh <- reply:
		select {
		case err := <-reply:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *SQLiteHealthRepository) Close() error {
	r.workerMu.Lock()
	if r.closed {
		r.workerMu.Unlock()
		return nil
	}
	r.closed = true
	r.workerMu.Unlock()

	close(r.stopCh)
	<-r.doneCh
	return nil
}
