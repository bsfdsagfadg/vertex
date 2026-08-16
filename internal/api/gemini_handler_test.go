package api

import (
	"encoding/json"
	"io"
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

// TestStreamGenerate_TruncatedAfterContent_RealErrorFrame 验证首帧后断流的透传层语义：
// 上游先交付有效内容帧再断流 → 客户端 SSE 收到内容帧 + 真实网络错误帧（error 对象，
// 而非 fake STOP 补帧或静默空响应），且内容帧先于错误帧到达。
func TestStreamGenerate_TruncatedAfterContent_RealErrorFrame(t *testing.T) {
	contentFrame := `{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"hello"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}]}}}}]}`

	mock := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		_, _ = w.Write([]byte(contentFrame))
		flusher.Flush()
		_, _ = w.Write([]byte(`{"a":}`))
		flusher.Flush()
	}

	fx := newTestServerCustomMock(t, mock, nil)

	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req, _ := http.NewRequest(http.MethodPost, fx.server.URL+"/v1beta/models/gemini-2.5-flash:streamGenerateContent?key=sk-test-key", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200: %s", resp.StatusCode, resp.Body)
	}

	raw, _ := io.ReadAll(resp.Body)
	events := strings.Split(string(raw), "data: ")
	if len(events) < 3 {
		t.Fatalf("期望至少 2 个 SSE 事件（内容 + 真实错误），实际 %d 个: %s", len(events)-1, raw)
	}

	var gotText string
	var gotError map[string]any
	contentSeen := false
	errorSeen := false
	for _, ev := range events[1:] {
		ev = strings.TrimSpace(ev)
		if ev == "" {
			continue
		}
		var obj struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
			Error map[string]any `json:"error"`
		}
		if err := json.Unmarshal([]byte(ev), &obj); err != nil {
			t.Fatalf("unmarshal SSE event: %v", err)
		}
		if obj.Error != nil {
			gotError = obj.Error
			errorSeen = true
		}
		if len(obj.Candidates) > 0 && len(obj.Candidates[0].Content.Parts) > 0 {
			gotText = obj.Candidates[0].Content.Parts[0].Text
			contentSeen = true
		}
	}

	if !contentSeen || gotText != "hello" {
		t.Errorf("期望内容帧 text=hello，实际 %q (seen=%v)", gotText, contentSeen)
	}
	if !errorSeen {
		t.Fatal("期望真实错误帧（非 fake STOP），实际无 error 事件")
	}
	if gotError["code"] != float64(500) && gotError["code"] != float64(502) {
		t.Errorf("期望网络类错误码（500/502），实际 %v", gotError["code"])
	}
	if msg, _ := gotError["message"].(string); !strings.Contains(msg, "已截断") && !strings.Contains(msg, "truncat") && !strings.Contains(msg, "中断") {
		t.Errorf("错误消息应体现截断语义，实际 %q", msg)
	}
}
