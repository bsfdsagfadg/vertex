package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGeminiEndpointRouting(t *testing.T) {
	fx := newTestServer(t)
	client := &http.Client{}

	prefixes := []string{"/v1beta", "/v1beta1", "/v1alpha"}

	// 1. 测试 GET /v1beta/models, /v1beta1/models, /v1alpha/models (Gemini 拉取模型)
	for _, prefix := range prefixes {
		t.Run("ModelsList_"+prefix, func(t *testing.T) {
			req, _ := http.NewRequest("GET", fx.server.URL+prefix+"/models?key=sk-test-key", nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("GET %s/models failed: %v", prefix, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s/models status = %d, want 200", prefix, resp.StatusCode)
			}

			var geminiModelsResp map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&geminiModelsResp); err != nil {
				t.Fatalf("Failed to decode Gemini models response: %v", err)
			}

			if _, ok := geminiModelsResp["models"]; !ok {
				t.Errorf("Gemini models response missing 'models' field: %v", geminiModelsResp)
			}
			if _, ok := geminiModelsResp["data"]; ok {
				t.Errorf("Gemini models response unexpectedly contained OpenAI 'data' field: %v", geminiModelsResp)
			}
		})
	}

	// 2. 测试未知/错误路径在 Gemini 路由下的报错格式（Gemini 规范 error 格式，而非 OpenAI 规范）
	for _, prefix := range prefixes {
		t.Run("UnknownMethod_"+prefix, func(t *testing.T) {
			reqErr, _ := http.NewRequest("POST", fx.server.URL+prefix+"/models/gemini-2.5-flash:unknownMethod?key=sk-test-key", nil)
			respErr, err := client.Do(reqErr)
			if err != nil {
				t.Fatalf("POST unknownMethod failed: %v", err)
			}
			defer respErr.Body.Close()

			if respErr.StatusCode != http.StatusNotFound {
				t.Errorf("POST %s/models/... status = %d, want 404", prefix, respErr.StatusCode)
			}

			var errResp map[string]any
			if err := json.NewDecoder(respErr.Body).Decode(&errResp); err != nil {
				t.Fatalf("Failed to decode error response: %v", err)
			}

			errObj, ok := errResp["error"].(map[string]any)
			if !ok {
				t.Fatalf("Expected 'error' object in response, got: %v", errResp)
			}

			if _, hasType := errObj["type"]; hasType {
				t.Errorf("Gemini unknownMethod returned OpenAI error format with 'type' field: %v", errResp)
			}
			statusVal, hasStatus := errObj["status"]
			if !hasStatus {
				t.Errorf("Gemini unknownMethod missing 'status' field in Gemini error format: %v", errResp)
			} else if statusVal != "NOT_FOUND" {
				t.Errorf("Gemini unknownMethod status field = %v, want 'NOT_FOUND'", statusVal)
			}
			if codeVal, hasCode := errObj["code"]; !hasCode || codeVal != float64(404) {
				t.Errorf("Gemini unknownMethod code field = %v, want 404", codeVal)
			}
		})
	}
}
