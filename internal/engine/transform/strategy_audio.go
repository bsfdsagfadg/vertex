package transform

import (
	"fmt"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/infra/config"
)

// AudioStrategy TTS 语音家族策略。
type AudioStrategy struct {
	model string
}

// Family 返回音频家族。
func (s *AudioStrategy) Family() ModelFamily { return FamilyAudio }

// FamilyStreamMode 音频 TTS 家族禁用流式通道。
func (s *AudioStrategy) FamilyStreamMode() StreamMode { return StreamModeForbidden }

// Enhance 兜底注入 AUDIO 响应模态，强行清空/屏蔽 tools 与 thinkingConfig。
func (s *AudioStrategy) Enhance(req *GeminiRequest, cfg config.ConfigProvider) {
	gc := req.GenerationConfig
	if gc == nil {
		gc = &GenerationConfig{}
		req.GenerationConfig = gc
	}
	if len(gc.ResponseModalities) == 0 {
		gc.ResponseModalities = []string{"AUDIO"}
	}
	// 强行清空/拒绝不支持的 tools 与 thinkingConfig，防止 TTS 上游 400
	req.Tools = nil
	gc.ThinkingConfig = nil
}

// Validate 禁止语音载荷携带 tools 与 thinkingConfig（TTS 不接受函数声明与思考配置）。
func (s *AudioStrategy) Validate(req *GeminiRequest) error {
	if len(req.Tools) > 0 {
		return fmt.Errorf("音频/语音模型不支持工具调用 (tools not supported for TTS)")
	}
	if req.GenerationConfig != nil && req.GenerationConfig.ThinkingConfig != nil {
		return fmt.Errorf("音频/语音模型不支持 thinkingConfig (thinkingConfig not supported for TTS)")
	}
	return nil
}

// Prepare 语音家族防御性再次确保 Tools 与 ThinkingConfig 为 nil，且 ResponseModalities 维持 ["AUDIO"]。
func (s *AudioStrategy) Prepare(req *GeminiRequest) {
	req.Tools = nil
	if req.GenerationConfig != nil {
		req.GenerationConfig.ThinkingConfig = nil
		if len(req.GenerationConfig.ResponseModalities) == 0 {
			req.GenerationConfig.ResponseModalities = []string{"AUDIO"}
		}
	}
}

// BuildVariables 实现语音家族独占的上行 variables 构建：
// 纯净转换 TTS 载荷，硬性过滤 Tools、ToolConfig 与 ThinkingConfig。
func (s *AudioStrategy) BuildVariables(model string, req *GeminiRequest, cfg config.ConfigProvider) *GeminiVariables {
	if req == nil {
		req = &GeminiRequest{}
	}

	contents := sanitizeContentRolesTyped(req.Contents)
	contents = filterEmptyContentsTyped(contents)

	gc := prepareNativeGenerationConfig(req.GenerationConfig)
	if gc != nil {
		gc.ThinkingConfig = nil // 语音模型强行置空 thinkingConfig
		if len(gc.ResponseModalities) == 0 {
			gc.ResponseModalities = []string{"AUDIO"}
		}
	}

	out := &GeminiRequest{
		Contents:          contents,
		SystemInstruction: req.SystemInstruction,
		Tools:             nil, // 语音硬性过滤 Tools
		ToolConfig:        nil, // 语音硬性过滤 ToolConfig
		SafetySettings:    BuildSafetySettingsTyped(cfg),
		GenerationConfig:  gc,
		CachedContent:     req.CachedContent,
		ServiceTier:       req.ServiceTier,
		Store:             req.Store,
	}

	return &GeminiVariables{
		Model:         config.ResolveModelName(model),
		GeminiRequest: out,
	}
}

// CalculateIdleTimeouts 语音合成 TTS 超时：
// preTimeout = max(base * 3, 40s)，postTimeout = max(base * 2, 20s)。
func (s *AudioStrategy) CalculateIdleTimeouts(baseSeconds int) (preTimeout, postTimeout time.Duration) {
	postTimeout = max(time.Duration(baseSeconds*2)*time.Second, 20*time.Second)
	preTimeout = max(time.Duration(baseSeconds*3)*time.Second, 40*time.Second)
	return preTimeout, postTimeout
}

// IsValidChunk 判定语音 Chunk 是否包含有效音频 InlineData 或真实安全拦截（完全忽略纯文本 Chunk）。
func (s *AudioStrategy) IsValidChunk(chunk *GeminiChunk) bool {
	if chunk == nil {
		return false
	}
	if chunk.PromptFeedback != nil && IsBlockReason(chunk.PromptFeedback.BlockReason) {
		return true
	}
	if len(chunk.Candidates) > 0 {
		for _, cand := range chunk.Candidates {
			if cand == nil {
				continue
			}
			if IsSafetyFinishReason(cand.FinishReason) {
				return true
			}
			if cand.Content != nil {
				for _, p := range cand.Content.Parts {
					if p.InlineData != nil && p.InlineData.Data != "" {
						mime := strings.ToLower(p.InlineData.MimeType)
						if mime == "" || mime == "audio" || strings.HasPrefix(mime, "audio/") {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// IsValidResponse 判定非流式语音响应是否包含有效音频 Payload 或真实安全拦截。
func (s *AudioStrategy) IsValidResponse(resp *GeminiResponse) bool {
	if resp == nil {
		return false
	}
	if resp.PromptFeedback != nil && IsBlockReason(resp.PromptFeedback.BlockReason) {
		return true
	}
	if len(resp.Candidates) > 0 {
		for _, cand := range resp.Candidates {
			if cand == nil {
				continue
			}
			if IsSafetyFinishReason(cand.FinishReason) {
				return true
			}
		}
	}
	audio := ExtractAudioTyped(resp)
	return len(audio.Bytes) > 0
}
