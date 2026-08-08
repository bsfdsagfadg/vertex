package config

type staticConfig struct {
	c AppConfig
}

func StaticProvider(c AppConfig) ConfigProvider { return staticConfig{c} }

func (s staticConfig) PortAPI() int                             { return s.c.PortAPI }
func (s staticConfig) MaxRetries() int                          { return s.c.MaxRetries }
func (s staticConfig) AdminPassword() string                    { return s.c.AdminPassword }
func (s staticConfig) ProxyURL() string                         { return s.c.ProxyURL }
func (s staticConfig) DebugPprof() bool                         { return s.c.DebugPprof }
func (s staticConfig) DebugMode() bool                          { return s.c.DebugMode }
func (s staticConfig) DropMaxTokens() bool                      { return s.c.DropMaxTokens }
func (s staticConfig) AggregateStream() bool                    { return s.c.AggregateStream }
func (s staticConfig) FakeStreamEnabled() bool                  { return s.c.FakeStreamEnabled }
func (s staticConfig) MaxN() int                                { return s.c.MaxN }
func (s staticConfig) MaxRequestMB() int                        { return s.c.MaxRequestMB }
func (s staticConfig) MaxSpillMB() int                          { return s.c.MaxSpillMB }
func (s staticConfig) RequestTimeout() int                      { return s.c.RequestTimeout }
func (s staticConfig) RaceTimeout() int                         { return s.c.RaceTimeout }
func (s staticConfig) StreamIdleTimeoutSeconds() int            { return s.c.StreamIdleTimeoutSeconds }
func (s staticConfig) ModelTurnGuardEnabled() bool              { return s.c.ModelTurnGuardEnabled }
func (s staticConfig) VertexAPIKey() string                     { return s.c.VertexAPIKey }
func (s staticConfig) CountTokensQuerySignature() string        { return s.c.CountTokensQuerySignature }
func (s staticConfig) SafetySettings() map[string]string        { return s.c.SafetySettings }
func (s staticConfig) ParallelPoolEnabled() bool                { return s.c.ParallelPoolEnabled }
func (s staticConfig) StickyNodePriority() bool                 { return s.c.StickyNodePriority }
func (s staticConfig) ParallelPoolRetryEnabled() bool           { return s.c.ParallelPoolRetryEnabled }
func (s staticConfig) ParallelPoolSize() int                    { return s.c.ParallelPoolSize }
func (s staticConfig) ParallelPoolDelayDynamic() bool           { return s.c.ParallelPoolDelayDynamic }
func (s staticConfig) ParallelPoolDelayMs() int                 { return s.c.ParallelPoolDelayMs }
func (s staticConfig) EntryProxyProbeEnabled() bool             { return s.c.EntryProxyProbeEnabled }
func (s staticConfig) ActiveNodeURI() string                    { return s.c.ActiveNodeURI }
func (s staticConfig) ParallelNodeTopK() int                    { return s.c.ParallelNodeTopK }
func (s staticConfig) BackgroundImage() string                  { return s.c.BackgroundImage }
func (s staticConfig) FontSize() string                         { return s.c.FontSize }
func (s staticConfig) FontColorType() string                    { return s.c.FontColorType }
func (s staticConfig) FontColor() string                        { return s.c.FontColor }
func (s staticConfig) CustomBgPresets() []string                { return s.c.CustomBgPresets }
func (s staticConfig) AutoRefreshLogs() bool                    { return s.c.GetAutoRefreshLogs() }
func (s staticConfig) DefaultImageSize() string                 { return s.c.DefaultImageSize }
func (s staticConfig) DefaultResponseModalities() string        { return s.c.DefaultResponseModalities }
func (s staticConfig) TelemetryEnabled() *bool                  { return s.c.TelemetryEnabled }
func (s staticConfig) BaseModels() []string                     { return s.c.BaseModels() }
func (s staticConfig) ModelRegistry() []ModelEntry              { return s.c.ModelRegistry() }
func (s staticConfig) AliasMap() map[string]string              { return s.c.AliasMap() }
func (s staticConfig) ModelsWithFakeVariants() []string         { return s.c.ModelsWithFakeVariants() }
func (s staticConfig) FakePrefixes() []string                   { return s.c.FakePrefixes() }
func (s staticConfig) ResolveModelName(s_ string) string        { return s.c.ResolveModelName(s_) }
func (s staticConfig) LookupModel(s_ string) (ModelEntry, bool) { return s.c.LookupModel(s_) }
func (s staticConfig) ConfigDir() string                        { return s.c.ConfigDir() }
func (s staticConfig) ConfigPath() string                       { return s.c.ConfigPath() }
