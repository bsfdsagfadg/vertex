package transform

import "testing"

func TestImageStrategy_EnhanceAndValidate(t *testing.T) {
	cfg := &mockConfigProvider{defaultImageSize: "1K", defaultResponseModalities: "默认"}

	t.Run("3.1 flash image - lowercase level normalized and validated", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &GeminiRequest{
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "high"},
			},
		}
		st.Enhance(req, cfg)
		if err := st.Validate(req); err != nil {
			t.Fatalf("validation failed: %v", err)
		}
		tc := req.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingLevel != "HIGH" {
			t.Fatalf("expected HIGH, got %v", tc)
		}
	})

	t.Run("unsupported image model - thinkingConfig purged to nil", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-2.5-flash-image"}
		req := &GeminiRequest{
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "high"},
			},
		}
		st.Enhance(req, cfg)
		if err := st.Validate(req); err != nil {
			t.Fatalf("validation failed: %v", err)
		}
		if req.GenerationConfig.ThinkingConfig != nil {
			t.Fatalf("expected nil thinkingConfig for unsupported image model, got %v", req.GenerationConfig.ThinkingConfig)
		}
	})

	t.Run("no client thinkingConfig - image strategy injects console default level (aligned with text)", func(t *testing.T) {
		cfgWithThinking := &mockConfigProvider{defaultThinkingLevel: "中", defaultImageSize: "1K", defaultResponseModalities: "默认"}

		// 2.5-flash-image (unsupported thinking) -> 不注入
		st25 := &ImageStrategy{model: "gemini-2.5-flash-image"}
		req25 := &GeminiRequest{}
		st25.Enhance(req25, cfgWithThinking)
		if req25.GenerationConfig.ThinkingConfig != nil {
			t.Fatalf("expected nil thinkingConfig for gemini-2.5-flash-image without client input, got %v", req25.GenerationConfig.ThinkingConfig)
		}
		if err := st25.Validate(req25); err != nil {
			t.Fatalf("validation failed for gemini-2.5-flash-image: %v", err)
		}

		// 3.1-flash-image (supports MINIMAL and HIGH thinking) -> 控制台"中"平滑降级为 MINIMAL
		st31 := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req31 := &GeminiRequest{}
		st31.Enhance(req31, cfgWithThinking)
		tc := req31.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingLevel != "MINIMAL" {
			t.Fatalf("expected MINIMAL thinkingConfig for gemini-3.1-flash-image without client input (console 中), got %v", tc)
		}
		if err := st31.Validate(req31); err != nil {
			t.Fatalf("validation failed for gemini-3.1-flash-image: %v", err)
		}
	})
}

func TestImageStrategy_All4Models_ThinkingDefaultInjection(t *testing.T) {
	cfgWithThinking := &mockConfigProvider{
		defaultThinkingLevel:      "高",
		defaultImageSize:          "1K",
		defaultResponseModalities: "默认",
	}

	t.Run("supporting models inject console HIGH", func(t *testing.T) {
		for _, model := range []string{"gemini-3.1-flash-image", "gemini-3.1-flash-lite-image"} {
			st := &ImageStrategy{model: model}
			req := &GeminiRequest{}
			st.Enhance(req, cfgWithThinking)
			tc := req.GenerationConfig.ThinkingConfig
			if tc == nil || tc.ThinkingLevel != "HIGH" {
				t.Fatalf("expected HIGH thinkingConfig for %s without client input (console 高), got %v", model, tc)
			}
			if err := st.Validate(req); err != nil {
				t.Fatalf("validation failed for %s: %v", model, err)
			}
		}
	})

	t.Run("unsupported models stay nil", func(t *testing.T) {
		for _, model := range []string{"gemini-2.5-flash-image", "gemini-3-pro-image"} {
			st := &ImageStrategy{model: model}
			req := &GeminiRequest{}
			st.Enhance(req, cfgWithThinking)
			if req.GenerationConfig.ThinkingConfig != nil {
				t.Fatalf("expected nil thinkingConfig for %s without client input, got %v", model, req.GenerationConfig.ThinkingConfig)
			}
			if err := st.Validate(req); err != nil {
				t.Fatalf("validation failed for %s: %v", model, err)
			}
		}
	})
}

