package transform

import (
	"testing"
)

func TestBuildGeminiVariablesTyped_WebSearch(t *testing.T) {
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

	vars := BuildGeminiVariablesTyped("gemini-3.6-flash", geminiReq, nil)
	if vars == nil || len(vars.Tools) != 2 {
		t.Fatalf("expected 2 tools (googleSearch + googleMaps) in vars, got %v", vars.Tools)
	}

	if vars.Tools[0].GoogleSearch == nil {
		t.Errorf("expected googleSearch in first tool, got %+v", vars.Tools[0])
	}

	// 第二个工具必须是 googleMaps
	if vars.Tools[1].GoogleMaps == nil {
		t.Errorf("expected googleMaps in second tool, got %+v", vars.Tools[1])
	}
}

func TestBuildGeminiVariablesTyped_WebSearchWithFunction(t *testing.T) {
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

	vars := BuildGeminiVariablesTyped("gemini-3.6-flash", geminiReq, nil)
	if vars == nil || len(vars.Tools) != 2 {
		t.Fatalf("expected 2 tools (function+search 共存, 追加 maps) in vars, got %v", vars.Tools)
	}

	if vars.Tools[0].GoogleSearch == nil {
		t.Errorf("expected googleSearch in tool[0], got %+v", vars.Tools[0])
	}
	if len(vars.Tools[0].FunctionDeclarations) != 1 {
		t.Errorf("expected functionDeclarations in tool[0], got %+v", vars.Tools[0])
	}

	// 第二个工具必须是 googleMaps（追加项不含其它字段）
	if vars.Tools[1].GoogleMaps == nil {
		t.Errorf("expected googleMaps in second tool, got %+v", vars.Tools[1])
	}
	if len(vars.Tools[1].FunctionDeclarations) != 0 {
		t.Errorf("appended tool must not carry functionDeclarations, got %+v", vars.Tools[1])
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

func TestBuildGeminiVariablesTyped_WebSearch_SnakeCase(t *testing.T) {
	t.Run("snake_case google_search triggers dual binding", func(t *testing.T) {
		geminiReq := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
			Tools:    []Tool{{GoogleSearch: map[string]any{}}},
		}
		vars := BuildGeminiVariablesTyped("gemini-3.6-flash", geminiReq, nil)
		if vars == nil || len(vars.Tools) != 2 {
			t.Fatalf("expected 2 tools (googleSearch + googleMaps), got %v", vars.Tools)
		}
		if vars.Tools[0].GoogleSearch == nil {
			t.Errorf("expected googleSearch in first tool")
		}
		if vars.Tools[1].GoogleMaps == nil {
			t.Errorf("expected googleMaps in second tool")
		}
	})

	t.Run("empty tool filtered in non-triggered path", func(t *testing.T) {
		geminiReq := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
			Tools:    []Tool{{}},
		}
		vars := BuildGeminiVariablesTyped("gemini-3.6-flash", geminiReq, nil)
		if vars == nil {
			t.Fatal("expected non-nil vars")
		}
		if len(vars.Tools) != 0 {
			t.Fatalf("expected 0 tools (empty tool filtered), got %v", vars.Tools)
		}
	})

	t.Run("unknown field tool filtered in non-triggered path", func(t *testing.T) {
		geminiReq := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
			Tools:    []Tool{{CodeExecution: map[string]any{"unknown": 123}}},
		}
		vars := BuildGeminiVariablesTyped("gemini-3.6-flash", geminiReq, nil)
		if vars == nil {
			t.Fatal("expected non-nil vars")
		}
		if len(vars.Tools) != 1 {
			t.Fatalf("expected 1 tool (codeExecution preserved), got %v", vars.Tools)
		}
	})

	t.Run("snake_case google_search with function_declarations", func(t *testing.T) {
		geminiReq := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "hello"}}}},
			Tools: []Tool{
				{
					GoogleSearch: map[string]any{},
					FunctionDeclarations: []FunctionDeclaration{
						{Name: "get_weather", Description: "get weather"},
					},
				},
			},
		}
		vars := BuildGeminiVariablesTyped("gemini-3.6-flash", geminiReq, nil)
		if vars == nil || len(vars.Tools) != 2 {
			t.Fatalf("expected 2 tools, got %v", vars.Tools)
		}
		if vars.Tools[0].GoogleSearch == nil {
			t.Errorf("expected googleSearch in first tool")
		}
		if len(vars.Tools[0].FunctionDeclarations) != 1 {
			t.Errorf("expected 1 function declaration in first tool")
		}
		if vars.Tools[1].GoogleMaps == nil {
			t.Errorf("expected googleMaps in second tool")
		}
	})
}
