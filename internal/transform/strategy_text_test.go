package transform

import "testing"

func TestTextStrategy_Enhance(t *testing.T) {
	cfg := &mockConfigProvider{defaultThinkingLevel: "中"}

	t.Run("2.5 pro - lowercase high level converted to budget", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-2.5-pro"}
		req := &GeminiRequest{
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "high"},
			},
		}
		st.Enhance(req, cfg)
		tc := req.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 32768 {
			t.Fatalf("expected 32768 budget, got %v", tc)
		}
		if tc.ThinkingLevel != "" {
			t.Errorf("expected empty thinkingLevel, got %q", tc.ThinkingLevel)
		}
	})

	t.Run("2.5 pro - explicit budget passthrough", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-2.5-pro"}
		budget := 8000
		req := &GeminiRequest{
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingBudget: &budget, ThinkingLevel: "high"},
			},
		}
		st.Enhance(req, cfg)
		tc := req.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 8000 {
			t.Fatalf("expected passthrough 8000 budget, got %v", tc)
		}
		if tc.ThinkingLevel != "" {
			t.Errorf("expected empty thinkingLevel, got %q", tc.ThinkingLevel)
		}
	})

	t.Run("3.6 flash - lowercase level to uppercase enum", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-3.6-flash"}
		req := &GeminiRequest{
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "high"},
			},
		}
		st.Enhance(req, cfg)
		tc := req.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingLevel != "HIGH" {
			t.Fatalf("expected ThinkingLevel HIGH, got %v", tc)
		}
		if tc.ThinkingBudget != nil {
			t.Errorf("expected nil thinkingBudget, got %v", tc.ThinkingBudget)
		}
	})

	t.Run("no client thinkingConfig - fallback to console default", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-3.6-flash"}
		req := &GeminiRequest{}
		st.Enhance(req, cfg)
		tc := req.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingLevel != "MEDIUM" {
			t.Fatalf("expected console default MEDIUM, got %v", tc)
		}
	})

	t.Run("3.7 flash - console default MINIMAL downgrades to LOW", func(t *testing.T) {
		cfgMinimal := &mockConfigProvider{defaultThinkingLevel: "最低"}
		st := &TextStrategy{model: "gemini-3.7-flash"}
		req := &GeminiRequest{}
		st.Enhance(req, cfgMinimal)
		tc := req.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingLevel != "LOW" {
			t.Fatalf("expected console default LOW after downgrade for 3.7-flash, got %v", tc)
		}
	})
}

func TestTextStrategy_ValidateAndBuildVariables(t *testing.T) {
	cfg := &mockConfigProvider{defaultThinkingLevel: "中"}

	t.Run("3.7 flash - validate invalid level MINIMAL throws error", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-3.7-flash"}
		req := &GeminiRequest{
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "MINIMAL"},
			},
		}
		if err := st.Validate(req); err == nil {
			t.Fatalf("expected validation error for MINIMAL on gemini-3.7-flash, got nil")
		}
	})

	t.Run("3.7 flash - validate valid level LOW passes", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-3.7-flash"}
		req := &GeminiRequest{
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "LOW"},
			},
		}
		if err := st.Validate(req); err != nil {
			t.Fatalf("expected validation pass for LOW on gemini-3.7-flash, got %v", err)
		}
	})

	t.Run("BuildVariables - parallel tool responses packed in single user turn", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-3.6-flash"}
		req := &GeminiRequest{
			Contents: []Content{
				{Role: "user", Parts: []Part{{Text: "call tool"}}},
				{Role: "user", Parts: []Part{{FunctionResponse: &FunctionResponse{Name: "fn1"}}}},
				{Role: "user", Parts: []Part{{FunctionResponse: &FunctionResponse{Name: "fn2"}}}},
			},
		}
		vars := st.BuildVariables("gemini-3.6-flash", req, cfg)
		contents := vars.GeminiRequest.Contents
		if len(contents) != 2 {
			t.Fatalf("expected 2 content turns, got %d", len(contents))
		}
		if len(contents[1].Parts) != 2 {
			t.Fatalf("expected 2 packed function responses in turn 2, got %d", len(contents[1].Parts))
		}
	})
}

func TestTextStrategy_BuildVariables_DefaultInjectionWithThinkingConfig(t *testing.T) {
	// 回归：客户端带 thinkingConfig 时（Enhance early-return 场景）采样注入仍须生效
	cfg := &mockConfigProvider{}
	st := &TextStrategy{model: "gemini-2.5-flash"}
	req := &GeminiRequest{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "hi"}}}},
		GenerationConfig: &GenerationConfig{
			ThinkingConfig: &ThinkingConfig{ThinkingBudget: intPtr(5000)},
		},
	}
	vars := st.BuildVariables("gemini-2.5-flash", req, cfg)
	gc := vars.GeminiRequest.GenerationConfig
	if gc == nil || gc.Temperature == nil || *gc.Temperature != 1.0 {
		t.Fatalf("expected temperature 1.0 injected despite thinkingConfig, got %v", gc)
	}
	if gc.MaxOutputTokens == nil || *gc.MaxOutputTokens != 65535 {
		t.Fatalf("expected maxOutputTokens 65535 injected, got %v", gc)
	}
	if gc.ThinkingConfig == nil || gc.ThinkingConfig.ThinkingBudget == nil || *gc.ThinkingConfig.ThinkingBudget != 5000 {
		t.Fatalf("expected client thinkingConfig preserved, got %v", gc)
	}
}

