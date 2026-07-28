package transform

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// ============ 多模态 content 转换 ============

func TestConvertUserContent_ImageDataURI(t *testing.T) {
	content := []any{
		map[string]any{"type": "text", "text": "看图"},
		map[string]any{"type": "image_url", "image_url": map[string]any{
			"url": "data:image/png;base64,AAAA",
		}},
	}
	parts := convertUserContent(content)
	if len(parts) < 2 {
		t.Fatalf("parts len=%d, want at least 2", len(parts))
	}
	id, ok := parts[1].(map[string]any)["inlineData"].(map[string]any)
	if !ok {
		t.Fatalf("part[1] 不是 inlineData: %v", parts[1])
	}
	if id["mimeType"] != "image/png" || id["data"] != "AAAA" {
		t.Errorf("inlineData=%v", id)
	}
}

func TestConvertUserContent_ImageRemoteURL(t *testing.T) {
	content := []any{
		map[string]any{"type": "image_url", "image_url": map[string]any{
			"url": "https://example.com/cat.jpeg",
		}},
	}
	parts := convertUserContent(content)
	if len(parts) < 1 {
		t.Fatalf("parts len=%d, want at least 1", len(parts))
	}
	fd, ok := parts[0].(map[string]any)["fileData"].(map[string]any)
	if !ok {
		t.Fatalf("远程 URL 应转 fileData: %v", parts[0])
	}
	if fd["mimeType"] != "image/jpeg" || fd["fileUri"] != "https://example.com/cat.jpeg" {
		t.Errorf("fileData=%v", fd)
	}
}

func TestConvertUserContent_Video(t *testing.T) {
	// data: URI 不带 video/* mime → 回退 video/mp4
	content := []any{
		map[string]any{"type": "video_url", "video_url": map[string]any{"url": "data:application/octet-stream;base64,QkJC"}},
	}
	parts := convertUserContent(content)
	if len(parts) < 1 {
		t.Fatalf("parts len=%d, want at least 1", len(parts))
	}
	id := parts[0].(map[string]any)["inlineData"].(map[string]any)
	if id["mimeType"] != "video/mp4" {
		t.Errorf("video mime=%v, want video/mp4 回退", id["mimeType"])
	}
	// input_video 字段名 + 显式 video mime
	content2 := []any{
		map[string]any{"type": "input_video", "input_video": "data:video/webm;base64,QkJC"},
	}
	parts2 := convertUserContent(content2)
	if len(parts2) < 1 {
		t.Fatalf("parts2 len=%d, want at least 1", len(parts2))
	}
	id2 := parts2[0].(map[string]any)["inlineData"].(map[string]any)
	if id2["mimeType"] != "video/webm" {
		t.Errorf("input_video mime=%v", id2["mimeType"])
	}
}

func TestConvertUserContent_InputAudio(t *testing.T) {
	// {data, format} 形态
	content := []any{
		map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "QUFB", "format": "mp3"}},
	}
	parts := convertUserContent(content)
	if len(parts) < 1 {
		t.Fatalf("parts len=%d, want at least 1", len(parts))
	}
	id := parts[0].(map[string]any)["inlineData"].(map[string]any)
	if id["mimeType"] != "audio/mpeg" {
		t.Errorf("audio mime=%v, want audio/mpeg", id["mimeType"])
	}
	// 未知 format → 回退 audio/wav
	content2 := []any{
		map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "QUFB", "format": "xyz"}},
	}
	parts2 := convertUserContent(content2)
	if len(parts2) < 1 {
		t.Fatalf("parts2 len=%d, want at least 1", len(parts2))
	}
	id2 := parts2[0].(map[string]any)["inlineData"].(map[string]any)
	if id2["mimeType"] != "audio/wav" {
		t.Errorf("未知 format mime=%v, want audio/wav 回退", id2["mimeType"])
	}
	// data: URI 形态
	content3 := []any{
		map[string]any{"type": "input_audio", "input_audio": "data:audio/flac;base64,QUFB"},
	}
	parts3 := convertUserContent(content3)
	if len(parts3) < 1 {
		t.Fatalf("parts3 len=%d, want at least 1", len(parts3))
	}
	id3 := parts3[0].(map[string]any)["inlineData"].(map[string]any)
	if id3["mimeType"] != "audio/flac" {
		t.Errorf("audio data URI mime=%v", id3["mimeType"])
	}
}

