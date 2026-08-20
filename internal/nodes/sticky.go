package nodes

import (
	"sync"
	"time"
)

const defaultStickyTTL = 30 * time.Minute

type StickyNodePool struct { //nolint:govet
	mu   sync.Mutex
	pool map[string]time.Time
	ttl  time.Duration
}

var globalStickyPool = NewStickyNodePool() //nolint:gochecknoglobals

func GetStickyPool() *StickyNodePool {
	return globalStickyPool
}

func NewStickyNodePool() *StickyNodePool {
	return &StickyNodePool{ //nolint:exhaustruct
		pool: make(map[string]time.Time),
		ttl:  defaultStickyTTL,
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
	p.pruneLocked(time.Now())
	_, exists := p.pool[uri]
	return exists
}

func (p *StickyNodePool) AvailableCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(time.Now())
	return len(p.pool)
}

func (p *StickyNodePool) StaleCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	count := 0
	for _, lastSeen := range p.pool {
		if now.Sub(lastSeen) >= p.ttl {
			count++
		}
	}
	return count
}

func (p *StickyNodePool) List() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(time.Now())
	uris := make([]string, 0, len(p.pool))
	for uri := range p.pool {
		uris = append(uris, uri)
	}
	return uris
}

func (p *StickyNodePool) pruneLocked(now time.Time) {
	for uri, lastSeen := range p.pool {
		if now.Sub(lastSeen) >= p.ttl {
			delete(p.pool, uri)
		}
	}
}
