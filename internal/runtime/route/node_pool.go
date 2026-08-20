// Package route owns the instance-scoped runtime state used to select and
// score request nodes. Persistent node data remains in the repository; only
// request-local admission, stickiness and short-lived selection state live in
// this object.
package route

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/repository"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

const nodeSnapshotTTL = 500 * time.Millisecond

type nodeSnapshot struct {
	nodes    []nodes.Node
	byURI    map[string]repository.Node
	health   map[string]repository.NodeHealth
	loadedAt time.Time
}

type NodePool struct {
	repository *repository.SQLite
	sticky     *nodes.StickyNodePool

	mu           sync.Mutex
	snapshot     nodeSnapshot
	lastSelected map[string]int64
	recentUse    map[string]int
	inFlight     sync.Map
	cursor       atomic.Uint64
	onDelete     func(string)
	sortDesc     *bool

	progressMu   sync.Mutex
	progressCond *sync.Cond
	progress     nodes.TestProgress
}

func NewNodePool(store *repository.SQLite) (*NodePool, error) {
	if store == nil {
		return nil, errors.New("request node repository is nil")
	}
	pool := &NodePool{
		repository:   store,
		sticky:       nodes.NewStickyNodePool(),
		lastSelected: make(map[string]int64),
		recentUse:    make(map[string]int),
	}
	pool.progressCond = sync.NewCond(&pool.progressMu)
	return pool, nil
}

func (p *NodePool) StickyPool() *nodes.StickyNodePool { return p.sticky }

func (p *NodePool) SetDeleteCallback(callback func(string)) {
	p.mu.Lock()
	p.onDelete = callback
	p.mu.Unlock()
}

func (p *NodePool) List(ctx context.Context) ([]nodes.Node, map[string]*nodes.NodeHealth, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	values, _, healthValues, err := p.repository.LoadNodeState(ctx)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]string, len(values))
	list := make([]nodes.Node, 0, len(values))
	for _, value := range values {
		byID[value.ID] = value.RawURI
		list = append(list, nodes.Node{Type: value.Type, Name: value.Name, RawURI: value.RawURI, Disabled: value.Disabled})
	}
	health := make(map[string]*nodes.NodeHealth, len(healthValues))
	for _, value := range healthValues {
		uri := byID[value.NodeID]
		if uri == "" {
			continue
		}
		health[uri] = &nodes.NodeHealth{
			RawURI: uri, SuccessCount: value.SuccessCount, FailCount: value.FailCount,
			ConsecutiveFailures: value.ConsecutiveFailures, LastTestMs: value.LastTestMS,
			LastTestError: value.LastTestError, LastSuccessAt: value.LastSuccessAt,
			LastFailAt: value.LastFailAt, CooldownUntil: value.CooldownUntil,
			Last429At: value.Last429At, RateLimitCount: value.RateLimitCount,
			LastSubHealthyAt: value.LastSubHealthyAt, InFlight: p.InFlight(uri),
		}
	}
	if p.sortDesc != nil {
		desc := *p.sortDesc
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Disabled != list[j].Disabled {
				return !list[i].Disabled
			}
			left, right := nodeSortScore(health[list[i].RawURI]), nodeSortScore(health[list[j].RawURI])
			if left == right {
				return list[i].Name < list[j].Name
			}
			if desc {
				return left > right
			}
			return left < right
		})
	}
	return list, health, nil
}

func nodeSortScore(health *nodes.NodeHealth) float64 {
	if health == nil {
		return 1e18
	}
	if health.ConsecutiveFailures > 0 {
		return 1e6 + float64(health.ConsecutiveFailures)*1000
	}
	if health.LastTestMs > 0 {
		return health.LastTestMs
	}
	return 1e18
}

func (p *NodePool) ImportManualNodes(ctx context.Context, values []nodes.Node, replace bool) error {
	return p.mutateSources(ctx, func(state *persistentNodeState) error {
		manualKey := sourceKey(repository.NodeSource{SourceType: nodes.SourceManual})
		if replace {
			for _, sources := range state.sources {
				delete(sources, manualKey)
			}
		}
		for _, value := range values {
			record, err := repositoryNode(value)
			if err != nil {
				return err
			}
			if _, exists := state.records[record.ID]; !exists {
				state.order = append(state.order, record.ID)
			}
			state.records[record.ID] = record
			manual := repository.NodeSource{NodeID: record.ID, SourceType: nodes.SourceManual}
			state.ensureSources(record.ID)[manualKey] = manual
		}
		state.pruneWithoutSources()
		return nil
	})
}