// ============ 工具调用：声明 + tool_choice ============

func TestConvertChatRequest_Tools(t *testing.T) {
	body := map[string]any{
		"model":    "m",
		"messages": []any{map[string]any{"role": "user", "content": "天气?"}},
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "查天气",
				"parameters": map[string]any{
					"type":                 "object",
					"$schema":              "http://json-schema.org/draft-07/schema#", // 应被白名单剔除
					"additionalProperties": false,                                     // 应被剔除
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
					"required": []any{"city"},
				},
			},
		}},
		"tool_choice": "required",
	}
	_, payload, err := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig()))
	if err != nil {
		t.Fatal(err)
	}
	tools := payload["tools"].([]any)
	fd := tools[0].(map[string]any)["functionDeclarations"].([]any)
	decl := fd[0].(map[string]any)
	if decl["name"] != "get_weather" {
		t.Errorf("name=%v", decl["name"])
	}
	params := decl["parameters"].(map[string]any)
	if _, ok := params["$schema"]; ok {
		t.Error("$schema 应被白名单剔除")
	}
	if _, ok := params["additionalProperties"]; ok {
		t.Error("additionalProperties 应被剔除")
	}
	if _, ok := params["properties"]; !ok {
		t.Error("properties 应保留")
	}
	tc := payload["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)
	if tc["mode"] != "ANY" {
		t.Errorf("required → mode=%v, want ANY", tc["mode"])
	}
}

func TestConvertChatRequest_ToolChoiceRequiredNoTools(t *testing.T) {
	body := map[string]any{
		"model":       "m",
		"messages":    []any{map[string]any{"role": "user", "content": "hi"}},
		"tool_choice": "required",
	}
	if _, _, err := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig())); err == nil {
		t.Error("required 无工具应报错")
	}
}

func TestConvertChatRequest_ToolChoiceUnknownFunc(t *testing.T) {
	body := map[string]any{
		"model":       "m",
		"messages":    []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":       []any{map[string]any{"type": "function", "function": map[string]any{"name": "a"}}},
		"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "b"}},
	}
	if _, _, err := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig())); err == nil {
		t.Error("引用未声明函数应报错")
	}
}

func TestConvertChatRequest_LegacyFunctions(t *testing.T) {
	// 顶层 functions（已废弃）+ function_call。
	body := map[string]any{
		"model":         "m",
		"messages":      []any{map[string]any{"role": "user", "content": "hi"}},
		"functions":     []any{map[string]any{"name": "f1", "description": "d"}},
		"function_call": "auto",
	}
	_, payload, err := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig()))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["tools"]; !ok {
		t.Error("legacy functions 应转成 tools")
	}
	if payload["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)["mode"] != "AUTO" {
		t.Error("legacy function_call=auto → AUTO")
	}
}

// ============ 工具调用：id 锚点 + 哨兵（多轮 round-trip） ============

