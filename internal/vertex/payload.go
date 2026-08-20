package vertex

import (
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/upstream/anonymousgraph"
)

const (
	querySignature = anonymousgraph.QuerySignature
	operationName  = anonymousgraph.OperationName
)

// buildRequestPayload 构建发往上游的完整请求体（对齐 _build_request_payload）：
// 用 transform 构建 variables，再强制注入 region=global 与 recaptchaToken，最后包壳。
func buildRequestPayload(model string, geminiPayload map[string]any, recaptchaToken string, cfg config.ConfigProvider) map[string]any {
	vars := transform.BuildVertexVariables(model, geminiPayload, cfg)
	return anonymousgraph.BuildEnvelope(vars, recaptchaToken)
}