func (p *NodePool) SetDisabled(ctx context.Context, uris []string, disabled bool) (int, error) {
	p.mu.Lock()
	values, _, _, err := p.repository.LoadNodeState(ctx)
	if err != nil {
		p.mu.Unlock()
		return 0, err
	}
	targets := make(map[string]struct{}, len(uris))
	for _, uri := range uris {
		targets[strings.TrimSpace(uri)] = struct{}{}
	}
	ids := make([]string, 0, len(targets))
	for _, value := range values {
		if _, exists := targets[value.RawURI]; exists {
			ids = append(ids, value.ID)
		}
	}
	err = p.repository.SetNodesDisabled(ctx, ids, disabled)
	if err == nil {
		p.snapshot.loadedAt = time.Time{}
	}
	p.mu.Unlock()
	return len(ids), err
}

func (p *NodePool) Delete(ctx context.Context, uris []string) (int, error) {
	targets := make(map[string]struct{}, len(uris))
	for _, uri := range uris {
		targets[strings.TrimSpace(uri)] = struct{}{}
	}
	removed := 0
	err := p.mutateSources(ctx, func(state *persistentNodeState) error {
		for nodeID, record := range state.records {
			if _, exists := targets[record.RawURI]; !exists {
				continue
			}
			delete(state.records, nodeID)
			delete(state.sources, nodeID)
			removed++
		}
		kept := state.order[:0]
		for _, nodeID := range state.order {
			if _, exists := state.records[nodeID]; exists {
				kept = append(kept, nodeID)
			}
		}
		state.order = kept
		return nil
	})
	return removed, err
}

func (p *NodePool) DeleteDisabled(ctx context.Context) (int, error) {
	list, _, err := p.List(ctx)
	if err != nil {
		return 0, err
	}
	uris := make([]string, 0)
	for _, value := range list {
		if value.Disabled {
			uris = append(uris, value.RawURI)
		}
	}
	return p.Delete(ctx, uris)
}

func (p *NodePool) SetSort(desc bool) {
	p.mu.Lock()
	p.sortDesc = new(bool)
	*p.sortDesc = desc
	p.mu.Unlock()
}

func (p *NodePool) PreviewDedup(ctx context.Context) (nodes.DedupPreview, error) {
	list, _, err := p.List(ctx)
	if err != nil {
		return nodes.DedupPreview{}, err
	}
	counts := make(map[string]int)
	for _, value := range list {
		identity, identityErr := transport.CanonicalURI(value.RawURI)
		if identityErr != nil {
			identity = value.RawURI
		}
		counts[identity]++
	}
	preview := nodes.DedupPreview{}
	for _, count := range counts {
		if count > 1 {
			preview.Groups++
			preview.DuplicateCount += count - 1
		}
	}
	return preview, nil
}

func (p *NodePool) Dedup(ctx context.Context) (int, error) {
	list, _, err := p.List(ctx)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{}, len(list))
	duplicates := make([]string, 0)
	for _, value := range list {
		identity, identityErr := transport.CanonicalURI(value.RawURI)
		if identityErr != nil {
			identity = value.RawURI
		}
		if _, exists := seen[identity]; exists {
			duplicates = append(duplicates, value.RawURI)
			continue
		}
		seen[identity] = struct{}{}
	}
	return p.Delete(ctx, duplicates)
}

func (p *NodePool) TestProgress() nodes.TestProgress {
	p.progressMu.Lock()
	defer p.progressMu.Unlock()
	return p.progress
}

func (p *NodePool) StartTest(total int) bool {
	p.progressMu.Lock()
	defer p.progressMu.Unlock()
	if p.progress.Running {
		return false
	}
	p.progress = nodes.TestProgress{Running: true, Total: total, CurrentNode: "准备中..."}
	return true
}

func (p *NodePool) UpdateTest(nodeName string, success bool) {
	p.progressMu.Lock()
	defer p.progressMu.Unlock()
	if !p.progress.Running || p.progress.Terminated {
		return
	}
	p.progress.Done++
	if success {
		p.progress.OkCount++
	} else {
		p.progress.FailCount++
	}
	p.progress.CurrentNode = nodeName
}

func (p *NodePool) FinishTest() {
	p.progressMu.Lock()
	p.progress.Running = false
	p.progress.Paused = false
	if p.progress.Terminated {
		p.progress.CurrentNode = "已终止"
	} else {
		p.progress.CurrentNode = "测试完成"
	}
	p.progressCond.Broadcast()
	p.progressMu.Unlock()
}