func TestTextStrategy_BuildVariables_SamplingByModel(t *testing.T) {
	cfg := &mockConfigProvider{}

	t.Run("3.7-flash forces temperature and topP nil", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-3.7-flash"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "hi"}}}},
			GenerationConfig: &GenerationConfig{
				Temperature: f64ptr(0.5),
				TopP:        f64ptr(0.7),
			},
		}
		vars := st.BuildVariables("gemini-3.7-flash", req, cfg)
		gc := vars.GeminiRequest.GenerationConfig
		if gc.Temperature != nil || gc.TopP != nil {
			t.Fatalf("expected nil Temperature/TopP for gemini-3.7-flash, got %v", gc)
		}
		if gc.MaxOutputTokens == nil || *gc.MaxOutputTokens != 65535 {
			t.Fatalf("expected maxOutputTokens 65535, got %v", gc)
		}
	})

	t.Run("3.6-flash and 3.5-flash-lite force nil sampling too", func(t *testing.T) {
		for _, model := range []string{"gemini-3.6-flash", "gemini-3.5-flash-lite"} {
			st := &TextStrategy{model: model}
			req := &GeminiRequest{
				Contents: []Content{{Role: "user", Parts: []Part{{Text: "hi"}}}},
				GenerationConfig: &GenerationConfig{
					Temperature: f64ptr(0.5),
					TopP:        f64ptr(0.7),
				},
			}
			vars := st.BuildVariables(model, req, cfg)
			gc := vars.GeminiRequest.GenerationConfig
			if gc.Temperature != nil || gc.TopP != nil {
				t.Fatalf("expected nil Temperature/TopP for %s, got %v", model, gc)
			}
			if gc.MaxOutputTokens == nil || *gc.MaxOutputTokens != 65535 {
				t.Fatalf("expected maxOutputTokens 65535 for %s, got %v", model, gc)
			}
		}
	})

	t.Run("unknown text model forces nil sampling and injects 65535", func(t *testing.T) {
		st := &TextStrategy{model: "unknown-text-model"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "hi"}}}},
			GenerationConfig: &GenerationConfig{
				Temperature: f64ptr(0.5),
				TopP:        f64ptr(0.7),
			},
		}
		vars := st.BuildVariables("unknown-text-model", req, cfg)
		gc := vars.GeminiRequest.GenerationConfig
		if gc.Temperature != nil || gc.TopP != nil {
			t.Fatalf("expected nil Temperature/TopP for unknown model, got %v", gc)
		}
		if gc.MaxOutputTokens == nil || *gc.MaxOutputTokens != 65535 {
			t.Fatalf("expected maxOutputTokens 65535 for unknown model, got %v", gc)
		}
	})

	t.Run("2.5-pro injects defaults and clamps", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-2.5-pro"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "hi"}}}},
			GenerationConfig: &GenerationConfig{
				Temperature:     f64ptr(2.5),
				TopP:            f64ptr(1.2),
				MaxOutputTokens: intPtr(100000),
			},
		}
		vars := st.BuildVariables("gemini-2.5-pro", req, cfg)
		gc := vars.GeminiRequest.GenerationConfig
		if gc.Temperature == nil || *gc.Temperature != 2.0 {
			t.Fatalf("expected temperature clamped 2.0, got %v", gc.Temperature)
		}
		if gc.TopP == nil || *gc.TopP != 1.0 {
			t.Fatalf("expected topP clamped 1.0, got %v", gc.TopP)
		}
		if gc.MaxOutputTokens == nil || *gc.MaxOutputTokens != 65535 {
			t.Fatalf("expected maxOutputTokens clamped 65535, got %v", gc.MaxOutputTokens)
		}
	})

	t.Run("drop_max_tokens clears maxOutputTokens", func(t *testing.T) {
		st := &TextStrategy{model: "gemini-2.5-pro"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "hi"}}}},
		}
		vars := st.BuildVariables("gemini-2.5-pro", req, &mockConfigProvider{dropMax: true})
		gc := vars.GeminiRequest.GenerationConfig
		if gc.MaxOutputTokens != nil {
			t.Fatalf("expected nil maxOutputTokens with drop_max_tokens, got %v", gc.MaxOutputTokens)
		}
		if gc.Temperature == nil || *gc.Temperature != 1.0 {
			t.Fatalf("expected temperature still injected, got %v", gc.Temperature)
		}
	})
}
