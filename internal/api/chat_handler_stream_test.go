package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// geminiRichResponseUnspecified 返回与 geminiRichResponse 相同的 parts 和 usageMetadata，
// 但 finishReason 为 FINISH_REASON_UNSPECIFIED，cleanGeminiFinishReason 应删除此键。
func geminiRichResponseUnspecified() string {
	return `{"candidates":[{"content":{"parts":[{"text":"Hello! "},{"text":"I'm thinking deeply","thought":true},{"functionCall":{"name":"get_weather","args":{"location":"Shanghai"}}},{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgo="}}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}],"usageMetadata":{"promptTokenCount":15,"candidatesTokenCount":42,"totalTokenCount":57,"thoughtsTokenCount":10}}`
}

// assertGeminiRichSSEPartsAndUsage 验证 Gemini rich SSE 的公共结构：事件格式、4 个 parts、usageMetadata。
// 返回第一个 candidate 供调用者做 finishReason 特定的断言。
func assertGeminiRichSSEPartsAndUsage(t *testing.T, data []byte) map[string]any {
	t.Helper()
	events := strings.Split(strings.TrimSpace(string(data)), "\n\n")
	if len(events) != 1 {
		t.Fatalf("expected 1 SSE event (no [DONE] for Gemini), got %d: %q", len(events), string(data))
	}
	if !strings.HasPrefix(events[0], "data: ") {
		t.Errorf("event should start with 'data: ', got: %s", events[0][:min(40, len(events[0]))])
	}

	jsonStr := strings.TrimPrefix(events[0], "data: ")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	cands, ok := parsed["candidates"].([]any)
	if !ok || len(cands) == 0 {
		t.Fatal("response missing candidates")
	}
	cand, ok := cands[0].(map[string]any)
	if !ok {
		t.Fatal("candidate not a map")
	}

	content, ok := cand["content"].(map[string]any)
	if !ok {
		t.Fatal("candidate.content missing")
	}
	parts, ok := content["parts"].([]any)
	if !ok || len(parts) != 4 {
		t.Fatalf("expected 4 parts, got %d", len(parts))
	}
	p0, _ := parts[0].(map[string]any)
	if p0["text"] != "Hello! " {
		t.Errorf("part[0].text=%q, want 'Hello! '", p0["text"])
	}
	p1, _ := parts[1].(map[string]any)
	if p1["text"] != "I'm thinking deeply" {
		t.Errorf("part[1].text=%q, want 'I'm thinking deeply'", p1["text"])
	}
	if p1["thought"] != true {
		t.Error("part[1].thought should be true")
	}
	p2, _ := parts[2].(map[string]any)
	fc, ok := p2["functionCall"].(map[string]any)
	if !ok {
		t.Fatal("part[2].functionCall missing")
	}
	if fc["name"] != "get_weather" {
		t.Errorf("functionCall.name=%q, want 'get_weather'", fc["name"])
	}
	p3, _ := parts[3].(map[string]any)
	id, ok := p3["inlineData"].(map[string]any)
	if !ok {
		t.Fatal("part[3].inlineData missing")
	}
	if id["mimeType"] != "image/png" {
		t.Errorf("inlineData.mimeType=%q, want 'image/png'", id["mimeType"])
	}
	if id["data"] != "iVBORw0KGgo=" {
		t.Errorf("inlineData.data=%q, want 'iVBORw0KGgo='", id["data"])
	}

	um, ok := parsed["usageMetadata"].(map[string]any)
	if !ok {
		t.Fatal("usageMetadata missing")
	}
	if pt, _ := um["promptTokenCount"].(float64); pt != 15 {
		t.Errorf("promptTokenCount=%v, want 15", pt)
	}
	if ct, _ := um["candidatesTokenCount"].(float64); ct != 42 {
		t.Errorf("candidatesTokenCount=%v, want 42", ct)
	}
	if tt, _ := um["totalTokenCount"].(float64); tt != 57 {
		t.Errorf("totalTokenCount=%v, want 57", tt)
	}
	if tt, _ := um["thoughtsTokenCount"].(float64); tt != 10 {
		t.Errorf("thoughtsTokenCount=%v, want 10", tt)
	}

	return cand
}

