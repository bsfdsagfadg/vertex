package nodes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/repository"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

const (
	SourceLegacy       = "legacy"
	SourceManual       = "manual"
	SourceSubscription = "subscription"
)

type NodeSource struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

type DedupPreview struct {
	Groups         int `json:"groups"`
	DuplicateCount int `json:"duplicate_count"`
}

func addNodeSourceUnsafe(rawURI string, source NodeSource) {
	if rawURI == "" || source.Type == "" {
		return
	}
	if nodeSources[rawURI] == nil {
		nodeSources[rawURI] = make(map[NodeSource]struct{})
	}
	nodeSources[rawURI][source] = struct{}{}
}

func removeNodeSourceUnsafe(rawURI string, source NodeSource) {
	sources := nodeSources[rawURI]
	delete(sources, source)
	if len(sources) == 0 {
		delete(nodeSources, rawURI)
	}
}

func hasNodeSourceUnsafe(rawURI string, source NodeSource) bool {
	_, ok := nodeSources[rawURI][source]
	return ok
}

func hasSourceTypeUnsafe(rawURI, sourceType string) bool {
	for source := range nodeSources[rawURI] {
		if source.Type == sourceType {
			return true
		}
	}
	return false
}

func reconcileLegacySourceUnsafe(rawURI string) {
	if hasSourceTypeUnsafe(rawURI, SourceSubscription) {
		removeNodeSourceUnsafe(rawURI, NodeSource{Type: SourceLegacy})
	}
}

func cloneNodeSourcesUnsafe() map[string]map[NodeSource]struct{} {
	cloned := make(map[string]map[NodeSource]struct{}, len(nodeSources))
	for rawURI, sources := range nodeSources {
		cloned[rawURI] = make(map[NodeSource]struct{}, len(sources))
		for source := range sources {
			cloned[rawURI][source] = struct{}{}
		}
	}
	return cloned
}

func cloneHealthMapUnsafe() map[string]*NodeHealth {
	cloned := make(map[string]*NodeHealth, len(healthMap))
	for rawURI, health := range healthMap {
		if health == nil {
			cloned[rawURI] = nil
			continue
		}
		copyHealth := *health
		cloned[rawURI] = &copyHealth
	}
	return cloned
}

func cloneStickyPoolUnsafe() map[string]time.Time {
	globalStickyPool.mu.Lock()
	defer globalStickyPool.mu.Unlock()
	cloned := make(map[string]time.Time, len(globalStickyPool.pool))
	for rawURI, sticky := range globalStickyPool.pool {
		cloned[rawURI] = sticky
	}
	return cloned
}

func restoreStickyPoolUnsafe(snapshot map[string]time.Time) {
	globalStickyPool.mu.Lock()
	defer globalStickyPool.mu.Unlock()
	globalStickyPool.pool = snapshot
}

func saveNodeStateUnsafe() error {
	if nodeRepository == nil {
		return nil
	}
	repositoryNodes := make([]repository.Node, 0, len(nodeList))
	ids := make(map[string]string, len(nodeList))
	for _, node := range nodeList {
		converted, err := repositoryNode(node)
		if err != nil {
			return err
		}
		repositoryNodes = append(repositoryNodes, converted)
		ids[node.RawURI] = converted.ID
	}
	repositorySources := make([]repository.NodeSource, 0)
	for rawURI, sources := range nodeSources {
		id := ids[rawURI]
		if id == "" {
			continue
		}
		for source := range sources {
			repositorySources = append(repositorySources, repository.NodeSource{
				NodeID: id, SourceType: source.Type, SourceID: source.ID,
			})
		}
	}
	return nodeRepository.SaveNodeState(context.Background(), repositoryNodes, repositorySources)
}

func repositoryNode(node Node) (repository.Node, error) {
	identity, err := transport.ProxyIdentity(node.RawURI)
	if err != nil {
		return repository.Node{}, fmt.Errorf("derive request node identity: %w", err)
	}
	sum := sha256.Sum256([]byte("rn\x00" + identity.SemanticFingerprint))
	return repository.Node{
		ID: "rn_" + hex.EncodeToString(sum[:12]), RawURI: node.RawURI,
		CanonicalIdentity: identity.SemanticFingerprint, EndpointFingerprint: identity.EndpointFingerprint,
		Type: node.Type, Name: node.Name, Disabled: node.Disabled,
	}, nil
}

func GetNodeSources(rawURI string) []NodeSource {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	sources := make([]NodeSource, 0, len(nodeSources[rawURI]))
	for source := range nodeSources[rawURI] {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Type == sources[j].Type {
			return sources[i].ID < sources[j].ID
		}
		return sources[i].Type < sources[j].Type
	})
	return sources
}

