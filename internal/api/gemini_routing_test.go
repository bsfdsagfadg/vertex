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
			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if _, ok := body["models"]; !ok {
				t.Errorf("missing Gemini models field: %v", body)
			}
			if _, ok := body["data"]; ok {
				t.Errorf("unexpected OpenAI data field: %v", body)
			}
		})

		t.Run("UnknownMethod_"+prefix, func(t *testing.T) {
			req, _ := http.NewRequest("POST", fx.server.URL+prefix+"/models/gemini-2.5-flash:unknownMethod?key=sk-test-key", nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("POST unknown method failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d, want 404", resp.StatusCode)
			}
			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			errObj, ok := body["error"].(map[string]any)
			if !ok {
				t.Fatalf("missing Gemini error object: %v", body)
			}
			if _, ok := errObj["type"]; ok {
				t.Errorf("unexpected OpenAI type field: %v", errObj)
			}
			if got := errObj["status"]; got != "NOT_FOUND" {
				t.Errorf("status = %v, want NOT_FOUND", got)
			}
			if got := errObj["code"]; got != float64(http.StatusNotFound) {
				t.Errorf("code = %v, want 404", got)
			}
		})
	}
}
