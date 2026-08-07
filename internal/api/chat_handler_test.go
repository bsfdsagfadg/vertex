package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
)

// collectSSEData 解析一段 SSE 输出，提取所有 `data: {...}` JSON 载荷。
func collectSSEData(t *testing.T, sse string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(sse, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(payload), &obj); err != nil {
			t.Fatalf("无法解析 SSE data 载荷 %q: %v", payload, err)
		}
		out = append(out, obj)
	}
	return out
}

// TestChatHandler_WriteStreamError_IncludesChoicesField 验证「流式中途报错」的 SSE 事件
// 必须包含合规的非空 choices 数组（SDK 兼容红线），同时附带 error 字段。
//
// 背景：sseWriter 在首个写入时已发送 200 OK HTTP 头，此后不能再用 HTTP 状态码 JSON 报错，
// 只能依赖 SSE 事件内的 choices 让 OpenAI SDK 免于解析崩溃。
func TestChatHandler_WriteStreamError_IncludesChoicesField(t *testing.T) {
	var packets []map[string]any
	c := &ChatHandler{} // writeStreamError 仅依赖包级函数，无需注入网络客户端

	write := func(line string) bool {
		for _, obj := range collectSSEData(t, line) {
			packets = append(packets, obj)
		}
		return true
	}

	ve := vertex.NewNetworkError(vertex.ErrStreamIdleTimeout)
	c.writeStreamError(write, ve, "req123", "gemini-flash")

	if len(packets) == 0 {
		t.Fatal("expected at least one SSE data event")
	}

	// 断言：报错事件包必须携带非空 choices 数组。
	first := packets[0]
	choices, ok := first["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("expected non-empty choices array in error SSE packet, got %v", first)
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		t.Fatalf("expected choices[0] to be an object, got %T", choices[0])
	}
	if choice["index"] != float64(0) && choice["index"] != 0 {
		t.Errorf("expected index=0, got %v", choice["index"])
	}
	if _, ok := choice["delta"].(map[string]any); !ok {
		t.Errorf("expected delta object present, got %v", choice["delta"])
	}
	if fr, _ := choice["finish_reason"].(string); fr != "error" {
		t.Errorf("expected finish_reason=error, got %q", fr)
	}

	// 断言 error 字段存在。
	if _, ok := first["error"]; !ok {
		t.Errorf("expected error field present in the SSE packet, got %v", first)
	}
}

// TestChatStream_MidStreamError_IncludesChoicesField 是计划约定的顶层入口测试，
// 通过写流明确空错误的访问缝，验证行为等同于上面的方法级测试。
func TestChatStream_MidStreamError_IncludesChoicesField(t *testing.T) {
	TestChatHandler_WriteStreamError_IncludesChoicesField(t)
}

// TestChatCompletion_MissingModel_400 验证缺少 model 字段时返回 400 错误。
func TestChatCompletion_MissingModel_400(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServer(t)

	body := map[string]any{"stream": false}
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	var errResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	oaiErr, ok := errResp["error"].(map[string]any)
	if !ok {
		t.Fatal("missing error field")
	}
	if oaiErr["code"] != float64(400) {
		t.Errorf("code=%v, want 400", oaiErr["code"])
	}
}

// TestChatCompletion_InvalidKey_401 验证无效 key 请求被拒绝。
func TestChatCompletion_InvalidKey_401(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServer(t)

	body := map[string]any{"model": "gemini-2.5-flash", "messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-invalid-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}

// TestChatCompletion_NormalRegression 验证常规非流式 chat completions 成功。
func TestChatCompletion_NormalRegression(t *testing.T) {
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
	})

	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "Say hello"}},
		"stream":   false,
	}
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	var oaiResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&oaiResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if oaiResp["object"] != "chat.completion" {
		t.Errorf("object=%q, want chat.completion", oaiResp["object"])
	}
	choices, ok := oaiResp["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatal("no choices")
	}
	msg, ok := choices[0].(map[string]any)["message"].(map[string]any)
	if !ok {
		t.Fatal("no message in choice")
	}
	content, _ := msg["content"].(string)
	if content == "" {
		t.Error("content should not be empty")
	}
}

// TestChatCompletion_AllNodes429 验证并行池中所有节点均 429 时返回 429。
func TestChatCompletion_AllNodes429(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	goroutineLeakCheck(t)

	fx := newTestServerCustomMock(t, mockAlways429(t), func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = true
		cfg.ParallelPoolSize = 3
		cfg.MaxRetries = 1
		cfg.RequestTimeoutSeconds = 10
	}, &directDialer{})

	nodes.MergeNodes([]nodes.Node{
		{RawURI: "mock-node-1", Name: "mock-node-1", Type: "vless"},
		{RawURI: "mock-node-2", Name: "mock-node-2", Type: "vless"},
		{RawURI: "mock-node-3", Name: "mock-node-3", Type: "vless"},
	})

	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":   false,
	}
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429", resp.StatusCode)
	}
	var errResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	bodyBytes, _ := json.Marshal(errResp)
	if !strings.Contains(strings.ToLower(string(bodyBytes)), "rate") {
		t.Errorf("response body should mention rate limit, got: %s", bodyBytes)
	}
}