func TestImageStrategy_ThinkingSmoothDowngrade(t *testing.T) {
	cfg := &mockConfigProvider{defaultImageSize: "1K", defaultResponseModalities: "默认"}
	st := &ImageStrategy{model: "gemini-3.1-flash-image"}

	cases := []struct {
		name  string
		level string
		want  string
	}{
		{name: "whitelisted HIGH preserved", level: "high", want: "HIGH"},
		{name: "whitelisted MINIMAL preserved", level: "minimal", want: "MINIMAL"},
		{name: "out-of-range LOW downgraded to MINIMAL", level: "low", want: "MINIMAL"},
		{name: "out-of-range MEDIUM downgraded to MINIMAL", level: "medium", want: "MINIMAL"},
		{name: "Chinese 中 downgraded to MINIMAL", level: "中", want: "MINIMAL"},
		{name: "Chinese 低 downgraded to MINIMAL", level: "低", want: "MINIMAL"},
		{name: "OFF treated as not-sent", level: "off", want: ""},
		{name: "NONE treated as not-sent", level: "none", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &GeminiRequest{
				GenerationConfig: &GenerationConfig{
					ThinkingConfig: &ThinkingConfig{ThinkingLevel: tc.level},
				},
			}
			st.Enhance(req, cfg)
			got := req.GenerationConfig.ThinkingConfig
			if tc.want == "" {
				if got != nil {
					t.Fatalf("expected nil thinkingConfig for level %q, got %v", tc.level, got)
				}
				return
			}
			if got == nil || got.ThinkingLevel != tc.want {
				t.Fatalf("expected level %q to resolve to %s, got %v", tc.level, tc.want, got)
			}
			if err := st.Validate(req); err != nil {
				t.Fatalf("validation failed after smoothing for level %q: %v", tc.level, err)
			}
		})
	}
}

func TestImageStrategy_BuildVariables_ThinkingSmoothDowngrade(t *testing.T) {
	cfg := &mockConfigProvider{}

	t.Run("direct BuildVariables downgrades out-of-range level", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "MEDIUM"},
			},
		}
		vars := st.BuildVariables("gemini-3.1-flash-image", req, cfg)
		tc := vars.GeminiRequest.GenerationConfig.ThinkingConfig
		if tc == nil || tc.ThinkingLevel != "MINIMAL" {
			t.Fatalf("expected MEDIUM downgraded to MINIMAL by BuildVariables, got %v", tc)
		}
	})

	t.Run("direct BuildVariables drops OFF to nil", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
			GenerationConfig: &GenerationConfig{
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "OFF"},
			},
		}
		vars := st.BuildVariables("gemini-3.1-flash-image", req, cfg)
		if vars.GeminiRequest.GenerationConfig.ThinkingConfig != nil {
			t.Fatalf("expected OFF dropped to nil by BuildVariables, got %v", vars.GeminiRequest.GenerationConfig.ThinkingConfig)
		}
	})
}

func TestImageStrategy_BuildVariables_FixedSafetySettings(t *testing.T) {
	cfg := &mockConfigProvider{}
	st := &ImageStrategy{model: "gemini-3.1-flash-image"}

	// 客户端传入任意类别/threshold 均被覆盖为固定 4×OFF
	req := &GeminiRequest{
		Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
		SafetySettings: []SafetySetting{
			{Category: "HARM_CATEGORY_CIVIC_INTEGRITY", Threshold: "BLOCK_LOW_AND_ABOVE"},
			{Category: "HARM_CATEGORY_JAILBREAK", Threshold: "OFF"},
			{Category: " custom ", Threshold: "NONE"},
		},
	}
	vars := st.BuildVariables("gemini-3.1-flash-image", req, cfg)
	assertFixed4OFF(t, vars.GeminiRequest.SafetySettings)
}

func TestImageStrategy_Prepare_Noop(t *testing.T) {
	// Prepare 已收敛为空实现：SafetySettings 由 BuildVariables 统一覆盖为固定 4×OFF，
	// 不再做 JAILBREAK / CIVIC_INTEGRITY 剥离与枚举规范化。
	st := &ImageStrategy{model: "gemini-3.1-flash-image"}
	req := &GeminiRequest{
		SafetySettings: []SafetySetting{
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_NONE"},
			{Category: " harm_category_jailbreak ", Threshold: "BLOCK_MEDIUM_AND_ABOVE"},
			{Category: "Harm_Category_Civic_Integrity", Threshold: "BLOCK_LOW_AND_ABOVE"},
		},
	}
	st.Prepare(req)
	if len(req.SafetySettings) != 3 {
		t.Fatalf("Prepare must not mutate SafetySettings, got %v", req.SafetySettings)
	}
	if req.SafetySettings[0].Category != "HARM_CATEGORY_HATE_SPEECH" {
		t.Fatalf("Prepare must not normalize categories, got %v", req.SafetySettings[0])
	}
}