func TestToolCallRoundTrip_IDAnchor(t *testing.T) {
	// 模拟多轮：user → assistant(tool_calls) → tool(结果) → 走完整 BuildVertexVariables 管线。
	body := map[string]any{
		"model": "m",
		"messages": []any{
			map[string]any{"role": "user", "content": "北京和上海天气?"},
			map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
				map[string]any{"id": "call_BJ", "type": "function", "function": map[string]any{
					"name": "get_weather", "arguments": `{"city":"北京"}`,
				}},
				map[string]any{"id": "call_SH", "type": "function", "function": map[string]any{
					"name": "get_weather", "arguments": `{"city":"上海"}`,
				}},
			}},
			// 故意乱序回传 + 缺 name，验证按 id 反查精确配对（不会错配）。
			map[string]any{"role": "tool", "tool_call_id": "call_SH", "content": `{"temp":"20C"}`},
			map[string]any{"role": "tool", "tool_call_id": "call_BJ", "content": `{"temp":"5C"}`},
		},
	}
	model, gemini, err := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig()))
	if err != nil {
		t.Fatal(err)
	}
	vars := BuildVertexVariables(model, gemini, config.StaticProvider(config.DefaultConfig()))
	contents := vars["contents"].([]any)

	// 找到 model 消息：两个 functionCall 都应带 base64 编码的 thoughtSignature 哨兵、不带 id。
	var modelParts []any
	var funcParts []any
	for _, c := range contents {
		cm := c.(map[string]any)
		switch cm["role"] {
		case "model":
			modelParts = cm["parts"].([]any)
		case "function":
			funcParts = append(funcParts, cm["parts"].([]any)...)
		}
	}
	if len(modelParts) != 2 {
		t.Fatalf("model parts=%d, want 2", len(modelParts))
	}
	wantSig := base64.StdEncoding.EncodeToString([]byte(skipThoughtSentinel))
	for _, p := range modelParts {
		pm := p.(map[string]any)
		fc := pm["functionCall"].(map[string]any)
		if _, hasID := fc["id"]; hasID {
			t.Error("functionCall 不应残留内部 id 锚点")
		}
		// args 应被解析为对象
		if _, ok := fc["args"].(map[string]any); !ok {
			t.Errorf("args 应是对象: %v", fc["args"])
		}
		if pm["thoughtSignature"] != wantSig {
			t.Errorf("哨兵 thoughtSignature=%v, want %v", pm["thoughtSignature"], wantSig)
		}
	}

	// functionResponse：按 id 反查 name。call_SH→get_weather；乱序也不错配。
	if len(funcParts) != 2 {
		t.Fatalf("function parts=%d, want 2", len(funcParts))
	}
	for _, p := range funcParts {
		fr := p.(map[string]any)["functionResponse"].(map[string]any)
		if fr["name"] != "get_weather" {
			t.Errorf("functionResponse.name=%v, want get_weather（id 反查）", fr["name"])
		}
		if _, hasID := fr["id"]; hasID {
			t.Error("functionResponse 不应残留内部 id 锚点")
		}
		if _, ok := fr["response"].(map[string]any); !ok {
			t.Errorf("response 应是对象: %v", fr["response"])
		}
	}
}

func TestToolCallNameResolution_PositionalFallback(t *testing.T) {
	// 无 id 时按位置兜底：两个 functionResponse 按出现顺序配 [fa, fb]。
	body := map[string]any{
		"model": "m",
		"messages": []any{
			map[string]any{"role": "user", "content": "x"},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"function": map[string]any{"name": "fa", "arguments": "{}"}},
				map[string]any{"function": map[string]any{"name": "fb", "arguments": "{}"}},
			}},
			map[string]any{"role": "tool", "content": "r1"}, // 无 tool_call_id
			map[string]any{"role": "tool", "content": "r2"},
		},
	}
	model, gemini, _ := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig()))
	vars := BuildVertexVariables(model, gemini, config.StaticProvider(config.DefaultConfig()))
	var names []string
	for _, c := range vars["contents"].([]any) {
		cm := c.(map[string]any)
		if cm["role"] == "function" {
			for _, p := range cm["parts"].([]any) {
				fr := p.(map[string]any)["functionResponse"].(map[string]any)
				names = append(names, fr["name"].(string))
			}
		}
	}
	if len(names) != 2 || names[0] != "fa" || names[1] != "fb" {
		t.Errorf("位置兜底 names=%v, want [fa fb]", names)
	}
}

func TestEmptyToolCallGuard(t *testing.T) {
	// 空 name 的 tool_call 应被跳过（守卫）。
	body := map[string]any{
		"model": "m",
		"messages": []any{
			map[string]any{"role": "user", "content": "x"},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "", "arguments": "{}"}},
			}},
		},
	}
	_, gemini, _ := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig()))
	contents := gemini["contents"].([]any)
	for _, c := range contents {
		if cm := c.(map[string]any); cm["role"] == "model" {
			t.Errorf("空 name tool_call 应被跳过，不应产生 model content: %v", cm)
		}
	}
}

// ============ chat 参数映射 ============

func TestReasoningEffort(t *testing.T) {
	for effort, wantLevel := range map[string]string{
		"minimal": "MINIMAL", "low": "LOW", "medium": "MEDIUM", "high": "HIGH",
		"none": "NONE", "xhigh": "HIGH",
	} {
		body := map[string]any{
			"model":            "m",
			"messages":         []any{map[string]any{"role": "user", "content": "hi"}},
			"reasoning_effort": effort,
		}
		_, payload, err := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig()))
		if err != nil {
			t.Fatal(err)
		}
		gc := payload["generationConfig"].(map[string]any)
		tc := gc["thinkingConfig"].(map[string]any)
		if tc["thinkingLevel"] != wantLevel {
			t.Errorf("reasoning_effort=%q → thinkingLevel=%v, want %v", effort, tc["thinkingLevel"], wantLevel)
		}
	}
}