func (p *NodePool) PauseTest() {
	p.progressMu.Lock()
	if p.progress.Running && !p.progress.Terminated {
		p.progress.Paused = true
		p.progress.CurrentNode = "已暂停..."
	}
	p.progressMu.Unlock()
}

func (p *NodePool) ResumeTest() {
	p.progressMu.Lock()
	if p.progress.Running && p.progress.Paused {
		p.progress.Paused = false
		p.progress.CurrentNode = "恢复测试中..."
		p.progressCond.Broadcast()
	}
	p.progressMu.Unlock()
}

func (p *NodePool) TerminateTest() {
	p.progressMu.Lock()
	if p.progress.Running {
		p.progress.Terminated = true
		p.progress.Paused = false
		p.progress.CurrentNode = "正在终止..."
		p.progressCond.Broadcast()
	}
	p.progressMu.Unlock()
}

func (p *NodePool) CheckTestControl() bool {
	p.progressMu.Lock()
	defer p.progressMu.Unlock()
	for p.progress.Running && p.progress.Paused && !p.progress.Terminated {
		p.progressCond.Wait()
	}
	return !p.progress.Running || p.progress.Terminated
}

func (p *NodePool) Invalidate() {
	p.mu.Lock()
	p.snapshot.loadedAt = time.Time{}
	p.mu.Unlock()
}

func (p *NodePool) ReplaceSubscriptionNodes(ctx context.Context, subscriptionID string, values []nodes.Node, adoptManual bool) error {
	if strings.TrimSpace(subscriptionID) == "" {
		return errors.New("subscription ID is required")
	}
	return p.mutateSources(ctx, func(state *persistentNodeState) error {
		source := repository.NodeSource{SourceType: nodes.SourceSubscription, SourceID: subscriptionID}
		incoming := make(map[string]struct{}, len(values))
		for _, value := range values {
			record, err := repositoryNode(value)
			if err != nil {
				return err
			}
			incoming[record.ID] = struct{}{}
		}
		for nodeID, sources := range state.sources {
			if _, exists := sources[sourceKey(source)]; exists {
				if _, keep := incoming[nodeID]; !keep {
					delete(sources, sourceKey(source))
				}
			}
		}
		for _, value := range values {
			record, err := repositoryNode(value)
			if err != nil {
				return err
			}
			sources := state.ensureSources(record.ID)
			_, manual := sources[sourceKey(repository.NodeSource{SourceType: nodes.SourceManual})]
			if _, exists := state.records[record.ID]; !exists {
				state.order = append(state.order, record.ID)
			}
			if !manual || adoptManual {
				state.records[record.ID] = record
			}
			if adoptManual {
				delete(sources, sourceKey(repository.NodeSource{SourceType: nodes.SourceManual}))
			}
			sources[sourceKey(source)] = repository.NodeSource{NodeID: record.ID, SourceType: source.SourceType, SourceID: source.SourceID}
		}
		state.pruneWithoutSources()
		return nil
	})
}

func (p *NodePool) RemoveSubscriptionSource(ctx context.Context, subscriptionID string, deleteNodes bool) error {
	if strings.TrimSpace(subscriptionID) == "" {
		return errors.New("subscription ID is required")
	}
	return p.mutateSources(ctx, func(state *persistentNodeState) error {
		key := sourceKey(repository.NodeSource{SourceType: nodes.SourceSubscription, SourceID: subscriptionID})
		for nodeID, sources := range state.sources {
			if _, exists := sources[key]; !exists {
				continue
			}
			if !deleteNodes {
				manual := repository.NodeSource{NodeID: nodeID, SourceType: nodes.SourceManual}
				sources[sourceKey(manual)] = manual
			}
			delete(sources, key)
		}
		state.pruneWithoutSources()
		return nil
	})
}

type persistentNodeState struct {
	records map[string]repository.Node
	order   []string
	sources map[string]map[string]repository.NodeSource
	before  map[string]string
}

func (p *NodePool) mutateSources(ctx context.Context, update func(*persistentNodeState) error) error {
	p.mu.Lock()
	values, sources, _, err := p.repository.LoadNodeState(ctx)
	if err != nil {
		p.mu.Unlock()
		return err
	}
	state := persistentNodeState{
		records: make(map[string]repository.Node, len(values)), order: make([]string, 0, len(values)),
		sources: make(map[string]map[string]repository.NodeSource), before: make(map[string]string, len(values)),
	}
	for _, value := range values {
		state.records[value.ID] = value
		state.order = append(state.order, value.ID)
		state.before[value.ID] = value.RawURI
	}
	for _, source := range sources {
		state.ensureSources(source.NodeID)[sourceKey(source)] = source
	}
	if err := update(&state); err != nil {
		p.mu.Unlock()
		return err
	}
	records, persistedSources := state.flatten()
	if err := p.repository.SaveNodeState(ctx, records, persistedSources); err != nil {
		p.mu.Unlock()
		return err
	}
	p.snapshot.loadedAt = time.Time{}
	callback := p.onDelete
	removed := state.removedURIs()
	p.mu.Unlock()
	if callback != nil {
		for _, uri := range removed {
			callback(uri)
		}
	}
	return nil
}

