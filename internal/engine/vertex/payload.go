package vertex

import (
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/bsfdsagfadg/vertex/internal/engine/transform"
	"github.com/bsfdsagfadg/vertex/internal/infra/config"
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

// BatchGraphQLPayload 上游 GraphQL 完整 Envelope 包壳。
type BatchGraphQLPayload struct {
	RequestContext *RequestContext `json:"requestContext"`
	QuerySignature string          `json:"querySignature"`
	OperationName  string          `json:"operationName"`
	Variables      any             `json:"variables"`
}

// RequestContext 上游请求上下文。
type RequestContext struct {
	ClientVersion    string         `json:"clientVersion"`
	PagePath         string         `json:"pagePath"`
	PageViewID       int64          `json:"pageViewId"`
	TrackingID       string         `json:"trackingId"`
	BackendOverrides map[string]any `json:"backendOverrides"`
	ClientSessionID  string         `json:"clientSessionId"`
	SelectedPurview  map[string]any `json:"selectedPurview"`
	Jurisdiction     string         `json:"jurisdiction"`
	LocalizationData Localization   `json:"localizationData"`
}

// Localization 语言环境元数据。
type Localization struct {
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

// buildTypedRequestPayload 由强类型 GeminiRequest 构建发往上游的完整请求体强类型指针。
func buildTypedRequestPayload(model string, req *transform.GeminiRequest, recaptchaToken string, cfg config.ConfigProvider) *BatchGraphQLPayload {
	vars := transform.BuildGeminiVariablesTyped(model, req, cfg)
	vars.Region = "global"
	vars.RecaptchaToken = recaptchaToken

	return &BatchGraphQLPayload{
		RequestContext: &RequestContext{
			ClientVersion:    "boq_cloud-boq-clientweb-vertexaistudio_20260630.00_p0",
			PagePath:         "/agent-platform/studio/multimodal",
			PageViewID:       randomPageViewID(),
			TrackingID:       randomTrackingID(),
			BackendOverrides: map[string]any{},
			ClientSessionID:  randomUUID(),
			SelectedPurview:  map[string]any{},
			Jurisdiction:     "global",
			LocalizationData: Localization{
				Locale:   "zh_CN",
				Timezone: "Asia/Hong_Kong",
			},
		},
		QuerySignature: querySignature,
		OperationName:  operationName,
		Variables:      vars,
	}
}
