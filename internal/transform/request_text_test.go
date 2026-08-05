package transform

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

func TestConvertChatRequest_PlainText(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	body := map[string]any{
		"model": "gemini-3.1-flash",
		"messages": []any{
			map[string]any{"role": "system", "content": "你是助手"},
			map[string]any{"role": "user", "content": "你好"},
		},
		"temperature": 0.7,
		"max_tokens":  float64(100),
	}
	model, payload, err := ConvertChatRequest(body, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if model != "gemini-3.1-flash" {
		t.Errorf("model=%q", model)
	}
	contents, _ := payload["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents len=%d, want 1", len(contents))
	}
	c0 := contents[0].(map[string]any)
	if c0["role"] != "user" {
		t.Errorf("role=%v, want user", c0["role"])
	}
	if c0["parts"].([]any)[0].(map[string]any)["text"] != "你好" {
		t.Errorf("user text mismatch")
	}
	si, ok := payload["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatal("missing systemInstruction")
	}
	if si["parts"].([]any)[0].(map[string]any)["text"] != "你是助手" {
		t.Error("system text mismatch")
	}
	gc := payload["generationConfig"].(map[string]any)
	if gc["temperature"] != 0.7 {
		t.Errorf("temperature=%v", gc["temperature"])
	}
	if gc["maxOutputTokens"] != float64(100) {
		t.Errorf("maxOutputTokens=%v", gc["maxOutputTokens"])
	}
}

func TestConvertChatRequest_EmptyMessages(t *testing.T) {
	_, _, err := ConvertChatRequest(map[string]any{"model": "m", "messages": []any{}}, config.StaticProvider(config.DefaultConfig()))
	if err == nil {
		t.Error("expected error for empty messages")
	}
}

func TestConvertChatRequest_MaxTokensInvalid(t *testing.T) {
	body := map[string]any{
		"model":      "m",
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
		"max_tokens": float64(0),
	}
	if _, _, err := ConvertChatRequest(body, config.StaticProvider(config.DefaultConfig())); err == nil {
		t.Error("expected error for max_tokens=0")
	}
}

func TestBuildVertexVariables_SafetyDefault(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	payload := map[string]any{"contents": []any{
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
	}}
	vars := BuildVertexVariables("gemini-3.1-flash", payload, cfg)
	if vars["model"] != "gemini-3.1-flash" {
		t.Error("model")
	}
	ss, ok := vars["safetySettings"].([]any)
	if !ok || len(ss) != 6 {
		t.Errorf("safetySettings=%v, want 6 BLOCK_NONE", vars["safetySettings"])
	}
	first := ss[0].(map[string]any)
	if first["threshold"] != "BLOCK_NONE" {
		t.Errorf("threshold=%v", first["threshold"])
	}
}

func TestBuildVertexVariables_SystemDemote(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	payload := map[string]any{
		"contents":          []any{},
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": "sys"}}},
	}
	vars := BuildVertexVariables("m", payload, cfg)
	if _, ok := vars["systemInstruction"]; ok {
		t.Error("systemInstruction 应在无 user 时被降级删除")
	}
	contents := vars["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents len=%d, want 1", len(contents))
	}
	c0 := contents[0].(map[string]any)
	if c0["role"] != "user" {
		t.Errorf("降级后 role=%v, want user", c0["role"])
	}
}

// TestBuildVertexVariables 测试 BuildVertexVariables 的 produced structure。
func TestBuildVertexVariables(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	geminiPayload := map[string]any{
		"contents": []any{map[string]any{
			"role":  "user",
			"parts": []any{map[string]any{"text": "Hello"}},
		}},
	}

	vars := BuildVertexVariables("gemini-2.5-flash", geminiPayload, cfg)
	if vars == nil {
		t.Fatal("BuildVertexVariables returned nil")
	}
	if vars["model"] != "gemini-2.5-flash" {
		t.Errorf("model=%q", vars["model"])
	}
	if _, ok := vars["contents"]; !ok {
		t.Error("vars missing contents")
	}
}

