package config

type ConfigProvider interface {
	PortAPI() int
	MaxRetries() int
	AdminPassword() string
	ProxyURL() string
	DebugPprof() bool
	DebugMode() bool

	DropMaxTokens() bool

	AggregateStream() bool
	FakeStreamEnabled() bool
	MaxN() int
	MaxRequestMB() int
	MaxSpillMB() int
	RequestTimeout() int
	RaceTimeout() int
	StreamIdleTimeoutSeconds() int
	ModelTurnGuardEnabled() bool

	VertexAPIKey() string
	CountTokensQuerySignature() string

	SafetySettings() map[string]string

	ParallelPoolEnabled() bool
	StickyNodePriority() bool
	ParallelPoolRetryEnabled() bool
	ParallelPoolSize() int
	ParallelPoolDelayDynamic() bool
	ParallelPoolDelayMs() int
	ActiveNodeURI() string
	ParallelNodeTopK() int

	BackgroundImage() string
	FontSize() string
	FontColorType() string
	FontColor() string
	CustomBgPresets() []string
	AutoRefreshLogs() bool

	TelemetryEnabled() *bool

	BaseModels() []string
	ModelRegistry() []ModelEntry
	AliasMap() map[string]string
	ModelsWithFakeVariants() []string
	FakePrefixes() []string
	ResolveModelName(string) string
	LookupModel(string) (ModelEntry, bool)

	ConfigDir() string
	ConfigPath() string
}

type ConfigWriter interface {
	WriteSettings(map[string]any) error
	WriteModels([]string, map[string]string) error
}

type dynamicConfig struct{}

func GetProvider() ConfigProvider { return dynamicConfig{} }

func (d dynamicConfig) PortAPI() int                      { return Load().PortAPI }
func (d dynamicConfig) MaxRetries() int                   { return Load().MaxRetries }
func (d dynamicConfig) AdminPassword() string             { return Load().AdminPassword }
func (d dynamicConfig) ProxyURL() string                  { return Load().ProxyURL }
func (d dynamicConfig) DebugPprof() bool                  { return Load().DebugPprof }
func (d dynamicConfig) DebugMode() bool                   { return Load().DebugMode }
func (d dynamicConfig) DropMaxTokens() bool               { return Load().DropMaxTokens }
func (d dynamicConfig) AggregateStream() bool             { return Load().AggregateStream }
func (d dynamicConfig) FakeStreamEnabled() bool           { return Load().FakeStreamEnabled }
func (d dynamicConfig) MaxN() int                         { return Load().MaxN }
func (d dynamicConfig) MaxRequestMB() int                 { return Load().MaxRequestMB }
func (d dynamicConfig) MaxSpillMB() int                   { return Load().MaxSpillMB }
func (d dynamicConfig) RequestTimeout() int               { return Load().RequestTimeout }
func (d dynamicConfig) RaceTimeout() int                  { return Load().RaceTimeout }
func (d dynamicConfig) StreamIdleTimeoutSeconds() int     { return Load().StreamIdleTimeoutSeconds }
func (d dynamicConfig) ModelTurnGuardEnabled() bool       { return Load().ModelTurnGuardEnabled }
func (d dynamicConfig) VertexAPIKey() string              { return Load().VertexAPIKey }
func (d dynamicConfig) CountTokensQuerySignature() string { return Load().CountTokensQuerySignature }
func (d dynamicConfig) SafetySettings() map[string]string {
	c := Load()
	out := make(map[string]string, len(c.SafetySettings))
	for k, v := range c.SafetySettings {
		out[k] = v
	}
	return out
}
func (d dynamicConfig) ParallelPoolEnabled() bool      { return Load().ParallelPoolEnabled }
func (d dynamicConfig) StickyNodePriority() bool       { return Load().StickyNodePriority }
func (d dynamicConfig) ParallelPoolRetryEnabled() bool { return Load().ParallelPoolRetryEnabled }
func (d dynamicConfig) ParallelPoolSize() int          { return Load().ParallelPoolSize }
func (d dynamicConfig) ParallelPoolDelayDynamic() bool { return Load().ParallelPoolDelayDynamic }
func (d dynamicConfig) ParallelPoolDelayMs() int       { return Load().ParallelPoolDelayMs }
func (d dynamicConfig) ActiveNodeURI() string          { return Load().ActiveNodeURI }
func (d dynamicConfig) ParallelNodeTopK() int          { return Load().ParallelNodeTopK }
func (d dynamicConfig) BackgroundImage() string        { return Load().BackgroundImage }
func (d dynamicConfig) FontSize() string               { return Load().FontSize }
func (d dynamicConfig) FontColorType() string          { return Load().FontColorType }
func (d dynamicConfig) FontColor() string              { return Load().FontColor }
func (d dynamicConfig) CustomBgPresets() []string {
	c := Load()
	out := make([]string, len(c.CustomBgPresets))
	copy(out, c.CustomBgPresets)
	return out
}
func (d dynamicConfig) TelemetryEnabled() *bool {
	c := Load()
	if c.TelemetryEnabled == nil {
		return nil
	}
	v := *c.TelemetryEnabled
	return &v
}
func (d dynamicConfig) BaseModels() []string                    { return Load().BaseModels() }
func (d dynamicConfig) ModelRegistry() []ModelEntry             { return Load().ModelRegistry() }
func (d dynamicConfig) AliasMap() map[string]string             { return Load().AliasMap() }
func (d dynamicConfig) ModelsWithFakeVariants() []string        { return Load().ModelsWithFakeVariants() }
func (d dynamicConfig) FakePrefixes() []string                  { return Load().FakePrefixes() }
func (d dynamicConfig) ResolveModelName(s string) string        { return Load().ResolveModelName(s) }
func (d dynamicConfig) LookupModel(s string) (ModelEntry, bool) { return Load().LookupModel(s) }
func (d dynamicConfig) AutoRefreshLogs() bool                   { return Load().GetAutoRefreshLogs() }
func (d dynamicConfig) ConfigDir() string                       { return Load().ConfigDir() }
func (d dynamicConfig) ConfigPath() string                      { return Load().ConfigPath() }
