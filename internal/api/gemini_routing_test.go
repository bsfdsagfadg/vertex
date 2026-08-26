package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGeminiEndpointRouting(t *testing.T) {
	fx := newTestServer(t)
	client := &http.Client{}

	prefixes := []string{"/v1beta", "/v1beta1", "/v1alpha", "/v1"}

	// 1. 测试各前缀 GET .../models (Gemini 拉取模型)
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

			modelsArr, ok := geminiModelsResp["models"].([]any)
			if !ok || len(modelsArr) == 0 {
				t.Fatalf("Gemini models response 'models' must be non-empty array: %v", geminiModelsResp)
			}
			for _, entry := range modelsArr {
				obj, ok := entry.(map[string]any)
				if !ok {
					t.Fatalf("Gemini model entry must be object: %v", entry)
				}
				methods, ok := obj["supportedGenerationMethods"].([]any)
				if !ok {
					t.Errorf("model %v missing supportedGenerationMethods (下游客户端依赖此字段过滤)", obj["name"])
					continue
				}
				hasGenerate := false
				for _, mv := range methods {
					if s, _ := mv.(string); s == "generateContent" {
						hasGenerate = true
						break
					}
				}
				if !hasGenerate {
					t.Errorf("model %v supportedGenerationMethods=%v missing generateContent", obj["name"], methods)
				}
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
