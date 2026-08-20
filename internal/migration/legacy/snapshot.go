package legacy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/repository"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

type Builder struct {
	Nodes              map[string]repository.Node
	NodeSources        map[string]repository.NodeSource
	NodeHealth         map[string]repository.NodeHealth
	GlobalProxies      map[string]repository.GlobalProxy
	GlobalProxySources map[string]repository.GlobalProxySource
	GlobalProxyHealth  map[string]repository.GlobalProxyHealth
	UserAgents         map[string]repository.SubscriptionUserAgent
	Subscriptions      map[string]repository.Subscription
	nodeIDByRawURI     map[string]string
	proxyIDByRawURI    map[string]string
}

func NewBuilder() *Builder {
	return &Builder{
		Nodes:              map[string]repository.Node{},
		NodeSources:        map[string]repository.NodeSource{},
		NodeHealth:         map[string]repository.NodeHealth{},
		GlobalProxies:      map[string]repository.GlobalProxy{},
		GlobalProxySources: map[string]repository.GlobalProxySource{},
		GlobalProxyHealth:  map[string]repository.GlobalProxyHealth{},
		UserAgents:         map[string]repository.SubscriptionUserAgent{},
		Subscriptions:      map[string]repository.Subscription{},
		nodeIDByRawURI:     map[string]string{},
		proxyIDByRawURI:    map[string]string{},
	}
}

func BuildSnapshot(ctx context.Context, dataRoot string) (repository.Snapshot, error) {
	builder := NewBuilder()
	if err := builder.readNodeJSON(filepath.Join(dataRoot, "nodes.json")); err != nil {
		return repository.Snapshot{}, err
	}
	if err := builder.readNodeHealthJSON(filepath.Join(dataRoot, "node_health.json")); err != nil {
		return repository.Snapshot{}, err
	}
	if err := builder.readSubscriptionsJSON(filepath.Join(dataRoot, "subscriptions.json")); err != nil {
		return repository.Snapshot{}, err
	}
	if err := builder.readProxyCandidatesJSON(filepath.Join(dataRoot, "proxy_url_candidates.json")); err != nil {
		return repository.Snapshot{}, err
	}
	if err := builder.readPinnedProxy(filepath.Join(dataRoot, "config.json")); err != nil {
		return repository.Snapshot{}, err
	}
	if err := builder.readDatabase(ctx, filepath.Join(dataRoot, "data.db")); err != nil {
		return repository.Snapshot{}, err
	}
	return builder.Snapshot(), nil
}

func (b *Builder) addNode(rawURI, nodeType, name string, disabled bool, sourceType, sourceID string) error {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return fmt.Errorf("legacy request node has an empty URI")
	}
	identity, err := transport.ProxyIdentity(rawURI)
	if err != nil {
		return fmt.Errorf("parse legacy request node identity: %w", err)
	}
	id := stableID("rn", identity.SemanticFingerprint)
	if existing, ok := b.Nodes[id]; ok {
		existing.Disabled = existing.Disabled && disabled
		if existing.Name == "" {
			existing.Name = name
		}
		b.Nodes[id] = existing
	} else {
		b.Nodes[id] = repository.Node{
			ID: id, RawURI: rawURI, CanonicalIdentity: identity.SemanticFingerprint,
			EndpointFingerprint: identity.EndpointFingerprint, Type: nodeType, Name: name, Disabled: disabled,
		}
	}
	b.nodeIDByRawURI[rawURI] = id
	if sourceType == "" {
		sourceType = "legacy"
	}
	source := repository.NodeSource{NodeID: id, SourceType: sourceType, SourceID: sourceID}
	b.NodeSources[id+"\x00"+sourceType+"\x00"+sourceID] = source
	return nil
}

func (b *Builder) addGlobalProxy(rawURI, name, proxyType string, disabled, pinned bool, sourceType, sourceID string) error {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return nil
	}
	identity, err := transport.ProxyIdentity(rawURI)
	if err != nil {
		return fmt.Errorf("parse legacy global proxy identity: %w", err)
	}
	id := stableID("gp", identity.SemanticFingerprint)
	if existing, ok := b.GlobalProxies[id]; ok {
		existing.Disabled = existing.Disabled && disabled
		existing.Pinned = existing.Pinned || pinned
		if existing.Name == "" {
			existing.Name = name
		}
		b.GlobalProxies[id] = existing
	} else {
		b.GlobalProxies[id] = repository.GlobalProxy{
			ID: id, RawURI: rawURI, CanonicalIdentity: identity.SemanticFingerprint,
			EndpointFingerprint: identity.EndpointFingerprint, Name: name, Type: proxyType,
			Disabled: disabled, Pinned: pinned,
		}
	}
	b.proxyIDByRawURI[rawURI] = id
	if sourceType == "" {
		sourceType = "legacy"
	}
	source := repository.GlobalProxySource{GlobalProxyID: id, SourceType: sourceType, SourceID: sourceID}
	b.GlobalProxySources[id+"\x00"+sourceType+"\x00"+sourceID] = source
	return nil
}

func stableID(prefix, identity string) string {
	sum := sha256.Sum256([]byte(prefix + "\x00" + identity))
	return prefix + "_" + hex.EncodeToString(sum[:12])
}

func readJSONIfExists(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read legacy %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse legacy %s: %w", filepath.Base(path), err)
	}
	return nil
}