func (s *persistentNodeState) ensureSources(nodeID string) map[string]repository.NodeSource {
	if s.sources[nodeID] == nil {
		s.sources[nodeID] = make(map[string]repository.NodeSource)
	}
	return s.sources[nodeID]
}

func (s *persistentNodeState) pruneWithoutSources() {
	kept := s.order[:0]
	for _, nodeID := range s.order {
		if len(s.sources[nodeID]) == 0 {
			delete(s.records, nodeID)
			delete(s.sources, nodeID)
			continue
		}
		kept = append(kept, nodeID)
	}
	s.order = kept
}

func (s *persistentNodeState) flatten() ([]repository.Node, []repository.NodeSource) {
	records := make([]repository.Node, 0, len(s.order))
	sources := make([]repository.NodeSource, 0)
	for _, nodeID := range s.order {
		record, exists := s.records[nodeID]
		if !exists {
			continue
		}
		records = append(records, record)
		keys := make([]string, 0, len(s.sources[nodeID]))
		for key := range s.sources[nodeID] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			sources = append(sources, s.sources[nodeID][key])
		}
	}
	return records, sources
}

func (s *persistentNodeState) removedURIs() []string {
	removed := make([]string, 0)
	for nodeID, uri := range s.before {
		if _, exists := s.records[nodeID]; !exists {
			removed = append(removed, uri)
		}
	}
	sort.Strings(removed)
	return removed
}

func sourceKey(source repository.NodeSource) string {
	return source.SourceType + "\x00" + source.SourceID
}

func repositoryNode(value nodes.Node) (repository.Node, error) {
	identity, err := transport.ProxyIdentity(strings.TrimSpace(value.RawURI))
	if err != nil {
		return repository.Node{}, err
	}
	sum := sha256.Sum256([]byte("rn\x00" + identity.SemanticFingerprint))
	return repository.Node{
		ID: "rn_" + hex.EncodeToString(sum[:12]), RawURI: strings.TrimSpace(value.RawURI),
		CanonicalIdentity: identity.SemanticFingerprint, EndpointFingerprint: identity.EndpointFingerprint,
		Type: value.Type, Name: value.Name, Disabled: value.Disabled,
	}, nil
}

func (p *NodePool) loadLocked(ctx context.Context) (nodeSnapshot, error) {
	if !p.snapshot.loadedAt.IsZero() && time.Since(p.snapshot.loadedAt) < nodeSnapshotTTL {
		return p.snapshot, nil
	}
	values, _, healthValues, err := p.repository.LoadNodeState(ctx)
	if err != nil {
		return nodeSnapshot{}, err
	}
	result := nodeSnapshot{
		nodes: make([]nodes.Node, 0, len(values)), byURI: make(map[string]repository.Node, len(values)),
		health: make(map[string]repository.NodeHealth, len(healthValues)), loadedAt: time.Now(),
	}
	byID := make(map[string]string, len(values))
	for _, value := range values {
		result.nodes = append(result.nodes, nodes.Node{Type: value.Type, Name: value.Name, RawURI: value.RawURI, Disabled: value.Disabled})
		result.byURI[value.RawURI] = value
		byID[value.ID] = value.RawURI
	}
	for _, health := range healthValues {
		if uri := byID[health.NodeID]; uri != "" {
			result.health[uri] = health
		}
	}
	p.snapshot = result
	return result, nil
}

func (p *NodePool) NodeName(uri string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot, err := p.loadLocked(context.Background())
	if err != nil {
		return "Unknown"
	}
	if value, ok := snapshot.byURI[uri]; ok && value.Name != "" {
		return value.Name
	}
	return "Unknown"
}

