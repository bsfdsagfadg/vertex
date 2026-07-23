package transform

import (
	"testing"
)

func TestApplyDefaultThinking_SkipWhenClientPassed(t *testing.T) {
	payload := map[string]any{
		"generationConfig": map[string]any{
			"thinkingConfig": map[string]any{"thinkingLevel": "HIGH"},
		},
	}
	ApplyDefaultThinking(payload, "高", "gemini-3.5-flash")
	gc := payload["generationConfig"].(map[string]any)
	tc := gc["thinkingConfig"].(map[string]any)
	if tc["thinkingLevel"] != "HIGH" {
		t.Errorf("client-passed thinkingConfig should not be overwritten, got %v", tc["thinkingLevel"])
	}
}

func TestApplyDefaultThinking_UnknownModel(t *testing.T) {
	payload := map[string]any{}
	ApplyDefaultThinking(payload, "高", "unknown-model-12345")
	_, ok := payload["generationConfig"]
	if ok {
		t.Error("unknown model should not create generationConfig")
	}
}

func TestApplyDefaultThinking_AutoLevel(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		wantTC  bool
		wantVal any
	}{
		{"2.5-pro-budget-minus1", "gemini-2.5-pro", true, -1},
		{"2.5-flash-budget-minus1", "gemini-2.5-flash", true, -1},
		{"3.x-text-skip", "gemini-3.5-flash", false, nil},
		{"3.1-flash-image-skip", "gemini-3.1-flash-image", false, nil},
		{"unsupported-skip", "gemini-2.5-flash-image", false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := map[string]any{}
			ApplyDefaultThinking(payload, "自动", tt.model)
			gc, _ := payload["generationConfig"].(map[string]any)
			tc, ok := gc["thinkingConfig"].(map[string]any)
			if tt.wantTC {
				if !ok {
					t.Fatal("expected thinkingConfig to be set")
				}
				budget, _ := tc["thinkingBudget"].(int)
				if budget != tt.wantVal {
					t.Errorf("expected thinkingBudget=%v, got %v", tt.wantVal, budget)
				}
			} else {
				if ok {
					t.Errorf("expected no thinkingConfig, got %v", tc)
				}
			}
		})
	}
}

func TestApplyDefaultThinking_ThinkingLevelModels(t *testing.T) {
	levels := []struct {
		level string
		want  string
	}{
		{"最低", "MINIMAL"},
		{"低", "LOW"},
		{"中", "MEDIUM"},
		{"高", "HIGH"},
	}
	for _, l := range levels {
		t.Run("3.5-flash-"+l.level, func(t *testing.T) {
			payload := map[string]any{}
			ApplyDefaultThinking(payload, l.level, "gemini-3.5-flash")
			gc := payload["generationConfig"].(map[string]any)
			tc := gc["thinkingConfig"].(map[string]any)
			if tc["thinkingLevel"] != l.want {
				t.Errorf("expected thinkingLevel=%q, got %q", l.want, tc["thinkingLevel"])
			}
		})
	}
}

func TestApplyDefaultThinking_ThinkingBudgetModels(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		level  string
		maxB   int
		wantFn func(int) int
	}{
		{"2.5-pro-min-8192", "gemini-2.5-pro", "最低", 32768, func(max int) int { return max * 1 / 4 }},
		{"2.5-pro-low-16384", "gemini-2.5-pro", "低", 32768, func(max int) int { return max * 2 / 4 }},
		{"2.5-pro-med-24576", "gemini-2.5-pro", "中", 32768, func(max int) int { return max * 3 / 4 }},
		{"2.5-pro-high-32768", "gemini-2.5-pro", "高", 32768, func(max int) int { return max * 4 / 4 }},
		{"2.5-flash-min-6144", "gemini-2.5-flash", "最低", 24576, func(max int) int { return max * 1 / 4 }},
		{"2.5-flash-high-24576", "gemini-2.5-flash", "高", 24576, func(max int) int { return max * 4 / 4 }},
		{"2.5-flash-lite-min-6144", "gemini-2.5-flash-lite", "最低", 24576, func(max int) int { return max * 1 / 4 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := map[string]any{}
			ApplyDefaultThinking(payload, tt.level, tt.model)
			gc := payload["generationConfig"].(map[string]any)
			tc := gc["thinkingConfig"].(map[string]any)
			want := tt.wantFn(tt.maxB)
			budget, _ := tc["thinkingBudget"].(int)
			if budget != want {
				t.Errorf("expected thinkingBudget=%d, got %d", want, budget)
			}
		})
	}
}

func TestApplyDefaultThinking_ImageModelsLevelRestriction(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		level  string
		wantTC bool
		want   string
	}{
		{"image-min-inject", "gemini-3.1-flash-image", "最低", true, "MINIMAL"},
		{"image-low-skip", "gemini-3.1-flash-image", "低", false, ""},
		{"image-med-skip", "gemini-3.1-flash-image", "中", false, ""},
		{"image-high-inject", "gemini-3.1-flash-image", "高", true, "HIGH"},
		{"lite-image-min-inject", "gemini-3.1-flash-lite-image", "最低", true, "MINIMAL"},
		{"lite-image-low-skip", "gemini-3.1-flash-lite-image", "低", false, ""},
		{"lite-image-high-inject", "gemini-3.1-flash-lite-image", "高", true, "HIGH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := map[string]any{}
			ApplyDefaultThinking(payload, tt.level, tt.model)
			gc, _ := payload["generationConfig"].(map[string]any)
			tc, ok := gc["thinkingConfig"].(map[string]any)
			if tt.wantTC {
				if !ok {
					t.Fatal("expected thinkingConfig to be set")
				}
				if tc["thinkingLevel"] != tt.want {
					t.Errorf("expected thinkingLevel=%q, got %q", tt.want, tc["thinkingLevel"])
				}
			} else {
				if ok {
					t.Errorf("expected no thinkingConfig, got %v", tc)
				}
			}
		})
	}
}

func TestApplyDefaultThinking_InvalidLevel(t *testing.T) {
	payload := map[string]any{}
	ApplyDefaultThinking(payload, "INVALID", "gemini-3.5-flash")
	_, ok := payload["generationConfig"]
	if ok {
		t.Error("invalid level should not create generationConfig")
	}
}

func TestApplyDefaultThinking_UnsupportedModel(t *testing.T) {
	payload := map[string]any{}
	ApplyDefaultThinking(payload, "高", "gemini-2.5-flash-image")
	_, ok := payload["generationConfig"]
	if ok {
		t.Error("unsupported model should not create generationConfig")
	}
}
