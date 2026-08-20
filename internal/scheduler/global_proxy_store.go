package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/repository"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

func (p *RoutePlanner) ListGlobalProxies(ctx context.Context) ([]config.ProxyCandidate, error) {
	records, err := p.repository.ListGlobalProxies(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]config.ProxyCandidate, 0, len(records))
	for _, record := range records {
		result = append(result, proxyCandidate(record))
	}
	return result, nil
}

func (p *RoutePlanner) HasGlobalProxy(ctx context.Context, rawURI string) (bool, error) {
	identity, err := transport.ProxyIdentity(strings.TrimSpace(rawURI))
	if err != nil {
		return false, err
	}
	records, err := p.repository.ListGlobalProxies(ctx)
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if record.CanonicalIdentity == identity.SemanticFingerprint {
			return true, nil
		}
	}
	return false, nil
}

func (p *RoutePlanner) AddGlobalProxy(ctx context.Context, rawURI, sourceType, sourceID string, pinned, rejectExisting bool) (config.ProxyCandidate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return config.ProxyCandidate{}, fmt.Errorf("URI 为空")
	}
	if err := transport.ValidateProxyURI(rawURI); err != nil {
		return config.ProxyCandidate{}, err
	}
	identity, err := transport.ProxyIdentity(rawURI)
	if err != nil {
		return config.ProxyCandidate{}, err
	}
	records, err := p.repository.ListGlobalProxies(ctx)
	if err != nil {
		return config.ProxyCandidate{}, err
	}
	for _, record := range records {
		if record.CanonicalIdentity != identity.SemanticFingerprint {
			continue
		}
		if rejectExisting {
			return config.ProxyCandidate{}, fmt.Errorf("该 URI 已在候选列表中")
		}
		record.Pinned = record.Pinned || pinned
		if err := p.repository.UpsertGlobalProxy(ctx, record.GlobalProxy,
			repository.GlobalProxySource{GlobalProxyID: record.ID, SourceType: sourceType, SourceID: sourceID},
			record.GlobalProxyHealth,
		); err != nil {
			return config.ProxyCandidate{}, err
		}
		newSource := repository.GlobalProxySource{GlobalProxyID: record.ID, SourceType: sourceType, SourceID: sourceID}
		hasSource := sourceType == ""
		for _, source := range record.Sources {
			if source.SourceType == newSource.SourceType && source.SourceID == newSource.SourceID {
				hasSource = true
				break
			}
		}
		if !hasSource {
			record.Sources = append(record.Sources, newSource)
		}
		return proxyCandidate(record), nil
	}
	parsed, _ := url.Parse(rawURI)
	sum := sha256.Sum256([]byte("gp\x00" + identity.SemanticFingerprint))
	name := strings.TrimSpace(parsed.Fragment)
	if decoded, decodeErr := url.QueryUnescape(name); decodeErr == nil {
		name = decoded
	}
	if name == "" {
		name = strings.ToLower(parsed.Scheme) + "://" + parsed.Host
	}
	record := repository.GlobalProxyRecord{
		GlobalProxy: repository.GlobalProxy{
			ID: "gp_" + hex.EncodeToString(sum[:12]), RawURI: rawURI,
			CanonicalIdentity: identity.SemanticFingerprint, EndpointFingerprint: identity.EndpointFingerprint,
			Name: name, Type: strings.ToLower(parsed.Scheme), Pinned: pinned,
		},
	}
	if err := p.repository.UpsertGlobalProxy(ctx, record.GlobalProxy,
		repository.GlobalProxySource{GlobalProxyID: record.ID, SourceType: sourceType, SourceID: sourceID},
		repository.GlobalProxyHealth{GlobalProxyID: record.ID},
	); err != nil {
		return config.ProxyCandidate{}, err
	}
	return proxyCandidate(record), nil
}

func (p *RoutePlanner) SetGlobalProxyEnabled(ctx context.Context, rawURI string, enabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	record, err := p.findGlobalProxy(ctx, rawURI)
	if err != nil {
		return err
	}
	if !enabled {
		return p.repository.SetGlobalProxyDisabled(ctx, record.CanonicalIdentity, true)
	}
	// A manual enable is an explicit retry request: keep the last diagnostic
	// result for the UI, but clear both automatic failure admission gates.
	return p.repository.UpdateGlobalProxyHealth(ctx, repository.GlobalProxyHealth{
		GlobalProxyID: record.ID, LastTestOK: record.LastTestOK, LastTestMS: record.LastTestMS,
		LastTestAt: record.LastTestAt, LastTestError: record.LastTestError,
		CooldownUntil: 0, ConsecutiveFailures: 0,
	}, false)
}