func upsertNodesUnsafe(newNodes []Node, source NodeSource, adoptManual bool) {
	existing := make(map[string]int, len(nodeList))
	for index, node := range nodeList {
		existing[node.RawURI] = index
	}
	for _, node := range newNodes {
		if strings.TrimSpace(node.RawURI) == "" {
			continue
		}
		index, ok := existing[node.RawURI]
		if !ok {
			nodeList = append(nodeList, node)
			existing[node.RawURI] = len(nodeList) - 1
		} else if source.Type != SourceSubscription || adoptManual || !hasSourceTypeUnsafe(node.RawURI, SourceManual) {
			nodeList[index] = node
		}
		addNodeSourceUnsafe(node.RawURI, source)
		if source.Type == SourceSubscription {
			if adoptManual {
				removeNodeSourceUnsafe(node.RawURI, NodeSource{Type: SourceManual})
			}
			reconcileLegacySourceUnsafe(node.RawURI)
		}
	}
}

func UpsertNodesWithSource(newNodes []Node, sourceType, sourceID string) error {
	mu.Lock()
	ensureLoaded()
	oldNodes := append([]Node(nil), nodeList...)
	oldSources := cloneNodeSourcesUnsafe()
	oldHealth := cloneHealthMapUnsafe()
	upsertNodesUnsafe(newNodes, NodeSource{Type: sourceType, ID: sourceID}, false)
	pruneHealthUnsafe()
	if err := saveNodeStateUnsafe(); err != nil {
		nodeList = oldNodes
		nodeSources = oldSources
		healthMap = oldHealth
		mu.Unlock()
		return err
	}
	mu.Unlock()
	return nil
}

func MergeNodes(newNodes []Node) {
	if err := UpsertNodesWithSource(newNodes, SourceManual, ""); err != nil {
		log.Printf("[Nodes] 合并手动节点失败: %v", err)
	}
}

func pruneNodesWithoutSourcesUnsafe() []string {
	kept := make([]Node, 0, len(nodeList))
	removed := make([]string, 0)
	for _, node := range nodeList {
		if len(nodeSources[node.RawURI]) == 0 {
			removed = append(removed, node.RawURI)
			delete(nodeSources, node.RawURI)
			continue
		}
		kept = append(kept, node)
	}
	nodeList = kept
	return removed
}

func finishRemovedNodesUnsafe(removed []string) {
	for _, rawURI := range removed {
		delete(healthMap, rawURI)
		globalStickyPool.Evict(rawURI)
	}
}

func notifyRemovedNodes(removed []string, callback func(string)) {
	if callback == nil {
		return
	}
	for _, rawURI := range removed {
		callback(rawURI)
	}
}

func ReplaceSubscriptionNodes(subscriptionID string, newNodes []Node, adoptManual bool) error {
	if strings.TrimSpace(subscriptionID) == "" {
		return fmt.Errorf("subscription ID is required")
	}
	mu.Lock()
	ensureLoaded()
	oldNodes := append([]Node(nil), nodeList...)
	oldSources := cloneNodeSourcesUnsafe()

	source := NodeSource{Type: SourceSubscription, ID: subscriptionID}
	newURIs := make(map[string]struct{}, len(newNodes))
	for _, node := range newNodes {
		if node.RawURI != "" {
			newURIs[node.RawURI] = struct{}{}
		}
	}
	for rawURI := range nodeSources {
		if !hasNodeSourceUnsafe(rawURI, source) {
			continue
		}
		if _, keep := newURIs[rawURI]; !keep {
			removeNodeSourceUnsafe(rawURI, source)
		}
	}
	upsertNodesUnsafe(newNodes, source, adoptManual)
	removed := pruneNodesWithoutSourcesUnsafe()
	if err := saveNodeStateUnsafe(); err != nil {
		nodeList = oldNodes
		nodeSources = oldSources
		mu.Unlock()
		return err
	}
	finishRemovedNodesUnsafe(removed)
	callback := getDeleteNodeCallback()
	mu.Unlock()
	notifyRemovedNodes(removed, callback)
	return nil
}

func RemoveSubscriptionSource(subscriptionID string, deleteNodes bool) error {
	if strings.TrimSpace(subscriptionID) == "" {
		return fmt.Errorf("subscription ID is required")
	}
	mu.Lock()
	ensureLoaded()
	oldNodes := append([]Node(nil), nodeList...)
	oldSources := cloneNodeSourcesUnsafe()
	source := NodeSource{Type: SourceSubscription, ID: subscriptionID}
	for rawURI := range nodeSources {
		if !hasNodeSourceUnsafe(rawURI, source) {
			continue
		}
		if !deleteNodes {
			addNodeSourceUnsafe(rawURI, NodeSource{Type: SourceManual})
		}
		removeNodeSourceUnsafe(rawURI, source)
	}
	removed := pruneNodesWithoutSourcesUnsafe()
	if err := saveNodeStateUnsafe(); err != nil {
		nodeList = oldNodes
		nodeSources = oldSources
		mu.Unlock()
		return err
	}
	finishRemovedNodesUnsafe(removed)
	callback := getDeleteNodeCallback()
	mu.Unlock()
	notifyRemovedNodes(removed, callback)
	return nil
}

