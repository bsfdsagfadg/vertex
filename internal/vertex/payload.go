package vertex

import (
	"crypto/rand"
	"math/big"
	"strings"

	"github.com/google/uuid"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/transform"
)

// batchGraphql 请求的固定包壳常量（逐字节对齐 PoC body.json / vertex_client）。
const (
	querySignature = "2/l8eCsMMY49imcDQ/lwwXyL8cYtTjxZBF2dNqy69LodY="
	operationName  = "StreamGenerateContentAnonymous"
)

func randomPageViewID() int64 {
	minVal := int64(1000000000000000)
	maxVal := int64(9000000000000000)
	n, err := rand.Int(rand.Reader, big.NewInt(maxVal))
	if err != nil {
		return minVal
	}
	return minVal + n.Int64()
}

func randomTrackingID() string {
	digits := make([]byte, 16)
	for i := range digits {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err == nil {
			digits[i] = '0' + byte(n.Int64())
		} else {
			digits[i] = '0'
		}
	}
	return "d" + string(digits)
}

func randomUUID() string {
	return strings.ToUpper(uuid.NewString())
}

// buildRequestPayload 构建发往上游的完整请求体（对齐 _build_request_payload）：
// 用 transform 构建 variables，再强制注入 region=global 与 recaptchaToken，最后包壳。
func buildRequestPayload(model string, geminiPayload map[string]any, recaptchaToken string, cfg config.ConfigProvider) map[string]any {
	vars := transform.BuildVertexVariables(model, geminiPayload, cfg)
	vars["region"] = "global"
	vars["recaptchaToken"] = recaptchaToken
	trackingID := randomTrackingID()
	return map[string]any{
		"requestContext": map[string]any{
			"clientVersion":    "boq_cloud-boq-clientweb-vertexaistudio_20260630.00_p0",
			"pagePath":         "/agent-platform/studio/multimodal",
			"pageViewId":       randomPageViewID(),
			"trackingId":       trackingID,
			"backendOverrides": map[string]any{},
			"clientSessionId":  randomUUID(),
			"selectedPurview":  map[string]any{},
			"jurisdiction":     "global",
			"localizationData": map[string]any{
				"locale":   "zh_CN",
				"timezone": "Asia/Hong_Kong",
			},
		},
		"querySignature": querySignature,
		"operationName":  operationName,
		"variables":      vars,
	}
}