func (p *RoutePlanner) SetGlobalProxyPinned(ctx context.Context, rawURI string, pinned bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	record, err := p.findGlobalProxy(ctx, rawURI)
	if err != nil {
		return err
	}
	return p.repository.SetGlobalProxyPinned(ctx, record.CanonicalIdentity, pinned)
}

func (p *RoutePlanner) RemoveGlobalProxy(ctx context.Context, rawURI string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	record, err := p.findGlobalProxy(ctx, rawURI)
	if err != nil {
		return false, err
	}
	deleted, err := p.repository.DeleteGlobalProxy(ctx, record.CanonicalIdentity)
	return deleted.Pinned, err
}

func (p *RoutePlanner) RemoveDisabledGlobalProxies(ctx context.Context) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.repository.DeleteDisabledGlobalProxies(ctx)
}

func (p *RoutePlanner) UpdateGlobalProxyResult(ctx context.Context, rawURI string, ok bool, elapsedMS float64, message string, cooldownSeconds int, countFailure, autoDisable bool, failureLimit int) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	record, err := p.findGlobalProxy(ctx, rawURI)
	if err != nil {
		return false, err
	}
	if cooldownSeconds < 0 {
		cooldownSeconds = 0
	}
	now := time.Now()
	health := repository.GlobalProxyHealth{
		GlobalProxyID: record.ID, LastTestOK: ok, LastTestMS: elapsedMS, LastTestAt: now.Unix(),
		LastTestError: message, ConsecutiveFailures: record.ConsecutiveFailures,
	}
	disabled := record.Disabled
	autoDisabled := false
	if ok {
		health.ConsecutiveFailures = 0
	} else {
		health.CooldownUntil = now.Add(time.Duration(cooldownSeconds) * time.Second).Unix()
		if countFailure {
			health.ConsecutiveFailures++
			if autoDisable && failureLimit > 0 && health.ConsecutiveFailures >= failureLimit && !disabled {
				disabled = true
				autoDisabled = true
				health.CooldownUntil = 0
			}
		}
	}
	return autoDisabled, p.repository.UpdateGlobalProxyHealth(ctx, health, disabled)
}

func (p *RoutePlanner) SelectGlobalProxy(ctx context.Context, cfg config.ConfigProvider) (string, error) {
	values, err := p.globalProxySequence(ctx, 1, cfg)
	if err != nil || len(values) == 0 {
		return "", err
	}
	return values[0], nil
}

func (p *RoutePlanner) findGlobalProxy(ctx context.Context, rawURI string) (repository.GlobalProxyRecord, error) {
	identity, err := transport.ProxyIdentity(strings.TrimSpace(rawURI))
	if err != nil {
		return repository.GlobalProxyRecord{}, err
	}
	records, err := p.repository.ListGlobalProxies(ctx)
	if err != nil {
		return repository.GlobalProxyRecord{}, err
	}
	for _, record := range records {
		if record.CanonicalIdentity == identity.SemanticFingerprint {
			return record, nil
		}
	}
	return repository.GlobalProxyRecord{}, fmt.Errorf("未找到该候选 URI")
}

func proxyCandidate(record repository.GlobalProxyRecord) config.ProxyCandidate {
	return config.ProxyCandidate{
		ID: record.ID, RawURI: record.RawURI, CanonicalIdentity: record.CanonicalIdentity,
		EndpointFingerprint: record.EndpointFingerprint, Name: record.Name, Type: record.Type,
		Disabled: record.Disabled, Pinned: record.Pinned, Sources: record.Sources,
		CooldownUntil: record.CooldownUntil, LastTestOK: record.LastTestOK, LastTestMs: record.LastTestMS,
		LastTestAt: record.LastTestAt, LastTestError: record.LastTestError,
		ConsecutiveFailures: record.ConsecutiveFailures,
	}
}
