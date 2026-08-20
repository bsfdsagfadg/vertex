package config

type staticConfig struct {
	c        AppConfig
	models   []ModelEntry
	aliases  map[string]string
	prefixes []string
}

// StaticProvider captures config.json and models.json into one immutable
// request-safe view. Collection getters always return copies.
func StaticProvider(c AppConfig) ConfigProvider {
	return staticConfig{
		c:        cloneConfig(&c),
		models:   ModelRegistry(),
		aliases:  AliasMap(),
		prefixes: FakePrefixes(),
	}
}

func (s staticConfig) Snapshot() ConfigProvider { return s }

func (s staticConfig) PortAPI() int                 { return s.c.PortAPI }
func (s staticConfig) MaxRetries() int              { return s.c.MaxRetries }
func (s staticConfig) AdminPassword() string        { return s.c.AdminPassword }
func (s staticConfig) ProxyURL() string             { return s.c.ProxyURL }
func (s staticConfig) GlobalProxyEnabled() bool     { return s.c.GlobalProxyEnabled }
func (s staticConfig) GlobalProxyRequired() bool    { return s.c.GlobalProxyRequired }
func (s staticConfig) GlobalProxySelection() string { return s.c.GlobalProxySelection }
func (s staticConfig) AllowDirectWithoutGlobalProxy() bool {
	return s.c.AllowDirectWithoutGlobal
}
func (s staticConfig) DebugPprof() bool                  { return s.c.DebugPprof }
func (s staticConfig) DebugMode() bool                   { return s.c.DebugMode }
func (s staticConfig) DropMaxTokens() bool               { return s.c.DropMaxTokens }
func (s staticConfig) AggregateStream() bool             { return s.c.AggregateStream }
func (s staticConfig) FakeStreamEnabled() bool           { return s.c.FakeStreamEnabled }
func (s staticConfig) MaxN() int                         { return s.c.MaxN }
func (s staticConfig) MaxRequestMB() int                 { return s.c.MaxRequestMB }
func (s staticConfig) MaxSpillMB() int                   { return s.c.MaxSpillMB }
func (s staticConfig) RequestTimeout() int               { return s.c.RequestTimeout }
func (s staticConfig) RaceTimeout() int                  { return s.c.RaceTimeout }
func (s staticConfig) StreamIdleTimeoutSeconds() int     { return s.c.StreamIdleTimeoutSeconds }
func (s staticConfig) ModelTurnGuardEnabled() bool       { return s.c.ModelTurnGuardEnabled }
func (s staticConfig) OpenAIParameterPolicy() string     { return s.c.OpenAIParameterPolicy }
func (s staticConfig) GeminiParameterPolicy() string     { return s.c.GeminiParameterPolicy }
func (s staticConfig) ToolSchemaPolicy() string          { return s.c.ToolSchemaPolicy }
func (s staticConfig) VertexAPIKey() string              { return s.c.VertexAPIKey }
func (s staticConfig) CountTokensQuerySignature() string { return s.c.CountTokensQuerySignature }
func (s staticConfig) SafetySettings() map[string]string {
	out := make(map[string]string, len(s.c.SafetySettings))
	for k, v := range s.c.SafetySettings {
		out[k] = v
	}
	return out
}
func (s staticConfig) ParallelPoolEnabled() bool      { return s.c.ParallelPoolEnabled }
func (s staticConfig) StickyNodePriority() bool       { return s.c.StickyNodePriority }
func (s staticConfig) ParallelPoolRetryEnabled() bool { return s.c.ParallelPoolRetryEnabled }
func (s staticConfig) ParallelPoolSize() int          { return s.c.ParallelPoolSize }
func (s staticConfig) ParallelPoolDelayDynamic() bool { return s.c.ParallelPoolDelayDynamic }
func (s staticConfig) ParallelPoolDelayMs() int       { return s.c.ParallelPoolDelayMs }
func (s staticConfig) EntryProxyProbeEnabled() bool   { return s.c.EntryProxyProbeEnabled }
func (s staticConfig) EntryProxyProbeIntervalSeconds() int {
	return s.c.EntryProxyProbeIntervalSeconds
}
func (s staticConfig) EntryProxyProbeCooldownSeconds() int {
	return s.c.EntryProxyProbeCooldownSeconds
}
func (s staticConfig) EntryProxyProbeAutoDisableEnabled() bool {
	return s.c.EntryProxyProbeAutoDisableEnabled
}
func (s staticConfig) EntryProxyProbeAutoDisableFailures() int {
	return s.c.EntryProxyProbeAutoDisableFailures
}
func (s staticConfig) ActiveNodeURI() string   { return s.c.ActiveNodeURI }
func (s staticConfig) ParallelNodeTopK() int   { return s.c.ParallelNodeTopK }
func (s staticConfig) BackgroundImage() string { return s.c.BackgroundImage }
func (s staticConfig) FontSize() string        { return s.c.FontSize }
func (s staticConfig) FontColorType() string   { return s.c.FontColorType }
func (s staticConfig) FontColor() string       { return s.c.FontColor }
func (s staticConfig) CustomBgPresets() []string {
	return append([]string(nil), s.c.CustomBgPresets...)
}
func (s staticConfig) AutoRefreshLogs() bool             { return s.c.GetAutoRefreshLogs() }
func (s staticConfig) DefaultImageSize() string          { return s.c.DefaultImageSize }
func (s staticConfig) DefaultResponseModalities() string { return s.c.DefaultResponseModalities }
func (s staticConfig) BaseModels() []string {
	out := make([]string, 0, len(s.models))
	for _, entry := range s.models {
		if entry.Enabled {
			out = append(out, entry.ID)
		}
	}
	return out
}
func (s staticConfig) ModelRegistry() []ModelEntry { return cloneModelEntries(s.models) }
func (s staticConfig) AliasMap() map[string]string {
	out := make(map[string]string, len(s.aliases))
	for k, v := range s.aliases {
		out[k] = v
	}
	return out
}
func (s staticConfig) ModelsWithFakeVariants() []string {
	out := make([]string, 0, len(s.models)*3)
	for _, entry := range s.models {
		if !entry.Enabled {
			continue
		}
		out = append(out, entry.ID)
		if s.c.FakeStreamEnabled && entry.FakeStreamEnabled {
			for _, prefix := range s.prefixes {
				out = append(out, prefix+entry.ID)
			}
		}
	}
	return out
}
func (s staticConfig) FakePrefixes() []string { return append([]string(nil), s.prefixes...) }
func (s staticConfig) ResolveModelName(model string) string {
	if resolved, ok := s.aliases[model]; ok {
		return resolved
	}
	return model
}
func (s staticConfig) LookupModel(model string) (ModelEntry, bool) {
	for _, entry := range s.models {
		if entry.ID == model {
			return entry, true
		}
	}
	return ModelEntry{}, false
}
func (s staticConfig) ConfigDir() string  { return s.c.ConfigDir() }
func (s staticConfig) ConfigPath() string { return s.c.ConfigPath() }
