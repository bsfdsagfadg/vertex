package transform

import (
	"testing"
)

func TestResolveThinkingConfig_UnknownModel(t *testing.T) {
	tc := ResolveThinkingConfig("高", "unknown-model-12345")
	if tc != nil {
		t.Error("unknown model should return nil ThinkingConfig")
	}
}

func TestResolveThinkingConfig_AutoLevel(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		wantTC  bool
		wantBudget int
	}{
		{"2.5-pro-budget-minus1", "gemini-2.5-pro", true, -1},
		{"2.5-flash-budget-minus1", "gemini-2.5-flash", true, -1},
		{"3.x-text-skip", "gemini-3.5-flash", false, 0},
		{"3.1-flash-image-skip", "gemini-3.1-flash-image", false, 0},
		{"unsupported-skip", "gemini-2.5-flash-image", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := ResolveThinkingConfig("自动", tt.model)
			if tt.wantTC {
				if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != tt.wantBudget {
					t.Fatalf("expected budget=%d, got %v", tt.wantBudget, tc)
				}
			} else {
				if tc != nil {
					t.Fatalf("expected nil thinkingConfig, got %v", tc)
				}
			}
		})
	}
}

func TestResolveThinkingConfig_ThinkingLevelModels(t *testing.T) {
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
			tc := ResolveThinkingConfig(l.level, "gemini-3.5-flash")
			if tc == nil || tc.ThinkingLevel != l.want {
				t.Errorf("expected level=%q, got %v", l.want, tc)
			}
		})
	}
}

func TestResolveThinkingConfig_ThinkingBudgetModels(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		level  string
		want   int
	}{
		{"2.5-pro-min", "gemini-2.5-pro", "最低", 8192},
		{"2.5-pro-low", "gemini-2.5-pro", "低", 16384},
		{"2.5-pro-med", "gemini-2.5-pro", "中", 24576},
		{"2.5-pro-high", "gemini-2.5-pro", "高", 32768},
		{"2.5-flash-min", "gemini-2.5-flash", "最低", 6144},
		{"2.5-flash-high", "gemini-2.5-flash", "高", 24576},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := ResolveThinkingConfig(tt.level, tt.model)
			if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != tt.want {
				t.Errorf("expected budget=%d, got %v", tt.want, tc)
			}
		})
	}
}

func TestResolveThinkingConfig_ImageModelsLevelRestriction(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		level  string
		wantTC bool
		want   string
	}{
		{"image-min-inject", "gemini-3.1-flash-image", "最低", true, "MINIMAL"},
		{"image-low-downgrade", "gemini-3.1-flash-image", "低", true, "MINIMAL"},
		{"image-med-downgrade", "gemini-3.1-flash-image", "中", true, "MINIMAL"},
		{"image-high-inject", "gemini-3.1-flash-image", "高", true, "HIGH"},
		{"lite-image-min-inject", "gemini-3.1-flash-lite-image", "最低", true, "MINIMAL"},
		{"lite-image-low-downgrade", "gemini-3.1-flash-lite-image", "低", true, "MINIMAL"},
		{"lite-image-high-inject", "gemini-3.1-flash-lite-image", "高", true, "HIGH"},
		{"3.7-flash-min-downgrade", "gemini-3.7-flash", "最低", true, "LOW"},
		{"3.7-flash-low-inject", "gemini-3.7-flash", "低", true, "LOW"},
		{"3.7-flash-med-inject", "gemini-3.7-flash", "中", true, "MEDIUM"},
		{"3.7-flash-high-inject", "gemini-3.7-flash", "高", true, "HIGH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := ResolveThinkingConfig(tt.level, tt.model)
			if tt.wantTC {
				if tc == nil || tc.ThinkingLevel != tt.want {
					t.Fatalf("expected thinkingLevel=%q, got %v", tt.want, tc)
				}
			} else {
				if tc != nil {
					t.Fatalf("expected nil thinkingConfig, got %v", tc)
				}
			}
		})
	}
}

