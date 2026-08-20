package transform

import (
	"testing"
)

func TestBuildGeminiVariables_WebSearch(t *testing.T) {
	// 测试场景 1：传入带有 GoogleSearch 的 Tool → 追加绑定 googleSearch + googleMaps 双工具
	geminiReq := &GeminiRequest{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
		Tools: []Tool{
			{GoogleSearch: &GoogleSearch{}},
		},
	}

	if len(geminiReq.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(geminiReq.Tools))
	}

	vars := BuildGeminiVariables("gemini-3.6-flash", geminiReq, nil)
	tools, ok := vars["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected 2 tools (googleSearch + googleMaps) in vars, got %v", vars["tools"])
	}

	toolMap, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool is not a map: %v", tools[0])
	}

	if _, ok := toolMap["googleSearch"]; !ok {
		t.Errorf("expected googleSearch in toolMap, got %v", toolMap)
	}

	// 第二个工具必须是 googleMaps
	toolMap2, ok := tools[1].(map[string]any)
	if !ok {
		t.Fatalf("second tool is not a map: %v", tools[1])
	}
	if _, ok := toolMap2["googleMaps"]; !ok {
		t.Errorf("expected googleMaps in second tool, got %v", toolMap2)
	}
}

func TestBuildGeminiVariables_WebSearchWithFunction(t *testing.T) {
	// 测试场景 2：同时传入 function 声明与 googleSearch → 追加共存，函数声明不丢失
	geminiReq := &GeminiRequest{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
		Tools: []Tool{
			{
				GoogleSearch: &GoogleSearch{},
				FunctionDeclarations: []FunctionDeclaration{
					{
						Name:        "get_weather",
						Description: "get weather",
						Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
					},
				},
			},
		},
	}

	if len(geminiReq.Tools) != 1 {
		t.Fatalf("expected 1 combined tool, got %d", len(geminiReq.Tools))
	}

	vars := BuildGeminiVariables("gemini-3.6-flash", geminiReq, nil)
	tools, ok := vars["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected 2 tools (function+search 共存, 追加 maps) in vars, got %v", vars["tools"])
	}

	toolMap, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool is not a map: %v", tools[0])
	}

	if _, ok := toolMap["googleSearch"]; !ok {
		t.Errorf("expected googleSearch in toolMap, got %v", toolMap)
	}
	if _, ok := toolMap["functionDeclarations"]; !ok {
		t.Errorf("expected functionDeclarations in toolMap, got %v", toolMap)
	}

	// 第二个工具必须是 googleMaps（追加项不含其它字段）
	toolMap2, ok := tools[1].(map[string]any)
	if !ok {
		t.Fatalf("second tool is not a map: %v", tools[1])
	}
	if _, ok := toolMap2["googleMaps"]; !ok {
		t.Errorf("expected googleMaps in second tool, got %v", toolMap2)
	}
	if _, ok := toolMap2["functionDeclarations"]; ok {
		t.Errorf("appended tool must not carry functionDeclarations, got %v", toolMap2)
	}
}

func TestBindSearchAndMapsTools(t *testing.T) {
	cases := []struct {
		name     string
		tools    []Tool
		wantLen  int
		wantGS   bool
		wantGM   bool
		wantFunc bool
	}{
		{
			name:    "only googleMaps triggers dual binding",
			tools:   []Tool{{GoogleMaps: &GoogleMaps{}}},
			wantLen: 2, wantGS: true, wantGM: true,
		},
		{
			name:    "functionDeclarations only stays untouched",
			tools:   []Tool{{FunctionDeclarations: []FunctionDeclaration{{Name: "f1"}}}},
			wantLen: 1, wantGS: false, wantGM: false, wantFunc: true,
		},
		{
			name:    "googleSearch and googleMaps already present appends nothing",
			tools:   []Tool{{GoogleSearch: &GoogleSearch{}}, {GoogleMaps: &GoogleMaps{}}},
			wantLen: 2, wantGS: true, wantGM: true,
		},
		{
			name:    "duplicate googleSearch tools folded to single marker",
			tools:   []Tool{{GoogleSearch: &GoogleSearch{}}, {GoogleSearch: &GoogleSearch{}}},
			wantLen: 2, wantGS: true, wantGM: true,
		},
		{
			name:    "duplicate googleMaps tools folded to single marker",
			tools:   []Tool{{GoogleMaps: &GoogleMaps{}}, {GoogleMaps: &GoogleMaps{}}},
			wantLen: 2, wantGS: true, wantGM: true,
		},
		{
			name:    "duplicate search tool keeps functionDeclarations of first",
			tools:   []Tool{{GoogleSearch: &GoogleSearch{}, FunctionDeclarations: []FunctionDeclaration{{Name: "f1"}}}, {GoogleSearch: &GoogleSearch{}}},
			wantLen: 2, wantGS: true, wantGM: true, wantFunc: true,
		},
		{
			name:    "googleSearchRetrieval alone does not trigger binding",
			tools:   []Tool{{GoogleSearchRetrieval: &GoogleSearch{}}},
			wantLen: 1, wantGS: false, wantGM: false,
		},
		{
			name:    "empty tools returns unchanged",
			tools:   nil,
			wantLen: 0, wantGS: false, wantGM: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := bindSearchAndMapsTools(c.tools)
			if len(out) != c.wantLen {
				t.Fatalf("expected %d tools, got %d: %+v", c.wantLen, len(out), out)
			}
			gs, gm, fn := false, false, false
			for _, tool := range out {
				if tool.GoogleSearch != nil {
					gs = true
				}
				if tool.GoogleMaps != nil {
					gm = true
				}
				if len(tool.FunctionDeclarations) > 0 {
					fn = true
				}
			}
			if gs != c.wantGS || gm != c.wantGM {
				t.Errorf("googleSearch=%v googleMaps=%v, want gs=%v gm=%v", gs, gm, c.wantGS, c.wantGM)
			}
			if fn != c.wantFunc {
				t.Errorf("functionDeclarations=%v, want %v", fn, c.wantFunc)
			}
		})
	}
}