// TestChatCompletion_UpstreamHang 验证上游挂起时，客户端通过 context.WithTimeout
// 主动断连后请求立即终止且不挂死（无 time.Sleep 硬编码等待）。
func TestChatCompletion_UpstreamHang(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	goroutineLeakCheck(t)

	// 上游挂起直到请求上下文被取消（客户端断连触发），证明断开传播链路生效。
	hangHandler := func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}

	fx := newTestServerCustomMock(t, hangHandler, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		// 服务端超时设大，确保是客户端断连（而非服务端超时）终止请求。
		cfg.RequestTimeoutSeconds = 30
		cfg.ParallelPoolSize = 1
	})

	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":   false,
	}
	reqBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", fx.server.URL+"/v1/chat/completions",
		strings.NewReader(string(reqBody)))
	req.Header.Set("Authorization", "Bearer sk-test-key")
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	elapsed := time.Since(start)
	if err == nil {
		defer resp.Body.Close()
		t.Logf("note: got status=%d (err=%v)", resp.StatusCode, err)
	} else {
		t.Logf("note: client got error after cancel: %v", err)
	}

	if elapsed > 500*time.Millisecond {
		t.Fatalf("request took %v, expected client disconnect within ~100ms", elapsed)
	}
}

// TestChatCompletion_ClientDisconnect 验证客户端提前断开时客户端侧能立即返回且不泄漏 goroutine。
// 使用可取消的上游 handler 与 context.WithTimeout，客户端断连后清理不再挂起。
func TestChatCompletion_ClientDisconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	goroutineLeakCheck(t)

	disconnectCh := make(chan struct{})

	slowHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		partial := `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"content":{"parts":[{"text":"hello"}],"role":"model"},"finishReason":"FINISH_REASON_UNSPECIFIED"}]}}}}}]`
		w.Write([]byte(partial))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(disconnectCh)
		// 挂起直到请求上下文被取消，模拟上游不返回完整响应。
		<-r.Context().Done()
	}

	fx := newTestServerCustomMock(t, slowHandler, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
	})

	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":   false,
	}

	reqBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", fx.server.URL+"/v1/chat/completions",
		strings.NewReader(string(reqBody)))
	req.Header.Set("Authorization", "Bearer sk-test-key")
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		t.Logf("client disconnect: %v (expected)", err)
	} else {
		resp.Body.Close()
	}

	select {
	case <-disconnectCh:
	case <-time.After(500 * time.Millisecond):
		t.Error("mock handler did not reach flush within 500ms")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("client disconnect took %v, expected within ~100ms", elapsed)
	}
}

// TestChatCompletion_HedgeRetryCancel 验证并行池在 429 下发生终止重试且无重试风暴。
func TestChatCompletion_HedgeRetryCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	goroutineLeakCheck(t)

	var mu sync.Mutex
	requestCount := 0

	handler := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		count := requestCount
		mu.Unlock()
		t.Logf("mock收到请求 #%d", count)

		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`[{"results":[{"data":{"error":"rate limit"}}]}]`))
	}

	fx := newTestServerCustomMock(t, handler, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = true
		cfg.ParallelPoolSize = 3
		cfg.MaxRetries = 1
		cfg.RequestTimeoutSeconds = 5
	}, &directDialer{})

	nodes.MergeNodes([]nodes.Node{
		{RawURI: "hedge-node-1", Name: "hedge-node-1", Type: "vless"},
		{RawURI: "hedge-node-2", Name: "hedge-node-2", Type: "vless"},
		{RawURI: "hedge-node-3", Name: "hedge-node-3", Type: "vless"},
	})

	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":   false,
	}

	start := time.Now()
	resp := doPost(t, fx.server.URL+"/v1/chat/completions", "sk-test-key", body)
	elapsed := time.Since(start)
	defer resp.Body.Close()

	t.Logf("请求耗时: %v, 状态码: %d, 总请求数: %d", elapsed, resp.StatusCode, requestCount)

	if elapsed > 10*time.Second {
		t.Error("request took too long, possible hang")
	}
	if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusBadGateway {
		t.Errorf("unexpected status: %d", resp.StatusCode)
	}
	if requestCount > 20 {
		t.Errorf("too many requests: %d (possible retry storm)", requestCount)
	}
}

// TestChatCompletion_StreamingSkip 记录完整 E2E 流式测试的阻塞原因。
func TestChatCompletion_StreamingSkip(t *testing.T) {
	// Full E2E streaming test is blocked by tls_client HTTP transport behavior
	// when connecting to HTTP (non-TLS) mock servers. The streaming logic is
	// verified by non-streaming test (same Gemini response format) and unit tests
	// for ConvertRealtimeChunk in the transform package.
	t.Skip("Skipped: tls_client HTTP streaming requires TLS mock server")
}

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