func TestImageStrategy_IsValidResponse(t *testing.T) {
	st := &ImageStrategy{model: "gemini-3.1-flash-image"}

	// 1. 无图片无 Safety 的纯文本响应 -> 判定为无效 (非流式不能因为纯文本而误判为连通胜出)
	textResp := &GeminiResponse{
		Candidates: []*Candidate{
			{
				Content: &Content{
					Parts: []Part{{Text: "Sorry, I cannot generate an image."}},
				},
			},
		},
	}
	if st.IsValidResponse(textResp) {
		t.Error("textResp without images or safety block should NOT be valid for ImageStrategy")
	}

	// 2. 包含图片 Payload 的响应 -> 判定为有效
	imageResp := &GeminiResponse{
		Candidates: []*Candidate{
			{
				Content: &Content{
					Parts: []Part{
						{InlineData: &InlineData{MimeType: "image/png", Data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}},
					},
				},
			},
		},
	}
	if !st.IsValidResponse(imageResp) {
		t.Error("imageResp with image payload should be valid for ImageStrategy")
	}

	// 3. 包含 Safety 拦截的响应 -> 判定为有效
	safetyResp := &GeminiResponse{
		Candidates: []*Candidate{
			{FinishReason: "SAFETY"},
		},
	}
	if !st.IsValidResponse(safetyResp) {
		t.Error("safetyResp should be valid for ImageStrategy")
	}
}

func TestImageStrategy_BuildVariables_WhitelistAndTools(t *testing.T) {
	cfg := &mockConfigProvider{
		defaultImageSize:          "1K",
		defaultResponseModalities: "图文",
	}

	t.Run("gemini-3.1-flash-lite-image filters tools and clamps size", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-lite-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw cat"}}}},
			Tools: []Tool{
				{GoogleSearch: map[string]any{}},
				{FunctionDeclarations: []FunctionDeclaration{{Name: "fn1"}}},
			},
			GenerationConfig: &GenerationConfig{
				ImageConfig: &ImageConfig{
					ImageSize:      "4K", // lite 不支持 4K -> 回退到 1K
					AspectRatio:    "auto",
					OutputMimeType: "image/png",
				},
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "HIGH"},
			},
		}
		vars := st.BuildVariables("gemini-3.1-flash-lite-image", req, cfg)
		outReq := vars.GeminiRequest

		// Tools 必须被清空（lite 不支持 search 也绝不允许 functions）
		if len(outReq.Tools) > 0 {
			t.Fatalf("expected 0 tools for lite-image, got %d", len(outReq.Tools))
		}
		if outReq.GenerationConfig.ImageConfig.ImageSize != "1K" {
			t.Fatalf("expected clamped 1K, got %q", outReq.GenerationConfig.ImageConfig.ImageSize)
		}
		if outReq.GenerationConfig.ThinkingConfig == nil || outReq.GenerationConfig.ThinkingConfig.ThinkingLevel != "HIGH" {
			t.Fatalf("expected HIGH thinkingLevel preserved for lite-image, got %v", outReq.GenerationConfig.ThinkingConfig)
		}
	})

	t.Run("gemini-3.1-flash-image preserves GoogleSearch and removes functions", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw dog"}}}},
			Tools: []Tool{
				{GoogleSearch: map[string]any{}},
				{FunctionDeclarations: []FunctionDeclaration{{Name: "calc"}}},
			},
			GenerationConfig: &GenerationConfig{
				ImageConfig: &ImageConfig{
					ImageSize:      "4K",
					AspectRatio:    "16:9",
					OutputMimeType: "image/jpeg",
				},
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "HIGH"},
			},
		}
		vars := st.BuildVariables("gemini-3.1-flash-image", req, cfg)
		outReq := vars.GeminiRequest

		if len(outReq.Tools) != 1 || outReq.Tools[0].GoogleSearch == nil || len(outReq.Tools[0].FunctionDeclarations) > 0 {
			t.Fatalf("expected exactly 1 tool with GoogleSearch only, got %v", outReq.Tools)
		}
		if outReq.GenerationConfig.ImageConfig.ImageSize != "4K" {
			t.Fatalf("expected 4K imageSize, got %q", outReq.GenerationConfig.ImageConfig.ImageSize)
		}
	})

	t.Run("gemini-3-pro-image auto aspect ratio downgraded to 1:1 and purges thinking", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3-pro-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw landscape"}}}},
			GenerationConfig: &GenerationConfig{
				ImageConfig: &ImageConfig{
					ImageSize:   "2K",
					AspectRatio: "auto", // pro 不支持 auto -> 回退到 1:1
				},
				ThinkingConfig: &ThinkingConfig{ThinkingLevel: "HIGH"}, // pro 不支持思考 -> 清空
			},
		}
		vars := st.BuildVariables("gemini-3-pro-image", req, cfg)
		outReq := vars.GeminiRequest

		if outReq.GenerationConfig.ImageConfig.AspectRatio != "1:1" {
			t.Fatalf("expected 1:1 aspect ratio fallback for pro-image, got %q", outReq.GenerationConfig.ImageConfig.AspectRatio)
		}
		if outReq.GenerationConfig.ThinkingConfig != nil {
			t.Fatalf("expected nil thinkingConfig for pro-image, got %v", outReq.GenerationConfig.ThinkingConfig)
		}
	})
}

