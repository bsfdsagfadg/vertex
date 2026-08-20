package anonymousgraph

import (
	"crypto/rand"
	"math/big"
	"strings"

	"github.com/google/uuid"
)

const (
	QuerySignature = "2/l8eCsMMY49imcDQ/lwwXyL8cYtTjxZBF2dNqy69LodY="
	OperationName  = "StreamGenerateContentAnonymous"
)

// BuildEnvelope owns the Graph operation and requestContext wire contract.
// Callers provide canonical-upstream variables; this method clones them before
// adding anonymous-route authentication fields.
func BuildEnvelope(variables map[string]any, recaptchaToken string) map[string]any {
	cloned := make(map[string]any, len(variables)+2)
	for key, value := range variables {
		cloned[key] = value
	}
	cloned["region"] = "global"
	cloned["recaptchaToken"] = recaptchaToken
	return map[string]any{
		"requestContext": map[string]any{
			"clientVersion":    "boq_cloud-boq-clientweb-vertexaistudio_20260630.00_p0",
			"pagePath":         "/agent-platform/studio/multimodal",
			"pageViewId":       randomPageViewID(),
			"trackingId":       randomTrackingID(),
			"backendOverrides": map[string]any{},
			"clientSessionId":  strings.ToUpper(uuid.NewString()),
			"selectedPurview":  map[string]any{},
			"jurisdiction":     "global",
			"localizationData": map[string]any{
				"locale": "zh_CN", "timezone": "Asia/Hong_Kong",
			},
		},
		"querySignature": QuerySignature,
		"operationName":  OperationName,
		"variables":      cloned,
	}
}

func BuildCountTokensEnvelope(model string, contents []any, recaptchaToken, querySignature string) map[string]any {
	return map[string]any{
		"requestContext": map[string]any{
			"clientVersion": "boq_cloud-boq-clientweb-vertexaistudio_20260402.09_p0",
			"pagePath":      "/vertex-ai/studio/multimodal",
			"jurisdiction":  "global",
			"localizationData": map[string]any{
				"locale": "zh_CN", "timezone": "Asia/Shanghai",
			},
		},
		"querySignature": querySignature,
		"operationName":  "CountTokens",
		"variables": map[string]any{
			"contents": contents, "endpoint": "", "model": strings.TrimPrefix(model, "models/"),
			"region": "global", "recaptchaToken": recaptchaToken,
		},
	}
}

func randomPageViewID() int64 {
	const minimum = int64(1000000000000000)
	const span = int64(9000000000000000)
	number, err := rand.Int(rand.Reader, big.NewInt(span))
	if err != nil {
		return minimum
	}
	return minimum + number.Int64()
}

func randomTrackingID() string {
	digits := make([]byte, 16)
	for index := range digits {
		number, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			digits[index] = '0'
			continue
		}
		digits[index] = '0' + byte(number.Int64())
	}
	return "d" + string(digits)
}
