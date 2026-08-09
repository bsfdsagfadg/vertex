package vertex

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"

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
	uuid := make([]byte, 16)
	_, _ = rand.Read(uuid)
	// Set version 4
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	// Set variant to RFC4122
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("%X-%X-%X-%X-%X",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// buildRequestPayload 构建发往上游的完整请求体（对齐 _build_request_payload）：
// 用 transform 构建 variables，再强制注入 region=global 与 recaptchaToken，最后包壳。
//
// 注意：本函数为旧 map 链路保留；新链路使用 buildTypedRequestPayload。
func buildRequestPayload(model string, geminiPayload map[string]any, recaptchaToken string, cfg config.ConfigProvider) map[string]any {
	b, err := json.Marshal(geminiPayload)
	if err != nil {
		vars := transform.BuildGeminiVariables(model, nil, cfg)
		return wrapPayload(vars, recaptchaToken)
	}
	var req transform.GeminiRequest
	_ = json.Unmarshal(b, &req)
	vars := transform.BuildGeminiVariables(model, &req, cfg)
	return wrapPayload(vars, recaptchaToken)
}

// buildTypedRequestPayload 由强类型 GeminiRequest 构建发往上游的完整请求体。
// 替代旧 buildRequestPayload（map 版）：变量构建全部收敛到 transform.BuildGeminiVariables。
func buildTypedRequestPayload(model string, req *transform.GeminiRequest, recaptchaToken string, cfg config.ConfigProvider) map[string]any {
	vars := transform.BuildGeminiVariables(model, req, cfg)
	return wrapPayload(vars, recaptchaToken)
}

// wrapPayload 把 variables 包裹为 batchGraphql envelope。
func wrapPayload(vars map[string]any, recaptchaToken string) map[string]any {
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
