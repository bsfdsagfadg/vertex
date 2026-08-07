package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// ---- stripFakePrefix：剥离 "假非流-" 前缀 ----

func TestStripFakePrefix(t *testing.T) {
	fakePrefixes := []string{"假非流-"}
	cases := []struct {
		name      string
		in        string
		wantModel string
		wantFake  bool
	}{
		{"chinese prefix", "假非流-gemini-2.5-flash", "gemini-2.5-flash", true},
		{"no prefix passthrough", "gemini-2.5-flash", "gemini-2.5-flash", false},
		{"empty passthrough", "", "", false},
		{"chinese prefix only", "假非流-", "", true},
		{"old fake- prefix not recognized", "fake-gemini-2.5-pro", "fake-gemini-2.5-pro", false},
		{"old 假流式- prefix not recognized", "假流式-gemini-2.5-flash", "假流式-gemini-2.5-flash", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotModel, gotFake := stripFakePrefix(c.in, fakePrefixes)
			if gotModel != c.wantModel || gotFake != c.wantFake {
				t.Errorf("stripFakePrefix(%q)=(%q,%v)，期望 (%q,%v)",
					c.in, gotModel, gotFake, c.wantModel, c.wantFake)
			}
		})
	}
}

// TestModelsOAI 验证 /v1/models 返回 OpenAI 兼容的模型列表。
func TestModelsOAI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServer(t)

	req, _ := http.NewRequest("GET", fx.server.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-test-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["object"] != "list" {
		t.Errorf(`object=%q, want "list"`, body["object"])
	}
	data, ok := body["data"].([]any)
	if !ok {
		t.Fatal("data is not an array")
	}
	if len(data) == 0 {
		t.Fatal("data is empty")
	}
	first := data[0].(map[string]any)
	if first["object"] != "model" {
		t.Errorf(`first object=%q, want "model"`, first["object"])
	}
}
