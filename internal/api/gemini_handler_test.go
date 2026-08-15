package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

func newGeminiHandlerForTest() *GeminiHandler {
	return &GeminiHandler{handler: handler{
		cfg: config.StaticProvider(config.DefaultConfig()),
	}}
}

func TestReadGeminiBody_WrappedGenerateContentRequest(t *testing.T) {
	h := newGeminiHandlerForTest()
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"generateContentRequest":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`))
	w := httptest.NewRecorder()

	req, ok := h.readGeminiBody(w, r)
	if !ok {
		t.Fatalf("readGeminiBody failed: %s", w.Body.String())
	}
	if len(req.Contents) != 1 || req.Contents[0].Parts[0].Text != "hi" {
		t.Errorf("contents 解析失败: %+v", req.Contents)
	}
}

func TestReadGeminiBody_DirectBody(t *testing.T) {
	h := newGeminiHandlerForTest()
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"direct"}]}],"generationConfig":{"temperature":0.5}}`))
	w := httptest.NewRecorder()

	req, ok := h.readGeminiBody(w, r)
	if !ok {
		t.Fatalf("readGeminiBody failed: %s", w.Body.String())
	}
	if len(req.Contents) != 1 || req.Contents[0].Parts[0].Text != "direct" {
		t.Errorf("contents 解析失败: %+v", req.Contents)
	}
	if req.GenerationConfig == nil || req.GenerationConfig.Temperature == nil || *req.GenerationConfig.Temperature != 0.5 {
		t.Errorf("generationConfig 解析失败: %+v", req.GenerationConfig)
	}
}

func TestReadGeminiBody_InvalidJSON(t *testing.T) {
	h := newGeminiHandlerForTest()
	for _, body := range []string{``, `{"a":}`, `[1,2,3]`, `"str"`, `{"text":"` + "\x01" + `"}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		w := httptest.NewRecorder()
		if req, ok := h.readGeminiBody(w, r); ok || req != nil {
			t.Errorf("body=%q 应报错, req=%+v", body, req)
		}
		if w.Code != http.StatusBadRequest {
			t.Errorf("body=%q status=%d, want 400", body, w.Code)
		}
		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["error"] == nil {
			t.Errorf("body=%q 响应缺少 error 对象: %s", body, w.Body.String())
		}
	}
}

func TestReadGeminiBody_EmptyAndNull(t *testing.T) {
	h := newGeminiHandlerForTest()
	for _, body := range []string{`{}`, `null`, `{"generateContentRequest": null}`, `{"generateContentRequest": 123}`, `{}garbage`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		w := httptest.NewRecorder()
		req, ok := h.readGeminiBody(w, r)
		if !ok {
			t.Fatalf("body=%q 不应报错: %s", body, w.Body.String())
		}
		if req == nil {
			t.Fatalf("body=%q req 为 nil", body)
		}
		if len(req.Contents) != 0 {
			t.Errorf("body=%q 应得到空请求, got %+v", body, req.Contents)
		}
	}
}

// handleCountTokens 集成：离线估算直通（强类型 contents，零 map 往返）。
func TestHandleCountTokens_EndToEnd(t *testing.T) {
	fx := newTestServer(t)
	client := &http.Client{}

	body := `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
	req, _ := http.NewRequest("POST", fx.server.URL+"/v1beta/models/gemini-2.5-flash:countTokens?key=sk-test-key", strings.NewReader(body))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("countTokens request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("countTokens status=%d, want 200: %s", resp.StatusCode, resp.Body)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// "hello" = 5 ASCII → 5/4 = 1
	if total, ok := out["totalTokens"].(float64); !ok || total != 1 {
		t.Errorf("totalTokens=%v, want 1", out["totalTokens"])
	}
}

// handleCountTokens 畸形请求体 → 400 invalid JSON（与 readGeminiBody 同构）。
func TestHandleCountTokens_InvalidBody(t *testing.T) {
	fx := newTestServer(t)
	client := &http.Client{}

	req, _ := http.NewRequest("POST", fx.server.URL+"/v1beta/models/gemini-2.5-flash:countTokens?key=sk-test-key", strings.NewReader(`{"a":}`))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("countTokens request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("countTokens status=%d, want 400", resp.StatusCode)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["error"] == nil {
		t.Errorf("响应缺少 error 对象: %v", out)
	}
}