func TestMediaResolution(t *testing.T) {
	for in, want := range map[string]string{
		"high":                    "MEDIA_RESOLUTION_HIGH",
		"low":                     "MEDIA_RESOLUTION_LOW",
		"MEDIA_RESOLUTION_MEDIUM": "MEDIA_RESOLUTION_MEDIUM",
	} {
		body := map[string]any{
			"model":            "m",
			"messages":         []any{map[string]any{"role": "user", "content": "hi"}},
			"media_resolution": in,
		}
		_, payload, _ := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig()))
		gc := payload["generationConfig"].(map[string]any)
		if gc["mediaResolution"] != want {
			t.Errorf("media_resolution=%q → %v, want %v", in, gc["mediaResolution"], want)
		}
	}
	// 嵌在 extra_body
	body := map[string]any{
		"model":      "m",
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
		"extra_body": map[string]any{"media_resolution": "low"},
	}
	_, payload, _ := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig()))
	if payload["generationConfig"].(map[string]any)["mediaResolution"] != "MEDIA_RESOLUTION_LOW" {
		t.Error("extra_body.media_resolution 未生效")
	}
}

func TestLogprobsAndSampling(t *testing.T) {
	body := map[string]any{
		"model":             "m",
		"messages":          []any{map[string]any{"role": "user", "content": "hi"}},
		"temperature":       0.5,
		"top_p":             0.9,
		"top_k":             float64(40),
		"seed":              float64(123),
		"logprobs":          true,
		"top_logprobs":      float64(5),
		"presence_penalty":  0.1,
		"frequency_penalty": 0.2,
	}
	_, payload, _ := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig()))
	gc := payload["generationConfig"].(map[string]any)
	checks := map[string]any{
		"temperature": 0.5, "topP": 0.9, "topK": float64(40), "seed": float64(123),
		"responseLogprobs": true, "logprobs": float64(5),
		"presencePenalty": 0.1, "frequencyPenalty": 0.2,
	}
	for k, want := range checks {
		if gc[k] != want {
			t.Errorf("genCfg[%q]=%v, want %v", k, gc[k], want)
		}
	}
}

func TestTopKClamp(t *testing.T) {
	// topK > 63 应在 BuildVertexVariables 的 generationConfig 转换里被 clamp 到 63。
	payload := map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}},
		"generationConfig": map[string]any{"topK": float64(100)},
	}
	vars := BuildVertexVariables("m", payload, config.StaticProvider(config.DefaultConfig()))
	gc := vars["generationConfig"].(map[string]any)
	if gc["topK"] != 63 {
		t.Errorf("topK=%v, want clamp 到 63", gc["topK"])
	}
}

func TestParallelToolCalls_GracefullyAccepted(t *testing.T) {
	// parallel_tool_calls 应被优雅接受、不报错、不影响 payload。
	body := map[string]any{
		"model":               "m",
		"messages":            []any{map[string]any{"role": "user", "content": "hi"}},
		"parallel_tool_calls": false,
	}
	if _, _, err := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig())); err != nil {
		t.Errorf("parallel_tool_calls 不应报错: %v", err)
	}
}

func TestSafetySettingsPassthrough(t *testing.T) {
	custom := []any{map[string]any{"category": "HARM_CATEGORY_HARASSMENT", "threshold": "BLOCK_LOW_AND_ABOVE"}}
	body := map[string]any{
		"model":           "m",
		"messages":        []any{map[string]any{"role": "user", "content": "hi"}},
		"safety_settings": custom,
	}
	model, gemini, _ := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig()))
	vars := BuildVertexVariables(model, gemini, config.StaticProvider(config.DefaultConfig()))
	ss := vars["safetySettings"].([]any)
	if len(ss) != 1 || ss[0].(map[string]any)["threshold"] != "BLOCK_LOW_AND_ABOVE" {
		t.Errorf("自定义 safety_settings 应透传、不被默认覆盖: %v", ss)
	}
}

// ============ usage 聚合 ============

