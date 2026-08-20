package api

import (
	"context"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

func (h *handler) listGlobalProxies(ctx context.Context) ([]config.ProxyCandidate, error) {
	if h.routePlanner != nil {
		return h.routePlanner.ListGlobalProxies(ctx)
	}
	return config.ListProxyCandidates(), nil
}

func (h *handler) addGlobalProxy(ctx context.Context, rawURI string) (config.ProxyCandidate, error) {
	if h.routePlanner != nil {
		return h.routePlanner.AddGlobalProxy(ctx, rawURI, "manual", "", false, true)
	}
	return config.AddProxyCandidate(rawURI)
}

func (h *handler) upsertGlobalProxy(ctx context.Context, rawURI, sourceType, sourceID string, pinned bool) (config.ProxyCandidate, error) {
	if h.routePlanner != nil {
		return h.routePlanner.AddGlobalProxy(ctx, rawURI, sourceType, sourceID, pinned, false)
	}
	return config.UpsertProxyCandidateSource(rawURI, sourceType, sourceID, pinned)
}

func (h *handler) hasGlobalProxy(ctx context.Context, rawURI string) (bool, error) {
	if h.routePlanner != nil {
		return h.routePlanner.HasGlobalProxy(ctx, rawURI)
	}
	return config.HasProxyCandidate(rawURI), nil
}

func (h *handler) setGlobalProxyEnabled(ctx context.Context, rawURI string, enabled bool) error {
	if h.routePlanner != nil {
		return h.routePlanner.SetGlobalProxyEnabled(ctx, rawURI, enabled)
	}
	return config.SetProxyCandidateEnabled(rawURI, enabled)
}

func (h *handler) setGlobalProxyPinned(ctx context.Context, rawURI string, pinned bool) error {
	if h.routePlanner != nil {
		return h.routePlanner.SetGlobalProxyPinned(ctx, rawURI, pinned)
	}
	return config.SetProxyCandidatePinned(rawURI, pinned)
}

func (h *handler) removeGlobalProxy(ctx context.Context, rawURI string) (bool, error) {
	if h.routePlanner != nil {
		return h.routePlanner.RemoveGlobalProxy(ctx, rawURI)
	}
	return config.RemoveProxyCandidate(rawURI)
}

func (h *handler) removeDisabledGlobalProxies(ctx context.Context) ([]string, error) {
	if h.routePlanner != nil {
		return h.routePlanner.RemoveDisabledGlobalProxies(ctx)
	}
	return config.RemoveDisabledProxyCandidates()
}

func (h *handler) updateGlobalProxyTest(ctx context.Context, rawURI string, ok bool, elapsed float64, message string) error {
	if h.routePlanner != nil {
		_, err := h.routePlanner.UpdateGlobalProxyResult(ctx, rawURI, ok, elapsed, message, h.cfg.EntryProxyProbeCooldownSeconds(), false, false, 0)
		return err
	}
	return config.UpdateProxyCandidateTest(rawURI, ok, elapsed, message)
}

func (h *handler) updateGlobalProxyProbe(ctx context.Context, rawURI string, ok bool, elapsed float64, message string, cfg config.ConfigProvider) (bool, error) {
	if h.routePlanner != nil {
		return h.routePlanner.UpdateGlobalProxyResult(ctx, rawURI, ok, elapsed, message,
			cfg.EntryProxyProbeCooldownSeconds(), true, cfg.EntryProxyProbeAutoDisableEnabled(), cfg.EntryProxyProbeAutoDisableFailures())
	}
	return config.UpdateProxyCandidateProbeResult(rawURI, ok, elapsed, message,
		cfg.EntryProxyProbeCooldownSeconds(), cfg.EntryProxyProbeAutoDisableEnabled(), cfg.EntryProxyProbeAutoDisableFailures())
}

func (h *handler) selectGlobalProxy(ctx context.Context, cfg config.ConfigProvider) (string, error) {
	if h.routePlanner != nil {
		return h.routePlanner.SelectGlobalProxy(ctx, cfg)
	}
	return config.SelectEntryProxy(cfg), nil
}

func (h *handler) markGlobalProxySuccess(rawURI string) error {
	if h.routePlanner != nil {
		return h.routePlanner.MarkGlobalProxySuccess(rawURI)
	}
	return config.MarkEntryProxySuccess(rawURI)
}
