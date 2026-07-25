package transform

import (
	"testing"
)

func TestApplyResponseModalities_NonImageModel(t *testing.T) {
		payload := map[string]any{}
	ApplyResponseModalities(payload, "仅图片", "gemini-3.5-flash")
	if _, ok := payload["generationConfig"]; ok {
		t.Error("非图像模型不应创建 generationConfig")
	}
}

func TestApplyResponseModalities_ClientPreserved(t *testing.T) {
	tests := []struct {
		name         string
		existingVal  any
		defaultMod   string
		expectChange bool
		expected     []any
	}{
		{"non-empty slice preserved", []any{"TEXT", "IMAGE"}, "仅图片", false, []any{"TEXT", "IMAGE"}},
		{"single element preserved", []any{"IMAGE"}, "图文", false, []any{"IMAGE"}},
		{"nil map value skipped", nil, "仅图片", true, []any{"IMAGE"}},
		{"empty slice replaced", []any{}, "图文", true, []any{"TEXT", "IMAGE"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := map[string]any{}
			if tt.existingVal != nil || tt.name == "nil map value skipped" {
				gc := map[string]any{"responseModalities": tt.existingVal}
				payload["generationConfig"] = gc
			}
			ApplyResponseModalities(payload, tt.defaultMod, "gemini-3.1-flash-image")
			gc, ok := payload["generationConfig"].(map[string]any)
			if !ok {
				t.Fatal("generationConfig 应存在")
			}
			rm, ok := gc["responseModalities"].([]any)
			if !ok {
				t.Fatalf("responseModalities 应为 []any, got %T", gc["responseModalities"])
			}
			if tt.expectChange {
				if len(rm) != len(tt.expected) {
					t.Errorf("len=%d, want %d, got %v", len(rm), len(tt.expected), rm)
				}
				for i, v := range tt.expected {
					if rm[i] != v {
						t.Errorf("rm[%d]=%v, want %v", i, rm[i], v)
					}
				}
			} else {
				if len(rm) != len(tt.existingVal.([]any)) {
					t.Errorf("客户端值被修改: got %v, want %v", rm, tt.existingVal)
				}
			}
		})
	}
}

func TestApplyResponseModalities_DefaultInjection(t *testing.T) {
	tests := []struct {
		name     string
		defaults string
		model    string
		want     []any
	}{
		{"仅图片注入 IMAGE", "仅图片", "gemini-3.1-flash-image", []any{"IMAGE"}},
		{"图文注入 TEXT IMAGE", "图文", "gemini-3.1-flash-image", []any{"TEXT", "IMAGE"}},
		{"lite image 模型图文", "图文", "gemini-3.1-flash-lite-image", []any{"TEXT", "IMAGE"}},
		{"非图像模型不注入", "图文", "gemini-3.5-flash", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := map[string]any{}
			ApplyResponseModalities(payload, tt.defaults, tt.model)
			if tt.want == nil {
				if _, ok := payload["generationConfig"]; ok {
					t.Error("不应创建 generationConfig")
				}
				return
			}
			gc, ok := payload["generationConfig"].(map[string]any)
			if !ok {
				t.Fatal("generationConfig 应存在")
			}
			rm, ok := gc["responseModalities"].([]any)
			if !ok {
				t.Fatalf("responseModalities 应为 []any, got %T", gc["responseModalities"])
			}
			if len(rm) != len(tt.want) {
				t.Errorf("len=%d, want %d, got %v", len(rm), len(tt.want), rm)
				return
			}
			for i, v := range tt.want {
				if rm[i] != v {
					t.Errorf("rm[%d]=%v, want %v", i, rm[i], v)
				}
			}
		})
	}
}