func TestConvertUsage_Detailed(t *testing.T) {
	meta := map[string]any{
		"promptTokenCount":        float64(10),
		"toolUsePromptTokenCount": float64(2),
		"candidatesTokenCount":    float64(20),
		"thoughtsTokenCount":      float64(5),
		"totalTokenCount":         float64(37),
		"cachedContentTokenCount": float64(3),
		"promptTokensDetails":     []any{map[string]any{"modality": "AUDIO", "tokenCount": float64(4)}},
		"candidatesTokensDetails": []any{map[string]any{"modality": "IMAGE", "tokenCount": float64(6)}},
	}
	u := ConvertUsage(meta)
	if u["prompt_tokens"] != 12 { // 10 + 2 toolUse
		t.Errorf("prompt_tokens=%v, want 12", u["prompt_tokens"])
	}
	if u["completion_tokens"] != 25 { // 20 + 5 thoughts
		t.Errorf("completion_tokens=%v, want 25", u["completion_tokens"])
	}
	if u["total_tokens"] != 37 {
		t.Errorf("total_tokens=%v, want 37", u["total_tokens"])
	}
	pd := u["prompt_tokens_details"].(map[string]any)
	if pd["cached_tokens"] != 3 || pd["audio_tokens"] != 4 {
		t.Errorf("prompt_tokens_details=%v", pd)
	}
	cd := u["completion_tokens_details"].(map[string]any)
	if cd["reasoning_tokens"] != 5 || cd["image_tokens"] != 6 {
		t.Errorf("completion_tokens_details=%v", cd)
	}
}

func TestGeminiResponsesToOAIJSON_NAggregation(t *testing.T) {
	mk := func(text string, prompt, completion int) map[string]any {
		return map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"parts": []any{map[string]any{"text": text}}, "role": "model"},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{
				"promptTokenCount":     float64(prompt),
				"candidatesTokenCount": float64(completion),
				"totalTokenCount":      float64(prompt + completion),
			},
		}
	}
	resps := []map[string]any{mk("A", 5, 3), mk("B", 5, 4)}
	out := GeminiResponsesToOAIJSON(resps, "m")
	choices := out["choices"].([]any)
	if len(choices) != 2 {
		t.Fatalf("choices=%d, want 2", len(choices))
	}
	if choices[0].(map[string]any)["index"] != 0 || choices[1].(map[string]any)["index"] != 1 {
		t.Error("choice index 应 0,1 递增")
	}
	if choices[1].(map[string]any)["message"].(map[string]any)["content"] != "B" {
		t.Error("第二个 choice 内容错")
	}
	u := out["usage"].(map[string]any)
	if u["prompt_tokens"] != 10 || u["completion_tokens"] != 7 || u["total_tokens"] != 17 {
		t.Errorf("聚合 usage=%v, want prompt10/completion7/total17", u)
	}
}

// ============ 响应：图像 inlineData + 代码块 ============

func TestExtractParts_ImageAndCode(t *testing.T) {
	parts := []any{
		map[string]any{"text": "结果:"},
		map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "XXXX"}},
		map[string]any{"executableCode": map[string]any{"codeLanguage": "PYTHON", "code": "print(1)"}},
		map[string]any{"codeExecutionResult": map[string]any{"output": "1"}},
	}
	text, tools, _ := ExtractParts(parts, false, nil)
	if tools != nil {
		t.Error("无工具调用")
	}
	if !strings.Contains(text, "![image](data:image/png;base64,XXXX)") {
		t.Errorf("图像 markdown 缺失: %q", text)
	}
	if !strings.Contains(text, "```python\nprint(1)\n```") {
		t.Errorf("代码块缺失: %q", text)
	}
	if !strings.Contains(text, "```output\n1\n```") {
		t.Errorf("output 块缺失: %q", text)
	}
}

// ============ 图像分辨率 ApplyImageConfig ============