func TestResolveThinkingConfig_InvalidLevel(t *testing.T) {
	tc := ResolveThinkingConfig("INVALID", "gemini-3.5-flash")
	if tc != nil {
		t.Error("invalid level should return nil")
	}
}

func TestResolveThinkingConfig_UnsupportedModel(t *testing.T) {
	tc := ResolveThinkingConfig("高", "gemini-2.5-flash-image")
	if tc != nil {
		t.Error("unsupported model should return nil")
	}
}

func TestNormalizeThinkingConfig(t *testing.T) {
	intPtr := func(v int) *int { return &v }

	t.Run("nil config", func(t *testing.T) {
		if res := NormalizeThinkingConfig(nil, "gemini-3.6-flash"); res != nil {
			t.Errorf("expected nil for nil input, got %v", res)
		}
	})

	t.Run("unsupported model", func(t *testing.T) {
		tc := &ThinkingConfig{ThinkingLevel: "high"}
		if res := NormalizeThinkingConfig(tc, "gemini-2.5-flash-image"); res != nil {
			t.Errorf("expected nil for unsupported model, got %v", res)
		}
	})

	t.Run("2.5 pro - budget exact passthrough", func(t *testing.T) {
		tc := &ThinkingConfig{ThinkingBudget: intPtr(8000), ThinkingLevel: "high"}
		res := NormalizeThinkingConfig(tc, "gemini-2.5-pro")
		if res == nil || res.ThinkingBudget == nil || *res.ThinkingBudget != 8000 {
			t.Fatalf("expected exact budget 8000, got %v", res)
		}
		if res.ThinkingLevel != "" {
			t.Errorf("expected empty thinkingLevel for 2.5 budget model, got %q", res.ThinkingLevel)
		}
	})

	t.Run("2.5 flash - lowercase level to budget", func(t *testing.T) {
		tc := &ThinkingConfig{ThinkingLevel: "high"}
		res := NormalizeThinkingConfig(tc, "gemini-2.5-flash")
		if res == nil || res.ThinkingBudget == nil || *res.ThinkingBudget != 24576 {
			t.Fatalf("expected budget 24576 for flash high, got %v", res)
		}
		if res.ThinkingLevel != "" {
			t.Errorf("expected empty thinkingLevel, got %q", res.ThinkingLevel)
		}
	})

	t.Run("3.6 flash - lowercase level to uppercase enum", func(t *testing.T) {
		tc := &ThinkingConfig{ThinkingLevel: "high"}
		res := NormalizeThinkingConfig(tc, "gemini-3.6-flash")
		if res == nil || res.ThinkingLevel != "HIGH" {
			t.Fatalf("expected ThinkingLevel HIGH, got %v", res)
		}
		if res.ThinkingBudget != nil {
			t.Errorf("expected nil budget for 3.x level model, got %v", res.ThinkingBudget)
		}
	})

	t.Run("3.5 flash - budget to level enum", func(t *testing.T) {
		tc := &ThinkingConfig{ThinkingBudget: intPtr(25000)}
		res := NormalizeThinkingConfig(tc, "gemini-3.5-flash")
		if res == nil || res.ThinkingLevel != "HIGH" {
			t.Fatalf("expected ThinkingLevel HIGH for 25000 budget, got %v", res)
		}
		if res.ThinkingBudget != nil {
			t.Errorf("expected nil budget, got %v", res.ThinkingBudget)
		}
	})

	t.Run("3.1 flash image - uppercase level normalization", func(t *testing.T) {
		tc := &ThinkingConfig{ThinkingLevel: "minimal"}
		res := NormalizeThinkingConfig(tc, "gemini-3.1-flash-image")
		if res == nil || res.ThinkingLevel != "MINIMAL" {
			t.Fatalf("expected MINIMAL, got %v", res)
		}
	})
}