// TestMatchTrailingFixModel 验证 matchTrailingFixModel 纯精确匹配语义：
// 空格 trim、空值/空清单短路、版本/层级后缀与前缀误伤均不自动命中。
func TestMatchTrailingFixModel(t *testing.T) {
	cases := []struct {
		name    string
		model   string
		entries []string
		want    bool
	}{
		{"精确命中", "gemini-3.6-flash", []string{"gemini-3.6-flash"}, true},
		{"版本后缀不命中", "gemini-3.6-flash-001", []string{"gemini-3.6-flash"}, false},
		{"层级变体不命中", "gemini-3.6-flash-lite", []string{"gemini-3.6-flash"}, false},
		{"前缀误伤屏蔽", "gemini-3.6-flashback", []string{"gemini-3.6-flash"}, false},
		{"空模型名", "", []string{"gemini-3.6-flash"}, false},
		{"空清单", "gemini-3.6-flash", nil, false},
		{"带首尾空格命中", " gemini-3.6-flash ", []string{"gemini-3.6-flash"}, true},
		{"清单项带空格命中", "gemini-3.6-flash", []string{" gemini-3.6-flash "}, true},
		{"多条目第二项命中", "gemini-3.6-flash-lite", []string{"gemini-3.5-flash-lite", "gemini-3.6-flash-lite"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchTrailingFixModel(c.model, c.entries); got != c.want {
				t.Errorf("matchTrailingFixModel(%q, %v)=%v, want %v", c.model, c.entries, got, c.want)
			}
		})
	}
}