func TestImageStrategy_BuildVariables_DefaultSamplingInjection(t *testing.T) {
	cfg := &mockConfigProvider{}

	t.Run("nil GenerationConfig injects defaults", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
		}
		vars := st.BuildVariables("gemini-3.1-flash-image", req, cfg)
		gc := vars.GeminiRequest.GenerationConfig
		if gc == nil {
			t.Fatal("expected non-nil GenerationConfig with default injection")
		}
		if gc.Temperature == nil || *gc.Temperature != 1.0 {
			t.Fatalf("expected temperature 1.0 injected, got %v", gc.Temperature)
		}
		if gc.TopP == nil || *gc.TopP != 0.95 {
			t.Fatalf("expected topP 0.95 injected, got %v", gc.TopP)
		}
		if gc.MaxOutputTokens == nil || *gc.MaxOutputTokens != 32768 {
			t.Fatalf("expected maxOutputTokens 32768 injected, got %v", gc.MaxOutputTokens)
		}
	})

	t.Run("lite-image temperature clamped to 1.0", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-lite-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
			GenerationConfig: &GenerationConfig{
				Temperature: f64ptr(1.5),
			},
		}
		vars := st.BuildVariables("gemini-3.1-flash-lite-image", req, cfg)
		gc := vars.GeminiRequest.GenerationConfig
		if gc.Temperature == nil || *gc.Temperature != 1.0 {
			t.Fatalf("expected lite-image temperature clamped to 1.0, got %v", gc.Temperature)
		}
	})

	t.Run("flash-image temperature 1.5 not over-clamped", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
			GenerationConfig: &GenerationConfig{
				Temperature: f64ptr(1.5),
			},
		}
		vars := st.BuildVariables("gemini-3.1-flash-image", req, cfg)
		gc := vars.GeminiRequest.GenerationConfig
		if gc.Temperature == nil || *gc.Temperature != 1.5 {
			t.Fatalf("expected flash-image temperature 1.5 preserved, got %v", gc.Temperature)
		}
	})

	t.Run("flash-image temperature 2.5 clamped to 2.0", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
			GenerationConfig: &GenerationConfig{
				Temperature: f64ptr(2.5),
			},
		}
		vars := st.BuildVariables("gemini-3.1-flash-image", req, cfg)
		gc := vars.GeminiRequest.GenerationConfig
		if gc.Temperature == nil || *gc.Temperature != 2.0 {
			t.Fatalf("expected flash-image temperature clamped to 2.0, got %v", gc.Temperature)
		}
	})

	t.Run("drop_max_tokens clears maxOutputTokens", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
		}
		vars := st.BuildVariables("gemini-3.1-flash-image", req, &mockConfigProvider{dropMax: true})
		gc := vars.GeminiRequest.GenerationConfig
		if gc.MaxOutputTokens != nil {
			t.Fatalf("expected nil maxOutputTokens with drop_max_tokens, got %v", gc.MaxOutputTokens)
		}
		if gc.Temperature == nil || *gc.Temperature != 1.0 {
			t.Fatalf("expected temperature still injected, got %v", gc.Temperature)
		}
	})
}

