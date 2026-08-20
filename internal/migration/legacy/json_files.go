package legacy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bsfdsagfadg/vertex/internal/repository"
)

func (b *Builder) readNodeJSON(path string) error {
	var document struct {
		Nodes []struct {
			Type     string `json:"type"`
			Name     string `json:"name"`
			RawURI   string `json:"raw_uri"`
			Disabled bool   `json:"disabled"`
			Source   string `json:"source"`
		} `json:"nodes"`
	}
	if err := readJSONIfExists(path, &document); err != nil {
		return err
	}
	for _, node := range document.Nodes {
		sourceType, sourceID := "legacy", ""
		if node.Source != "" {
			sourceType, sourceID = "subscription", node.Source
		}
		if err := b.addNode(node.RawURI, node.Type, node.Name, node.Disabled, sourceType, sourceID); err != nil {
			return fmt.Errorf("convert %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func (b *Builder) readNodeHealthJSON(path string) error {
	var document map[string]struct {
		SuccessCount        int     `json:"success_count"`
		FailCount           int     `json:"fail_count"`
		ConsecutiveFailures int     `json:"consecutive_failures"`
		LastTestMS          float64 `json:"last_test_ms"`
		LastTestError       string  `json:"last_test_error"`
		LastSuccessAt       int64   `json:"last_success_at"`
		LastFailAt          int64   `json:"last_fail_at"`
		CooldownUntil       int64   `json:"cooldown_until"`
		Last429At           int64   `json:"last_429_at"`
		RateLimitCount      int     `json:"rate_limit_count"`
		LastSubHealthyAt    int64   `json:"last_sub_healthy_at"`
	}
	if err := readJSONIfExists(path, &document); err != nil {
		return err
	}
	for rawURI, health := range document {
		nodeID := b.nodeIDByRawURI[rawURI]
		if nodeID == "" {
			return fmt.Errorf("legacy node health references unknown request node")
		}
		b.NodeHealth[nodeID] = repository.NodeHealth{
			NodeID: nodeID, SuccessCount: health.SuccessCount, FailCount: health.FailCount,
			ConsecutiveFailures: health.ConsecutiveFailures, LastTestMS: health.LastTestMS,
			LastTestError: health.LastTestError, LastSuccessAt: health.LastSuccessAt,
			LastFailAt: health.LastFailAt, CooldownUntil: health.CooldownUntil,
			Last429At: health.Last429At, RateLimitCount: health.RateLimitCount,
			LastSubHealthyAt: health.LastSubHealthyAt,
		}
	}
	return nil
}

func (b *Builder) readSubscriptionsJSON(path string) error {
	var document struct {
		Subscriptions []repository.Subscription          `json:"subscriptions"`
		CustomUAs     []repository.SubscriptionUserAgent `json:"custom_uas"`
	}
	if err := readJSONIfExists(path, &document); err != nil {
		return err
	}
	for _, ua := range document.CustomUAs {
		if ua.ID == "" || ua.Name == "" || ua.UserAgent == "" {
			return fmt.Errorf("legacy subscription user agent is incomplete")
		}
		b.UserAgents[ua.ID] = ua
	}
	for _, subscription := range document.Subscriptions {
		if subscription.ID == "" || subscription.URL == "" {
			return fmt.Errorf("legacy subscription is incomplete")
		}
		if subscription.CustomUAID != "" {
			if _, ok := b.UserAgents[subscription.CustomUAID]; !ok {
				return fmt.Errorf("legacy subscription references unknown custom UA")
			}
		}
		b.Subscriptions[subscription.ID] = subscription
	}
	return nil
}

func (b *Builder) readProxyCandidatesJSON(path string) error {
	var candidates []struct {
		RawURI   string `json:"raw_uri"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		Disabled bool   `json:"disabled"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read legacy %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(data, &candidates); err != nil {
		var envelope struct {
			Candidates []struct {
				RawURI   string `json:"raw_uri"`
				Name     string `json:"name"`
				Type     string `json:"type"`
				Disabled bool   `json:"disabled"`
			} `json:"candidates"`
		}
		if envelopeErr := json.Unmarshal(data, &envelope); envelopeErr != nil {
			return fmt.Errorf("parse legacy %s: %w", filepath.Base(path), err)
		}
		candidates = envelope.Candidates
	}
	for _, candidate := range candidates {
		if err := b.addGlobalProxy(candidate.RawURI, candidate.Name, candidate.Type, candidate.Disabled, false, "legacy_file", ""); err != nil {
			return err
		}
	}
	return nil
}

func (b *Builder) readPinnedProxy(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read legacy config proxy: %w", err)
	}
	var document struct {
		ProxyURL string `json:"proxy_url"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("parse legacy config proxy: %w", err)
	}
	return b.addGlobalProxy(document.ProxyURL, "Migrated pinned proxy", "", false, true, "legacy_config", "proxy_url")
}