// assertGeminiRichSSE 验证 Gemini rich SSE 事件的候选结构、4 个 parts、usageMetadata
// 以及 finishReason 为 "STOP"。
func assertGeminiRichSSE(t *testing.T, data []byte) {
	t.Helper()
	cand := assertGeminiRichSSEPartsAndUsage(t, data)
	if fr, _ := cand["finishReason"].(string); fr != "STOP" {
		t.Errorf("finishReason=%q, want 'STOP'", fr)
	}
}

// assertGeminiRichSSEUnspecified 验证 finishReason 为 FINISH_REASON_UNSPECIFIED
// 时，cleanGeminiFinishReason 已将其删除（候选存在但无 finishReason 键）。
func assertGeminiRichSSEUnspecified(t *testing.T, data []byte) {
	t.Helper()
	cand := assertGeminiRichSSEPartsAndUsage(t, data)
	if fr, exists := cand["finishReason"]; exists {
		t.Errorf("finishReason should be deleted, got %v", fr)
	}
}

// ---- 单包 SSE 回归测试 ----

// TestSinglePacketSSE_OAI_FakeNonStream 验证 OpenAI 假非流前缀走单包 SSE。
func TestSinglePacketSSE_OAI_FakeNonStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiRichResponse())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
	})

	body := map[string]any{
		"model":    "假非流-gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "Say hello"}},
		"stream":   true,
	}
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	assertOAIStreamSSE(t, data)
}

// TestSinglePacketSSE_OAI_AggregateStream 验证 aggregate_stream=true 走单包 SSE。
func TestSinglePacketSSE_OAI_AggregateStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiRichResponse())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
		cfg.AggregateStream = true
	})

	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "Say hello"}},
		"stream":   true,
	}
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	assertOAIStreamSSE(t, data)
}

// TestSinglePacketSSE_Gemini_AggregateStream 验证 Gemini streamGenerateContent
// 在 aggregate_stream=true 时走单包 SSE（无 [DONE]）。
func TestSinglePacketSSE_Gemini_AggregateStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiRichResponse())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
		cfg.AggregateStream = true
	})

	body := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Say hello"}}}},
	}
	resp := doPost(t, fx.server.URL+"/v1/models/gemini-2.5-flash:streamGenerateContent", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	assertGeminiRichSSE(t, data)
}

// TestSinglePacketSSE_Gemini_FakeNonStream 验证 Gemini 假非流前缀走单包 SSE（无 [DONE]）。
func TestSinglePacketSSE_Gemini_FakeNonStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiRichResponse())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
	})

	body := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Say hello"}}}},
	}
	resp := doPost(t, fx.server.URL+"/v1/models/假非流-gemini-2.5-flash:streamGenerateContent", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	assertGeminiRichSSE(t, data)
}

// TestSinglePacketSSE_Gemini_GenerateContent 验证 Gemini generateContent 在
// aggregate_stream=true 时仍输出普通 JSON，不走 SSE。
func TestSinglePacketSSE_Gemini_GenerateContent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiNonStreamingResponse())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
		cfg.AggregateStream = true
	})

	body := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Say hello"}}}},
	}
	resp := doPost(t, fx.server.URL+"/v1/models/gemini-2.5-flash:generateContent", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type=%q, want application/json", ct)
	}

	var parsed map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatalf("decode JSON body: %v", err)
	}
	if _, ok := parsed["candidates"]; !ok {
		t.Error("response missing candidates")
	}
}

// TestSinglePacketSSE_Gemini_AggregateStream_Unspecified 验证 FINISH_REASON_UNSPECIFIED
// 在 aggregate_stream=true 时被 cleanGeminiFinishReason 删除。
func TestSinglePacketSSE_Gemini_AggregateStream_Unspecified(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiRichResponseUnspecified())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
		cfg.AggregateStream = true
	})

	body := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Say hello"}}}},
	}
	resp := doPost(t, fx.server.URL+"/v1/models/gemini-2.5-flash:streamGenerateContent", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	assertGeminiRichSSEUnspecified(t, data)
}