func TestBuildVertexVariables_TrailingModelFix(t *testing.T) {
	// 场景1: toggle ON + 命中模型 + 纯 functionResponse → role 修正 + 追加
	t.Run("命中模型+纯functionResponse→role修正+追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "天气如何"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"city": "北京"}}}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "get_weather", "response": map[string]any{"temp": 22}}}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		// 第3项 role→function，末尾含 functionResponse part → 视为 model turn 未闭合，追加 → 4 项
		if len(contents) != 4 {
			t.Fatalf("len(contents)=%d, want 4 (3项+追加)", len(contents))
		}
		third := contents[2].(map[string]any)
		if third["role"] != "function" {
			t.Errorf("third content role=%q, want function", third["role"])
		}
		last := contents[3].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
		lastParts := last["parts"].([]any)
		if len(lastParts) != 1 {
			t.Fatalf("last parts len=%d, want 1", len(lastParts))
		}
		if text, _ := lastParts[0].(map[string]any)["text"].(string); text != "继续" {
			t.Errorf("last parts[0].text=%q, want 继续", text)
		}
	})

	// 场景2: toggle ON + 命中模型 + 末尾 model → 无条件追加
	t.Run("命中模型+末尾model→无条件追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 3 {
			t.Fatalf("len(contents)=%d, want 3 (2项+追加)", len(contents))
		}
		last := contents[2].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
	})

	// 场景3: toggle ON + 命中模型 + 末尾 user → 不追加（行为翻转）
	t.Run("命中模型+末尾user→不追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}},
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "继续"}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		// 末尾为 user → 条件不匹配，不追加
		if len(contents) != 3 {
			t.Fatalf("len(contents)=%d, want 3 (不追加)", len(contents))
		}
		last := contents[2].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
	})

	// 场景4: toggle ON + 不命中模型 → 不做任何修改
	t.Run("不命中模型→不做任何修改", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}},
			},
		}
		vars := BuildVertexVariables("gemini-2.5-flash", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 2 {
			t.Errorf("len(contents)=%d, want 2 (不做修改)", len(contents))
		}
	})

	// 场景5: toggle OFF + 命中模型 → 不做任何修改
	t.Run("开关关闭+命中模型→不做修改", func(t *testing.T) {
		cfg := config.StaticProvider(config.DefaultConfig())
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 2 {
			t.Errorf("len(contents)=%d, want 2 (不做修改)", len(contents))
		}
	})

	// 场景6: toggle ON + 版本后缀 → 纯精确匹配不命中 → 不追加
	t.Run("版本后缀不自动命中→不追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash-001", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 2 {
			t.Errorf("len(contents)=%d, want 2 (纯精确匹配下版本后缀不自动命中→不追加)", len(contents))
		}
	})

	// 场景6b: toggle ON + 不在清单中的模型 → 即使开关开启也不追加
	t.Run("不在清单中的模型→不追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}},
			},
		}
		vars := BuildVertexVariables("gemini-1.5-pro", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 2 {
			t.Errorf("len(contents)=%d, want 2 (清单外模型不追加)", len(contents))
		}
	})

	// 场景7: toggle ON + 空 contents → 不 panic
	t.Run("空contents→不panic", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 0 {
			t.Errorf("len(contents)=%d, want 0", len(contents))
		}
	})

	// 场景8: toggle ON + 命中模型 + 末尾 function → 追加
	t.Run("命中模型+末尾function→追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "天气如何"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "get_weather"}}}},
				map[string]any{"role": "function", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "get_weather", "response": map[string]any{"temp": 22}}}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		// 末尾 role="function" 但含 functionResponse part → 视为 model turn 未闭合，追加 → 4 项
		if len(contents) != 4 {
			t.Fatalf("len(contents)=%d, want 4 (3项+追加)", len(contents))
		}
		last := contents[3].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
	})

	// 场景9: toggle ON + 混合 parts（text+functionResponse）末尾 role 仍为 model → 追加
	t.Run("命中模型+混合parts末尾model→追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{
					map[string]any{"text": "结果如下"},
					map[string]any{"functionResponse": map[string]any{"name": "a", "response": map[string]any{"v": 1}}},
				}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		// 混合 parts 不被 normalizeFunctionResponseRoles 修正，role 仍为 model → 条件追加
		if len(contents) != 3 {
			t.Fatalf("len(contents)=%d, want 3 (2项+追加)", len(contents))
		}
		last := contents[2].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
	})

	// 场景10: toggle ON + 真实 bug 复现（OpenCode"继续"场景，user/model 交替，与生产日志结构一致）→ 末尾 user 含 functionResponse → 追加
	t.Run("命中模型+user含functionResponse→追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "这里是 win环境"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "glob", "args": map[string]any{"pattern": "**/*"}}}}},
				map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "glob", "response": map[string]any{"content": "a.txt"}}}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "read"}}}},
				map[string]any{"role": "user", "parts": []any{
					map[string]any{"functionResponse": map[string]any{"name": "read", "response": map[string]any{"content": "b.txt"}}},
					map[string]any{"text": "继续"},
				}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		// 最后一条 role=user 但含 functionResponse part → 必须追加
		if len(contents) != 6 {
			t.Fatalf("len(contents)=%d, want 6 (5项+追加)", len(contents))
		}
		last := contents[5].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
		lastParts := last["parts"].([]any)
		if len(lastParts) != 1 {
			t.Fatalf("last parts len=%d, want 1 (纯文本)", len(lastParts))
		}
	})

	// 场景11: toggle ON + 纯 functionResponse 结尾（路径A：中断后无用户文本）→ 追加
	t.Run("命中模型+纯functionResponse末尾→追加", func(t *testing.T) {
		cfg := config.StaticProvider(config.AppConfig{TrailingModelFixEnabled: true, TrailingFixModels: []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"}})
		payload := map[string]any{
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
				map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "read"}}}},
				map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "read", "response": map[string]any{"content": "x"}}}}},
			},
		}
		vars := BuildVertexVariables("gemini-3.6-flash", payload, cfg)
		contents := vars["contents"].([]any)
		if len(contents) != 4 {
			t.Fatalf("len(contents)=%d, want 4 (3项+追加)", len(contents))
		}
		last := contents[3].(map[string]any)
		if last["role"] != "user" {
			t.Errorf("last content role=%q, want user", last["role"])
		}
	})
}

// TestMarshalRoundTrip 验证 ConvertChatRequest + BuildVertexVariables 的 JSON 可序列化。
func TestMarshalRoundTrip(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	body := map[string]any{
		"model":    "gemini-2.5-flash",
		"messages": []any{map[string]any{"role": "user", "content": "Hello"}},
	}
	model, geminiPayload, err := ConvertChatRequest(body, cfg)
	if err != nil {
		t.Fatalf("ConvertChatRequest: %v", err)
	}
	payload := BuildVertexVariables(model, geminiPayload, cfg)

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty marshal result")
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["model"] != "gemini-2.5-flash" {
		t.Errorf("model mismatch after round-trip")
	}
}

// TestMergeContiguousRoles 验证相邻同 role content 被合并。
func TestMergeContiguousRoles(t *testing.T) {
	contents := []any{
		map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hi"}}},
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": "q1"}}},
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": "q2"}}},
		map[string]any{"role": "model", "parts": []any{map[string]any{"text": "bye"}}},
	}
	merged := mergeContiguousRoles(contents).([]any)
	if len(merged) != 3 {
		t.Fatalf("应合并为 3 个 content，实际 %d", len(merged))
	}
	user := merged[1].(map[string]any)
	if parts := user["parts"].([]any); len(parts) != 2 {
		t.Errorf("user content 应有 2 个 parts，实际 %d", len(parts))
	}
}

// TestMergeContiguousRoles_FunctionResponse 合并多轮工具结果（连续 function 角色）。
func TestMergeContiguousRoles_FunctionResponse(t *testing.T) {
	contents := []any{
		map[string]any{"role": "model", "parts": []any{
			map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"city": "NY"}}},
			map[string]any{"functionCall": map[string]any{"name": "get_time", "args": map[string]any{"tz": "EST"}}},
		}},
		map[string]any{"role": "function", "parts": []any{
			map[string]any{"functionResponse": map[string]any{"name": "get_weather", "response": map[string]any{"temp": 20}}},
		}},
		map[string]any{"role": "function", "parts": []any{
			map[string]any{"functionResponse": map[string]any{"name": "get_time", "response": map[string]any{"time": "10:00"}}},
		}},
		map[string]any{"role": "model", "parts": []any{map[string]any{"text": "done"}}},
	}
	merged := mergeContiguousRoles(contents).([]any)
	if len(merged) != 3 {
		t.Fatalf("应合并为 3 个 content，实际 %d", len(merged))
	}
	funcContent := merged[1].(map[string]any)
	if parts := funcContent["parts"].([]any); len(parts) != 2 {
		t.Errorf("function content 应合并两个 functionResponse 为 2 个 parts，实际 %d", len(parts))
	}
}

func TestNormalizeFunctionResponseRoles(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{
			name: "纯 functionResponse 修正 role",
			in:   []any{map[string]any{"role": "model", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "get_weather"}}}}},
			want: []any{map[string]any{"role": "function", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "get_weather"}}}}},
		},
		{
			name: "纯 functionResponse 多个 part",
			in: []any{map[string]any{"role": "model", "parts": []any{
				map[string]any{"functionResponse": map[string]any{"name": "a"}},
				map[string]any{"functionResponse": map[string]any{"name": "b"}},
			}}},
			want: []any{map[string]any{"role": "function", "parts": []any{
				map[string]any{"functionResponse": map[string]any{"name": "a"}},
				map[string]any{"functionResponse": map[string]any{"name": "b"}},
			}}},
		},
		{
			name: "混合 text + functionResponse 不改",
			in: []any{map[string]any{"role": "model", "parts": []any{
				map[string]any{"text": "hi"},
				map[string]any{"functionResponse": map[string]any{"name": "a"}},
			}}},
			want: []any{map[string]any{"role": "model", "parts": []any{
				map[string]any{"text": "hi"},
				map[string]any{"functionResponse": map[string]any{"name": "a"}},
			}}},
		},
		{
			name: "纯 text 不改",
			in:   []any{map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hi"}}}},
			want: []any{map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hi"}}}},
		},
		{
			name: "纯 functionCall 不改",
			in:   []any{map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "get_weather"}}}}},
			want: []any{map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "get_weather"}}}}},
		},
		{
			name: "role 已是 function 不改",
			in:   []any{map[string]any{"role": "function", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "a"}}}}},
			want: []any{map[string]any{"role": "function", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "a"}}}}},
		},
		{
			name: "空 parts 不改",
			in:   []any{map[string]any{"role": "model", "parts": []any{}}},
			want: []any{map[string]any{"role": "model", "parts": []any{}}},
		},
		{
			name: "非 map element 保留",
			in:   []any{"string-elem", map[string]any{"role": "model", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "a"}}}}},
			want: []any{"string-elem", map[string]any{"role": "function", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "a"}}}}},
		},
		{
			name: "contents 不是数组",
			in:   "not an array",
			want: "not an array",
		},
		{
			name: "nil contents",
			in:   nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeFunctionResponseRoles(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("normalizeFunctionResponseRoles() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestEndsWithModelTurn(t *testing.T) {
	cases := []struct {
		name string
		c    map[string]any
		want bool
	}{
		{"纯文本user", map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}, false},
		{"user含functionResponse", map[string]any{"role": "user", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "a"}}}}, true},
		{"user含functionCall", map[string]any{"role": "user", "parts": []any{map[string]any{"functionCall": map[string]any{"name": "a"}}}}, true},
		{"user混合parts", map[string]any{"role": "user", "parts": []any{map[string]any{"text": "继续"}, map[string]any{"functionResponse": map[string]any{"name": "a"}}}}, true},
		{"model文本", map[string]any{"role": "model", "parts": []any{map[string]any{"text": "hello"}}}, true},
		{"function角色", map[string]any{"role": "function", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "a"}}}}, true},
		{"system文本", map[string]any{"role": "system", "parts": []any{map[string]any{"text": "sys"}}}, false},
		{"空parts", map[string]any{"role": "user", "parts": []any{}}, false},
		{"role缺失", map[string]any{"parts": []any{map[string]any{"text": "hi"}}}, true},
		{"非map part", map[string]any{"role": "user", "parts": []any{"not-a-map"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := endsWithModelTurn(tc.c); got != tc.want {
				t.Errorf("endsWithModelTurn=%v, want %v", got, tc.want)
			}
		})
	}
}