func DeleteNodesBySource(source string) {
	if err := RemoveSubscriptionSource(source, true); err != nil {
		log.Printf("[Nodes] 删除订阅来源 %s 失败: %v", source, err)
	}
}

func ImportManualNodes(newNodes []Node, replace bool) error {
	mu.Lock()
	ensureLoaded()
	oldNodes := append([]Node(nil), nodeList...)
	oldSources := cloneNodeSourcesUnsafe()
	if replace {
		for rawURI := range nodeSources {
			removeNodeSourceUnsafe(rawURI, NodeSource{Type: SourceManual})
		}
	}
	upsertNodesUnsafe(newNodes, NodeSource{Type: SourceManual}, false)
	removed := pruneNodesWithoutSourcesUnsafe()
	if err := saveNodeStateUnsafe(); err != nil {
		nodeList = oldNodes
		nodeSources = oldSources
		mu.Unlock()
		return err
	}
	finishRemovedNodesUnsafe(removed)
	callback := getDeleteNodeCallback()
	mu.Unlock()
	notifyRemovedNodes(removed, callback)
	return nil
}

func canonicalNodeKey(rawURI string) string {
	key, err := transport.CanonicalURI(rawURI)
	if err != nil {
		return strings.TrimSpace(rawURI)
	}
	return key
}

func previewDedupUnsafe() DedupPreview {
	counts := make(map[string]int)
	for _, node := range nodeList {
		counts[canonicalNodeKey(node.RawURI)]++
	}
	preview := DedupPreview{}
	for _, count := range counts {
		if count > 1 {
			preview.Groups++
			preview.DuplicateCount += count - 1
		}
	}
	return preview
}

func PreviewDedupNodes() DedupPreview {
	mu.Lock()
	defer mu.Unlock()
	ensureLoaded()
	return previewDedupUnsafe()
}

func mergeNodeSourcesUnsafe(keepURI, removeURI string) {
	for source := range nodeSources[removeURI] {
		addNodeSourceUnsafe(keepURI, source)
	}
	delete(nodeSources, removeURI)
	reconcileLegacySourceUnsafe(keepURI)
}

func preferCandidateUnsafe(current, candidate Node) bool {
	currentManual := hasSourceTypeUnsafe(current.RawURI, SourceManual)
	candidateManual := hasSourceTypeUnsafe(candidate.RawURI, SourceManual)
	if currentManual != candidateManual {
		return candidateManual
	}
	if current.Disabled != candidate.Disabled {
		return !candidate.Disabled
	}
	currentHealth := healthMap[current.RawURI]
	candidateHealth := healthMap[candidate.RawURI]
	if currentHealth == nil || candidateHealth == nil {
		return currentHealth == nil && candidateHealth != nil
	}
	return candidateHealth.LastSuccessAt > currentHealth.LastSuccessAt
}

func mergeNodeRuntimeStateUnsafe(keepURI, removeURI string) {
	if healthMap[keepURI] == nil && healthMap[removeURI] != nil {
		healthMap[keepURI] = healthMap[removeURI]
	}
	delete(healthMap, removeURI)
	if globalStickyPool.IsSticky(removeURI) {
		globalStickyPool.Add(keepURI)
	}
	globalStickyPool.Evict(removeURI)
}

func DedupNodes() int {
	mu.Lock()
	ensureLoaded()
	oldNodes := append([]Node(nil), nodeList...)
	oldSources := cloneNodeSourcesUnsafe()
	oldHealth := cloneHealthMapUnsafe()
	oldSticky := cloneStickyPoolUnsafe()
	winners := make(map[string]int)
	kept := make([]Node, 0, len(nodeList))
	removedURIs := make([]string, 0)
	for _, candidate := range nodeList {
		key := canonicalNodeKey(candidate.RawURI)
		winnerIndex, duplicate := winners[key]
		if !duplicate {
			winners[key] = len(kept)
			kept = append(kept, candidate)
			continue
		}
		winner := kept[winnerIndex]
		if preferCandidateUnsafe(winner, candidate) {
			mergeNodeSourcesUnsafe(candidate.RawURI, winner.RawURI)
			mergeNodeRuntimeStateUnsafe(candidate.RawURI, winner.RawURI)
			kept[winnerIndex] = candidate
			removedURIs = append(removedURIs, winner.RawURI)
		} else {
			mergeNodeSourcesUnsafe(winner.RawURI, candidate.RawURI)
			mergeNodeRuntimeStateUnsafe(winner.RawURI, candidate.RawURI)
			removedURIs = append(removedURIs, candidate.RawURI)
		}
	}
	nodeList = kept
	if err := saveNodeStateUnsafe(); err != nil {
		nodeList = oldNodes
		nodeSources = oldSources
		healthMap = oldHealth
		restoreStickyPoolUnsafe(oldSticky)
		mu.Unlock()
		log.Printf("[Nodes] 去重保存失败: %v", err)
		return 0
	}
	saveHealthUnsafe()
	callback := getDeleteNodeCallback()
	mu.Unlock()
	notifyRemovedNodes(removedURIs, callback)
	return len(removedURIs)
}