func TestApplyTextSamplingParams(t *testing.T) {
	specSupported := TextSpecFor("gemini-3.5-flash")
	specUnsupported := TextSpecFor("gemini-3.7-flash")

	cases := []struct {
		name    string
		gc      *GenerationConfig
		spec    TextModelSpec
		dropMax bool
		want    func(*GenerationConfig) bool
	}{
		{
			name: "nil gc injects defaults for supported model",
			spec: specSupported,
			want: func(gc *GenerationConfig) bool {
				return gc != nil &&
					gc.MaxOutputTokens != nil && *gc.MaxOutputTokens == 65535 &&
					gc.Temperature != nil && *gc.Temperature == 1.0 &&
					gc.TopP != nil && *gc.TopP == 0.95
			},
		},
		{
			name: "unsupported model forces temperature and topP nil",
			gc:   &GenerationConfig{Temperature: f64ptr(0.5), TopP: f64ptr(0.7), MaxOutputTokens: intPtr(1000)},
			spec: specUnsupported,
			want: func(gc *GenerationConfig) bool {
				return gc.Temperature == nil && gc.TopP == nil &&
					gc.MaxOutputTokens != nil && *gc.MaxOutputTokens == 1000
			},
		},
		{
			name: "temperature clamped to spec max",
			gc:   &GenerationConfig{Temperature: f64ptr(2.5)},
			spec: specSupported,
			want: func(gc *GenerationConfig) bool {
				return gc.Temperature != nil && *gc.Temperature == 2.0
			},
		},
		{
			name: "topP clamped to 1.0",
			gc:   &GenerationConfig{TopP: f64ptr(1.2)},
			spec: specSupported,
			want: func(gc *GenerationConfig) bool {
				return gc.TopP != nil && *gc.TopP == 1.0
			},
		},
		{
			name: "maxOutputTokens clamped to spec",
			gc:   &GenerationConfig{MaxOutputTokens: intPtr(100000)},
			spec: specSupported,
			want: func(gc *GenerationConfig) bool {
				return gc.MaxOutputTokens != nil && *gc.MaxOutputTokens == 65535
			},
		},
		{
			name:    "drop_max_tokens clears maxOutputTokens but keeps sampling",
			gc:      &GenerationConfig{MaxOutputTokens: intPtr(100)},
			spec:    specSupported,
			dropMax: true,
			want: func(gc *GenerationConfig) bool {
				return gc.MaxOutputTokens == nil &&
					gc.Temperature != nil && *gc.Temperature == 1.0
			},
		},
		{
			name: "2.5-flash topP default is 1.0",
			spec: TextSpecFor("gemini-2.5-flash"),
			want: func(gc *GenerationConfig) bool {
				return gc.TopP != nil && *gc.TopP == 1.0
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &mockConfigProvider{dropMax: c.dropMax}
			out := applyTextSamplingParams(c.gc, c.spec, cfg)
			if !c.want(out) {
				t.Errorf("applyTextSamplingParams result mismatch: %+v", out)
			}
		})
	}
}

func intPtr(v int) *int { return &v }
