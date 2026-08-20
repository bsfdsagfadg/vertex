package legacy

import (
	"sort"

	"github.com/bsfdsagfadg/vertex/internal/repository"
)

func (b *Builder) Snapshot() repository.Snapshot {
	snapshot := repository.Snapshot{}
	for _, value := range b.Nodes {
		snapshot.Nodes = append(snapshot.Nodes, value)
	}
	for _, value := range b.NodeSources {
		snapshot.NodeSources = append(snapshot.NodeSources, value)
	}
	for _, value := range b.NodeHealth {
		snapshot.NodeHealth = append(snapshot.NodeHealth, value)
	}
	for _, value := range b.GlobalProxies {
		snapshot.GlobalProxies = append(snapshot.GlobalProxies, value)
	}
	for _, value := range b.GlobalProxySources {
		snapshot.GlobalProxySources = append(snapshot.GlobalProxySources, value)
	}
	for _, value := range b.GlobalProxyHealth {
		snapshot.GlobalProxyHealth = append(snapshot.GlobalProxyHealth, value)
	}
	for _, value := range b.UserAgents {
		snapshot.UserAgents = append(snapshot.UserAgents, value)
	}
	for _, value := range b.Subscriptions {
		snapshot.Subscriptions = append(snapshot.Subscriptions, value)
	}
	sort.Slice(snapshot.Nodes, func(i, j int) bool { return snapshot.Nodes[i].ID < snapshot.Nodes[j].ID })
	sort.Slice(snapshot.NodeSources, func(i, j int) bool {
		if snapshot.NodeSources[i].NodeID != snapshot.NodeSources[j].NodeID {
			return snapshot.NodeSources[i].NodeID < snapshot.NodeSources[j].NodeID
		}
		if snapshot.NodeSources[i].SourceType != snapshot.NodeSources[j].SourceType {
			return snapshot.NodeSources[i].SourceType < snapshot.NodeSources[j].SourceType
		}
		return snapshot.NodeSources[i].SourceID < snapshot.NodeSources[j].SourceID
	})
	sort.Slice(snapshot.GlobalProxies, func(i, j int) bool { return snapshot.GlobalProxies[i].ID < snapshot.GlobalProxies[j].ID })
	sort.Slice(snapshot.Subscriptions, func(i, j int) bool { return snapshot.Subscriptions[i].ID < snapshot.Subscriptions[j].ID })
	sort.Slice(snapshot.UserAgents, func(i, j int) bool { return snapshot.UserAgents[i].ID < snapshot.UserAgents[j].ID })
	return snapshot
}