// TestSinglePacketSSE_Gemini_FakeNonStream_Unspecified 验证 FINISH_REASON_UNSPECIFIED
// 在假非流前缀时被 cleanGeminiFinishReason 删除。
func TestSinglePacketSSE_Gemini_FakeNonStream_Unspecified(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := fmt.Sprintf(`[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":%s}}}]}]`,
			geminiRichResponseUnspecified())
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
	})

	body := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Say hello"}}}},
	}
	resp := doPost(t, fx.server.URL+"/v1/models/假非流-gemini-2.5-flash:streamGenerateContent", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	assertGeminiRichSSEUnspecified(t, data)
}

// TestChatCompletion_EmptyTextResponse_Returns502 验证上游返回无有效文本内容的空包时返回 502。
func TestChatCompletion_EmptyTextResponse_Returns502(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"finishReason":"STOP","content":{"parts":[]}}]}}}}]}]`
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
	})

	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "Say hello"}},
		"stream":   false,
	}
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502", resp.StatusCode)
	}
}

// TestChatCompletion_ImageStream_EmptyResponse_Returns502 验证生图模型走 Chat 端点且 stream=true 时空响应返回 502。
func TestChatCompletion_ImageStream_EmptyResponse_Returns502(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"finishReason":"STOP","content":{"parts":[]}}]}}}}]}]`
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
	})

	body := map[string]any{
		"model":    "gemini-3.1-flash-image",
		"messages": []any{map[string]any{"role": "user", "content": "Generate a cute cat"}},
		"stream":   true,
	}
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	var errObj map[string]any
	if err := json.Unmarshal(data, &errObj); err != nil {
		t.Fatalf("failed to unmarshal error json: %v", err)
	}
	if _, ok := errObj["error"]; !ok {
		t.Fatalf("expected 'error' field in 502 response, got: %s", string(data))
	}
}

// TestChatCompletion_ImageNonStream_EmptyResponse_Returns502 验证生图模型走 Chat 端点且 stream=false 时空响应返回 502。
func TestChatCompletion_ImageNonStream_EmptyResponse_Returns502(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"finishReason":"STOP","content":{"parts":[]}}]}}}}]}]`
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
	})

	body := map[string]any{
		"model":    "gemini-3.1-flash-image",
		"messages": []any{map[string]any{"role": "user", "content": "Generate a cute cat"}},
		"stream":   false,
	}
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	var errObj map[string]any
	if err := json.Unmarshal(data, &errObj); err != nil {
		t.Fatalf("failed to unmarshal error json: %v", err)
	}
	if _, ok := errObj["error"]; !ok {
		t.Fatalf("expected 'error' field in 502 response, got: %s", string(data))
	}
}

// TestChatCompletion_ImageStream_ValidImage_Returns200SSE 验证正常返回有效 InlineData 图片时返回 200 且 SSE 包含 markdown 图片内容。
func TestChatCompletion_ImageStream_ValidImage_Returns200SSE(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		resp := `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"finishReason":"STOP","content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}}]}}]}}}}]}]`
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
	})

	body := map[string]any{
		"model":    "gemini-3.1-flash-image",
		"messages": []any{map[string]any{"role": "user", "content": "Generate a cute cat"}},
		"stream":   true,
	}
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	sseStr := string(data)
	if !strings.Contains(sseStr, "data:") {
		t.Fatalf("expected SSE data format, got: %s", sseStr)
	}
	if !strings.Contains(sseStr, "![image](data:image/png;base64,") {
		t.Fatalf("expected markdown image format in SSE output, got: %s", sseStr)
	}
	if !strings.Contains(sseStr, "data: [DONE]") {
		t.Fatalf("expected [DONE] terminator in SSE output, got: %s", sseStr)
	}
}
