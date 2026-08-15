package transform

import (
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// StreamMode 标识模型家族支持的流式传输模式。
type StreamMode string

const (
	StreamModeTrue      StreamMode = "true_stream"      // 原生真流式 (Text 家族)
	StreamModeAggregate StreamMode = "aggregate_stream" // 聚合单包 SSE (Image 家族降级保护)
	StreamModeForbidden StreamMode = "forbidden"        // 禁用流式 / Unary 专用 (Audio 家族)
)

// ResolvedModel 存储 ModelResolver 解析后的模型元数据与家族策略绑定。
type ResolvedModel struct {
	RawModel    string        // 原始请求模型名 (例: "models/假非流-gemini-image-alias")
	ActualModel string        // 剥离归一后的 Gemini 真正模型名 (例: "gemini-3.1-flash-image")
	IsFake      bool          // 是否带有假非流修饰
	Family      ModelFamily   // FamilyText / FamilyImage / FamilyAudio
	Strategy    ModelStrategy // 绑定的家族策略
}

// trimGeminiPathPrefix 清理 GCP 规范前缀 (models/ 与 publishers/*/models/)，不区分大小写。
func trimGeminiPathPrefix(model string) string {
	m := strings.TrimSpace(model)
	lower := strings.ToLower(m)
	if strings.HasPrefix(lower, "models/") {
		return m[len("models/"):]
	}
	if idx := strings.Index(lower, "/models/"); idx != -1 {
		return m[idx+len("/models/"):]
	}
	return m
}

// stripFakePrefix 动态剥离 cfg.FakePrefixes() 前缀。
func stripFakePrefix(model string, fakePrefixes []string) (string, bool) {
	for _, p := range fakePrefixes {
		if strings.HasPrefix(model, p) {
			return model[len(p):], true
		}
	}
	return model, false
}

// ResolveModel 统一解析模型名称，剥离 GCP 前缀与假非流前缀，查询别名，并绑定家族与 Strategy。
func ResolveModel(rawModel string, cfg config.ConfigProvider) *ResolvedModel {
	m := trimGeminiPathPrefix(rawModel)

	var isFake bool
	if cfg != nil {
		m, isFake = stripFakePrefix(m, cfg.FakePrefixes())
		m = cfg.ResolveModelName(m)
	} else {
		m, isFake = stripFakePrefix(m, config.FakePrefixes())
		m = config.ResolveModelName(m)
	}

	family := FamilyFor(m)
	if family != FamilyText {
		isFake = false
	}
	router := NewModelFamilyRouter()
	strategy := router.For(m)

	return &ResolvedModel{
		RawModel:    rawModel,
		ActualModel: m,
		IsFake:      isFake,
		Family:      family,
		Strategy:    strategy,
	}
}
