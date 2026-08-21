package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/infra/config"
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

// sseEvent 解析单个 SSE data 事件的轻量结构（覆盖本文件断言所需字段）。
type sseEvent struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error map[string]any `json:"error"`
}

func parseSSEEvents(t *testing.T, raw string) []sseEvent {
	t.Helper()
	var events []sseEvent
	for _, ev := range strings.Split(string(raw), "data: ") {
		ev = strings.TrimSpace(ev)
		if ev == "" {
			continue
		}
		var obj sseEvent
		if err := json.Unmarshal([]byte(ev), &obj); err != nil {
			t.Fatalf("unmarshal SSE event: %v", err)
		}
		events = append(events, obj)
	}
	return events
}

func hasStopFinishEvent(events []sseEvent) bool {
	for _, e := range events {
		for _, c := range e.Candidates {
			if c.FinishReason == "STOP" {
				return true
			}
		}
	}
	return false
}

// TestStreamGenerate_FirstFrameSafety_TrueStream 验证问题 6 修复：真流式首帧安全拦截
// 以 HTTP 200 + SSE 安全帧放行（携带真实拦截原因），而非 4xx JSON。
func TestStreamGenerate_FirstFrameSafety_TrueStream(t *testing.T) {
	mock := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"RECITATION"}}`))
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

	if resp.StatusCode == http.StatusBadRequest {
		t.Fatalf("安全拦截不得回退为 400（问题 6 回归），实际 400")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200: %s", resp.StatusCode, resp.Body)
	}

	raw, _ := io.ReadAll(resp.Body)
	events := parseSSEEvents(t, string(raw))
	if len(events) == 0 {
		t.Fatalf("无 SSE 事件: %s", raw)
	}
	first := events[0]
	if first.PromptFeedback.BlockReason != "RECITATION" {
		t.Errorf("blockReason=%q, want RECITATION", first.PromptFeedback.BlockReason)
	}
	if len(first.Candidates) == 0 {
		t.Fatal("安全帧应含 candidates 骨架")
	}
	if first.Candidates[0].FinishReason != "RECITATION" {
		t.Errorf("finishReason=%q, want RECITATION", first.Candidates[0].FinishReason)
	}
	if len(first.Candidates[0].Content.Parts) != 0 {
		t.Errorf("安全帧 candidates 应无内容 parts")
	}
}

// TestStreamGenerate_FirstFrameSafety_Aggregate 验证聚合流首帧安全拦截同样 200 放行（4.4(a)）。
func TestStreamGenerate_FirstFrameSafety_Aggregate(t *testing.T) {
	mock := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"PROHIBITED_CONTENT"}}`))
	}
	fx := newTestServerCustomMock(t, mock, func(cfg *config.AppConfig) {
		cfg.AggregateStream = true
	})

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
	events := parseSSEEvents(t, string(raw))
	if len(events) == 0 {
		t.Fatalf("无 SSE 事件: %s", raw)
	}
	if events[0].PromptFeedback.BlockReason != "PROHIBITED_CONTENT" {
		t.Errorf("blockReason=%q, want PROHIBITED_CONTENT", events[0].PromptFeedback.BlockReason)
	}
}

// TestStreamGenerate_FirstFrameNonSafetyError 回归：首帧非安全错误仍走 4xx JSON，不得被改写成 200。
func TestStreamGenerate_FirstFrameNonSafetyError(t *testing.T) {
	mock := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"bad request"}}`))
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

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非安全错误应保持 400 JSON，实际 %d: %s", resp.StatusCode, resp.Body)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["error"] == nil {
		t.Errorf("响应缺少 error 对象: %v", out)
	}
}

// TestStreamGenerate_CleanEOF_NoFakeStop 验证假 STOP 修复（5.3）：流以干净 EOF 结束
// 但从未出现真实 finishReason → 客户端收到截断错误帧，绝无 FinishReason=STOP 帧。
func TestStreamGenerate_CleanEOF_NoFakeStop(t *testing.T) {
	contentFrame := `{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"hello"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}]}}}}]}`

	mock := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(contentFrame))
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
	events := parseSSEEvents(t, string(raw))
	if hasStopFinishEvent(events) {
		t.Fatalf("禁止补发 FinishReason=STOP 假结束帧（假 STOP 修复回归），实际事件: %s", raw)
	}
	var gotError map[string]any
	for _, e := range events {
		if e.Error != nil {
			gotError = e.Error
		}
	}
	if gotError == nil {
		t.Fatalf("应收到截断错误帧，实际事件: %s", raw)
	}
	if msg, _ := gotError["message"].(string); !strings.Contains(msg, "已截断") && !strings.Contains(msg, "truncat") && !strings.Contains(msg, "中断") {
		t.Errorf("错误消息应体现截断语义，实际 %q", msg)
	}
}

// TestStreamGenerate_AbruptClose_ErrorFrameNoStop 端到端（5.1 + 5.3）：内容帧后连接被
// 强行切断 → 客户端收到真实网络错误帧且无 STOP 帧。
func TestStreamGenerate_AbruptClose_ErrorFrameNoStop(t *testing.T) {
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
		panic("simulated abrupt upstream disconnect")
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
	events := parseSSEEvents(t, string(raw))
	if hasStopFinishEvent(events) {
		t.Fatalf("断流场景禁止补发 STOP 帧，实际事件: %s", raw)
	}
	var gotError map[string]any
	for _, e := range events {
		if e.Error != nil {
			gotError = e.Error
		}
	}
	if gotError == nil {
		t.Fatalf("断流场景应收到网络错误帧，实际事件: %s", raw)
	}
}
