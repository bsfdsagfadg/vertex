package api

import (
	"fmt"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/transform"
)

// AudioStrategy TTS 语音家族策略。
type AudioStrategy struct {
	model string
}

// Family 返回音频家族。
func (s *AudioStrategy) Family() transform.ModelFamily { return transform.FamilyAudio }

// Enhance 兜底注入 AUDIO 响应模态，强行清空/屏蔽 tools 与 thinkingConfig。
func (s *AudioStrategy) Enhance(req *transform.GeminiRequest, cfg config.ConfigProvider) {
	gc := req.GenerationConfig
	if gc == nil {
		gc = &transform.GenerationConfig{}
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
func (s *AudioStrategy) Validate(req *transform.GeminiRequest) error {
	if len(req.Tools) > 0 {
		return fmt.Errorf("音频/语音模型不支持工具调用 (tools not supported for TTS)")
	}
	if req.GenerationConfig != nil && req.GenerationConfig.ThinkingConfig != nil {
		return fmt.Errorf("音频/语音模型不支持 thinkingConfig (thinkingConfig not supported for TTS)")
	}
	return nil
}

// Prepare 语音家族防御性再次确保 Tools 与 ThinkingConfig 为 nil，且 ResponseModalities 维持 ["AUDIO"]。
func (s *AudioStrategy) Prepare(req *transform.GeminiRequest) {
	req.Tools = nil
	if req.GenerationConfig != nil {
		req.GenerationConfig.ThinkingConfig = nil
		if len(req.GenerationConfig.ResponseModalities) == 0 {
			req.GenerationConfig.ResponseModalities = []string{"AUDIO"}
		}
	}
}