func TestApplyImageConfig(t *testing.T) {
	flashModel := "gemini-3.1-flash-image"
	liteModel := "gemini-3.1-flash-lite-image"
	proModel := "gemini-3-pro-image"

	// image_size 档位
	gp := map[string]any{}
	ApplyImageConfig(gp, map[string]any{"image_size": "2K"}, flashModel)
	if gp["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)["imageSize"] != "2K" {
		t.Error("image_size=2K 未写入")
	}
	// 像素 → 档位
	gp2 := map[string]any{}
	ApplyImageConfig(gp2, map[string]any{"size": "2048x2048"}, flashModel)
	if _, has := gp2["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)["imageSize"]; has {
		t.Error("size 不应推导 imageSize")
	}
	// imageConfig 顶层透传
	gp3 := map[string]any{}
	ApplyImageConfig(gp3, map[string]any{"imageConfig": map[string]any{"aspectRatio": "16:9"}}, flashModel)
	if gp3["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)["aspectRatio"] != "16:9" {
		t.Error("imageConfig 透传失败")
	}
	// 不命中：不动 payload
	gp4 := map[string]any{}
	ApplyImageConfig(gp4, map[string]any{}, flashModel)
	if len(gp4) != 0 {
		t.Errorf("无分辨率参数时不应改 payload: %v", gp4)
	}
	// size="1024x1792" → aspectRatio="9:16", imageSize 不存在
	gp5 := map[string]any{}
	ApplyImageConfig(gp5, map[string]any{"size": "1024x1792"}, flashModel)
	gc5 := gp5["generationConfig"].(map[string]any)
	ic5 := gc5["imageConfig"].(map[string]any)
	if ic5["aspectRatio"] != "9:16" {
		t.Errorf("aspectRatio=%v，期望 9:16", ic5["aspectRatio"])
	}
	if _, has := ic5["imageSize"]; has {
		t.Error("size 不应推导 imageSize")
	}
	// size="2048x2048" → aspectRatio="1:1", imageSize 不存在
	gp6 := map[string]any{}
	ApplyImageConfig(gp6, map[string]any{"size": "2048x2048"}, flashModel)
	ic6 := gp6["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)
	if ic6["aspectRatio"] != "1:1" {
		t.Errorf("aspectRatio=%v，期望 1:1", ic6["aspectRatio"])
	}
	if _, has := ic6["imageSize"]; has {
		t.Error("size 不应推导 imageSize")
	}
	// size="auto" → 不设 aspectRatio/imageSize
	gp7 := map[string]any{}
	ApplyImageConfig(gp7, map[string]any{"size": "auto"}, flashModel)
	if gc, ok := gp7["generationConfig"].(map[string]any); ok {
		if ic, ok := gc["imageConfig"].(map[string]any); ok {
			if _, has := ic["aspectRatio"]; has {
				t.Error("auto size 不应设 aspectRatio")
			}
			if _, has := ic["imageSize"]; has {
				t.Error("auto size 不应设 imageSize")
			}
		}
	}
	// lite-image：size="2048x2048" → aspectRatio="1:1"（支持），imageSize 不写入（size 不推导 imageSize）
	gp8 := map[string]any{}
	ApplyImageConfig(gp8, map[string]any{"size": "2048x2048"}, liteModel)
	ic8 := gp8["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)
	if ic8["aspectRatio"] != "1:1" {
		t.Errorf("aspectRatio=%v，期望 1:1", ic8["aspectRatio"])
	}
	if _, has := ic8["imageSize"]; has {
		t.Error("lite-image 的 2K 应被过滤丢弃")
	}
	// lite-image：image_size="4K" → imageSize 不写入（4K 被过滤）
	gp9 := map[string]any{}
	ApplyImageConfig(gp9, map[string]any{"image_size": "4K"}, liteModel)
	if gc, ok := gp9["generationConfig"].(map[string]any); ok {
		if _, ok := gc["imageConfig"]; ok {
			t.Error("lite-image 的 4K 应被过滤丢弃，不应设 imageConfig")
		}
	}
	// pro-image：imageConfig.aspectRatio="auto" → passthrough 时 auto 被过滤丢弃
	gp10 := map[string]any{}
	ApplyImageConfig(gp10, map[string]any{"imageConfig": map[string]any{"aspectRatio": "auto"}}, proModel)
	if gc10, ok := gp10["generationConfig"].(map[string]any); ok {
		if ic10, ok := gc10["imageConfig"].(map[string]any); ok {
			if _, has := ic10["aspectRatio"]; has {
				t.Error("pro-image 不支持 auto，应被过滤")
			}
		}
	}
	// flash-image：imageConfig.aspectRatio="16:9" → passthrough 保留
	gp11 := map[string]any{}
	ApplyImageConfig(gp11, map[string]any{"imageConfig": map[string]any{"aspectRatio": "16:9"}}, flashModel)
	ic11 := gp11["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)
	if ic11["aspectRatio"] != "16:9" {
		t.Errorf("aspectRatio=%v，期望 16:9", ic11["aspectRatio"])
	}
	// flash-image：imageConfig.imageSize="8K" → passthrough 被过滤丢弃
	gp12 := map[string]any{}
	ApplyImageConfig(gp12, map[string]any{"imageConfig": map[string]any{"imageSize": "8K"}}, flashModel)
	if gc12, ok := gp12["generationConfig"].(map[string]any); ok {
		if ic12, ok := gc12["imageConfig"].(map[string]any); ok {
			if _, has := ic12["imageSize"]; has {
				t.Error("8K 不应被透传")
			}
		}
	}
}

// ============ 工具 schema → Vertex 原生格式 ============

func TestToNativeSchema(t *testing.T) {
	// 标准 JSON Schema → 原生：type 大写、properties 转 [{key,value}]、剥离 $schema。
	std := map[string]any{
		"type":    "object",
		"$schema": "http://json-schema.org/draft-07/schema#",
		"properties": map[string]any{
			"city": map[string]any{"type": "string", "description": "城市"},
		},
		"required": []any{"city"},
	}
	native := toNativeSchema(std).(map[string]any)
	if native["type"] != "OBJECT" {
		t.Errorf("type=%v, want OBJECT（大写）", native["type"])
	}
	if _, ok := native["$schema"]; ok {
		t.Error("$schema 应被剥离")
	}
	props, ok := native["properties"].([]any)
	if !ok || len(props) != 1 {
		t.Fatalf("properties 应转为 [{key,value}] 列表: %v", native["properties"])
	}
	p0 := props[0].(map[string]any)
	if p0["key"] != "city" {
		t.Errorf("property key=%v", p0["key"])
	}
	if p0["value"].(map[string]any)["type"] != "STRING" {
		t.Errorf("嵌套 type 应大写: %v", p0["value"])
	}
}

func TestConvertToolsFormat_NativeParameters(t *testing.T) {
	// 经 BuildVertexVariables 的 tools 归一后，parameters 应是原生格式（type 大写、properties 列表）。
	body := map[string]any{
		"model":    "m",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "f", "parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"x": map[string]any{"type": "integer"}},
			},
		}}},
	}
	model, gemini, _ := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig()))
	vars := BuildVertexVariables(model, gemini, config.StaticProvider(config.DefaultConfig()))
	tools := vars["tools"].([]any)
	decl := tools[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	params := decl["parameters"].(map[string]any)
	if params["type"] != "OBJECT" {
		t.Errorf("归一后 parameters.type=%v, want OBJECT", params["type"])
	}
	if _, ok := params["properties"].([]any); !ok {
		t.Errorf("归一后 properties 应是列表: %v", params["properties"])
	}
}