func TestImageStrategy_BuildVariables_SearchFilter(t *testing.T) {
	cfg := &mockConfigProvider{}

	t.Run("flash-image keeps only googleSearch, strips maps/retrieval/functions", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
			Tools: []Tool{
				{GoogleSearch: map[string]any{}},
				{GoogleMaps: map[string]any{}},
				{GoogleSearchRetrieval: map[string]any{}},
				{FunctionDeclarations: []FunctionDeclaration{{Name: "fn"}}},
			},
			ToolConfig: &ToolConfig{FunctionCallingConfig: &FunctionCallingConfig{Mode: "AUTO"}},
		}
		vars := st.BuildVariables("gemini-3.1-flash-image", req, cfg)
		outReq := vars.GeminiRequest
		if len(outReq.Tools) != 1 || outReq.Tools[0].GoogleSearch == nil {
			t.Fatalf("expected exactly 1 googleSearch tool, got %v", outReq.Tools)
		}
		if outReq.Tools[0].GoogleMaps != nil || outReq.Tools[0].GoogleSearchRetrieval != nil || len(outReq.Tools[0].FunctionDeclarations) > 0 {
			t.Fatalf("expected stripped tool, got %v", outReq.Tools[0])
		}
		if outReq.ToolConfig != nil {
			t.Fatalf("expected nil ToolConfig for image family, got %v", outReq.ToolConfig)
		}
	})

	t.Run("pro-image keeps only googleSearch", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3-pro-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
			Tools: []Tool{
				{GoogleSearch: map[string]any{}},
				{GoogleMaps: map[string]any{}},
			},
		}
		vars := st.BuildVariables("gemini-3-pro-image", req, cfg)
		outReq := vars.GeminiRequest
		if len(outReq.Tools) != 1 || outReq.Tools[0].GoogleSearch == nil {
			t.Fatalf("expected exactly 1 googleSearch tool for pro-image, got %v", outReq.Tools)
		}
	})

	t.Run("lite-image (no search) clears all tools and ToolConfig", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-lite-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
			Tools: []Tool{
				{GoogleSearch: map[string]any{}},
				{GoogleMaps: map[string]any{}},
			},
			ToolConfig: &ToolConfig{FunctionCallingConfig: &FunctionCallingConfig{Mode: "AUTO"}},
		}
		vars := st.BuildVariables("gemini-3.1-flash-lite-image", req, cfg)
		outReq := vars.GeminiRequest
		if len(outReq.Tools) != 0 {
			t.Fatalf("expected 0 tools for lite-image, got %v", outReq.Tools)
		}
		if outReq.ToolConfig != nil {
			t.Fatalf("expected nil ToolConfig for lite-image, got %v", outReq.ToolConfig)
		}
	})
}

func TestImageStrategy_BuildVariables_SearchFilter_Pipeline(t *testing.T) {
	cfg := &mockConfigProvider{}

	t.Run("flash-image googleSearch tool preserved, other tools stripped", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
			Tools:    []Tool{{GoogleSearch: map[string]any{}}},
		}
		vars := st.BuildVariables("gemini-3.1-flash-image", req, cfg)
		outReq := vars.GeminiRequest
		if len(outReq.Tools) != 1 || outReq.Tools[0].GoogleSearch == nil {
			t.Fatalf("expected exactly 1 googleSearch tool, got %v", outReq.Tools)
		}
	})

	t.Run("lite-image googleSearch tool cleared", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-lite-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
			Tools:    []Tool{{GoogleSearch: map[string]any{}}},
		}
		vars := st.BuildVariables("gemini-3.1-flash-lite-image", req, cfg)
		outReq := vars.GeminiRequest
		if len(outReq.Tools) != 0 {
			t.Fatalf("expected 0 tools for lite-image, got %v", outReq.Tools)
		}
	})

	t.Run("flash-image empty tool filtered", func(t *testing.T) {
		st := &ImageStrategy{model: "gemini-3.1-flash-image"}
		req := &GeminiRequest{
			Contents: []Content{{Role: "user", Parts: []Part{{Text: "draw"}}}},
			Tools:    []Tool{{}},
		}
		vars := st.BuildVariables("gemini-3.1-flash-image", req, cfg)
		outReq := vars.GeminiRequest
		if len(outReq.Tools) != 0 {
			t.Fatalf("expected 0 tools for empty input, got %v", outReq.Tools)
		}
	})
}
