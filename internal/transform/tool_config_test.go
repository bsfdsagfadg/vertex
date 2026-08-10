package transform

import (
	"encoding/json"
	"testing"
)

func TestPrepareNativeToolConfig_ValidatedMode(t *testing.T) {
	// 验证：客户端直接通过 Gemini REST API 传入 mode: "VALIDATED"
	rawJSON := `{
		"contents": [{"role": "user", "parts": [{"text": "hello"}]}],
		"tools": [{"functionDeclarations": [{"name": "test_fn"}]}],
		"toolConfig": {
			"functionCallingConfig": {
				"mode": "VALIDATED"
			}
		}
	}`

	req, err := NormalizeGeminiRequestMap(parseJSONMap(rawJSON))
	if err != nil {
		t.Fatalf("NormalizeGeminiRequestMap failed: %v", err)
	}

	vars := BuildGeminiVariables("gemini-3.6-flash", req, nil)

	tc, ok := vars["toolConfig"].(map[string]any)
	if !ok {
		t.Fatalf("toolConfig missing in vars: %v", vars)
	}

	fcc, ok := tc["functionCallingConfig"].(map[string]any)
	if !ok {
		t.Fatalf("functionCallingConfig missing in toolConfig: %v", tc)
	}

	if fcc["mode"] != "AUTO" {
		t.Errorf("mode=%v, want 'AUTO'", fcc["mode"])
	}
}

func parseJSONMap(s string) map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(s), &m)
	return m
}
