package nodes

import (
	"sync"
	"time"
)

type StickyNodePool struct { //nolint:govet
	mu   sync.Mutex
	pool map[string]time.Time
}

const StaleTTL = 30 * time.Minute

var globalStickyPool = NewStickyNodePool() //nolint:gochecknoglobals

func GetStickyPool() *StickyNodePool {
	return globalStickyPool
}

func NewStickyNodePool() *StickyNodePool {
	return &StickyNodePool{ //nolint:exhaustruct
		pool: make(map[string]time.Time),
	}
}

func (p *StickyNodePool) Add(uri string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pool[uri] = time.Now()
}

func (p *StickyNodePool) Evict(uri string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.pool, uri)
}

func (p *StickyNodePool) IsSticky(uri string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, exists := p.pool[uri]
	return exists
}

func (p *StickyNodePool) AvailableCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pool)
}

func (p *StickyNodePool) StaleCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := time.Now().Add(-StaleTTL)
	count := 0
	for _, addedAt := range p.pool {
		if addedAt.Before(cutoff) {
			count++
		}
	}
	return count
}

func (p *StickyNodePool) List() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	uris := make([]string, 0, len(p.pool))
	for uri := range p.pool {
		uris = append(uris, uri)
	}
	return uris
}

func (p *StickyNodePool) EvictStale(maxAge time.Duration) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	count := 0
	for uri, addedAt := range p.pool {
		if addedAt.Before(cutoff) {
			delete(p.pool, uri)
			count++
		}
	}
	return count
}