// ============ 并行工具响应合并 ============

func TestParallelToolResponses_Coalesced(t *testing.T) {
	// OpenAI 把每个 tool 结果拆成独立 message；应合并进同一个 function content
	// （Gemini 要求并行调用 turn 的 functionResponse 数 = functionCall 数，在同一 content）。
	body := map[string]any{
		"model": "m",
		"messages": []any{
			map[string]any{"role": "user", "content": "x"},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "a", "function": map[string]any{"name": "f1", "arguments": "{}"}},
				map[string]any{"id": "b", "function": map[string]any{"name": "f2", "arguments": "{}"}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "b", "content": "rb"},
			map[string]any{"role": "tool", "tool_call_id": "a", "content": "ra"},
		},
	}
	model, gemini, _ := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig()))
	vars := BuildVertexVariables(model, gemini, config.StaticProvider(config.DefaultConfig()))
	var funcContents int
	var funcResponseParts int
	for _, c := range vars["contents"].([]any) {
		cm := c.(map[string]any)
		if cm["role"] == "function" {
			funcContents++
			funcResponseParts += len(cm["parts"].([]any))
		}
	}
	if funcContents != 1 {
		t.Errorf("并行响应应合并进 1 个 function content，实际 %d 个", funcContents)
	}
	if funcResponseParts != 2 {
		t.Errorf("function content 应含 2 个 functionResponse part，实际 %d", funcResponseParts)
	}
}