func (p *NodePool) Select(k, topK int, stickyBonus bool, reserve bool) []nodes.Node {
	if k <= 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot, err := p.loadLocked(context.Background())
	if err != nil {
		return nil
	}
	now := time.Now().Unix()
	type candidate struct {
		node     nodes.Node
		tier     int
		inFlight int32
		sticky   bool
		latency  float64
		selected int64
	}
	candidates := make([]candidate, 0, len(snapshot.nodes))
	for _, node := range snapshot.nodes {
		health := snapshot.health[node.RawURI]
		if node.Disabled || health.CooldownUntil > now {
			continue
		}
		tier := 1
		if health.LastSubHealthyAt > 0 {
			tier = 2
		}
		candidates = append(candidates, candidate{
			node: node, tier: tier, inFlight: p.InFlight(node.RawURI),
			sticky: stickyBonus && p.sticky.IsSticky(node.RawURI), latency: health.LastTestMS,
			selected: p.lastSelected[node.RawURI],
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.tier != right.tier {
			return left.tier < right.tier
		}
		if left.inFlight != right.inFlight {
			return left.inFlight < right.inFlight
		}
		if left.sticky != right.sticky {
			return left.sticky
		}
		if left.selected != right.selected {
			return left.selected < right.selected
		}
		if left.latency > 0 && right.latency > 0 && left.latency != right.latency {
			return left.latency < right.latency
		}
		return left.node.RawURI < right.node.RawURI
	})
	if topK <= 0 {
		topK = 80
	}
	limit := max(k, topK)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	selected := make([]nodes.Node, 0, min(k, len(candidates)))
	if len(candidates) > 0 {
		start := int(p.cursor.Add(1)-1) % len(candidates)
		for index := 0; index < len(candidates) && len(selected) < k; index++ {
			item := candidates[(start+index)%len(candidates)]
			selected = append(selected, item.node)
			p.lastSelected[item.node.RawURI] = now
			p.recentUse[item.node.RawURI]++
			if reserve {
				p.IncInFlight(item.node.RawURI)
			}
		}
	}
	return selected
}

func (p *NodePool) IncInFlight(uri string) {
	value, _ := p.inFlight.LoadOrStore(uri, new(atomic.Int32))
	value.(*atomic.Int32).Add(1)
}

func (p *NodePool) DecInFlight(uri string) {
	value, ok := p.inFlight.Load(uri)
	if !ok {
		return
	}
	counter := value.(*atomic.Int32)
	for {
		current := counter.Load()
		if current <= 0 || counter.CompareAndSwap(current, current-1) {
			return
		}
	}
}

func (p *NodePool) InFlight(uri string) int32 {
	if value, ok := p.inFlight.Load(uri); ok {
		return value.(*atomic.Int32).Load()
	}
	return 0
}

func (p *NodePool) AverageLatency() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot, err := p.loadLocked(context.Background())
	if err != nil {
		return 500
	}
	now := time.Now().Unix()
	var total float64
	var count int
	for _, node := range snapshot.nodes {
		health := snapshot.health[node.RawURI]
		if !node.Disabled && health.LastTestMS > 0 && health.CooldownUntil <= now {
			total += health.LastTestMS
			count++
		}
	}
	if count == 0 {
		return 500
	}
	return total / float64(count)
}

func (p *NodePool) RecordResult(uri string, success bool, latencyMS float64, message string) {
	p.record(uri, success, latencyMS, message, false, 0)
}

func (p *NodePool) RecordRateLimit(uri string, cooldownSeconds int) {
	p.record(uri, false, 0, "429 Rate Limit", true, cooldownSeconds)
}

func (p *NodePool) record(uri string, success bool, latencyMS float64, message string, rateLimited bool, cooldownSeconds int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot, err := p.loadLocked(context.Background())
	if err != nil {
		return
	}
	node, ok := snapshot.byURI[uri]
	if !ok {
		return
	}
	health := snapshot.health[uri]
	health.NodeID = node.ID
	now := time.Now().Unix()
	health.LastTestMS = latencyMS
	health.LastTestError = message
	if success {
		health.SuccessCount++
		health.ConsecutiveFailures = 0
		health.LastSuccessAt = now
		health.CooldownUntil = 0
		health.LastSubHealthyAt = 0
		health.Last429At = 0
		health.RateLimitCount = 0
	} else {
		health.FailCount++
		health.ConsecutiveFailures++
		health.LastFailAt = now
		health.LastSubHealthyAt = now
		if rateLimited {
			health.Last429At = now
			health.RateLimitCount++
			health.CooldownUntil = now + int64(max(0, cooldownSeconds))
		} else {
			failures := max(1, health.ConsecutiveFailures)
			cooldown := min(1800, 30*(1<<min(failures-1, 6)))
			health.CooldownUntil = now + int64(cooldown)
		}
	}
	if p.repository.UpsertNodeHealthBatch(context.Background(), []repository.NodeHealth{health}) == nil {
		p.snapshot.loadedAt = time.Time{}
	}
}
