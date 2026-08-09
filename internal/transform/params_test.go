package transform

import (
	"testing"
)

func TestResolveResponseModalities_NonImageModel(t *testing.T) {
	res := ResolveResponseModalities("仅图片", "gemini-3.5-flash")
	if res != nil {
		t.Error("非图像模型不应解析出 responseModalities")
	}
}

func TestResolveResponseModalities_DefaultInjection(t *testing.T) {
	tests := []struct {
		name     string
		defaults string
		model    string
		wantLen  int
		want0    string
	}{
		{"仅图片注入 IMAGE", "仅图片", "gemini-3.1-flash-image", 1, "IMAGE"},
		{"图文注入 TEXT IMAGE", "图文", "gemini-3.1-flash-image", 2, "TEXT"},
		{"lite image 模型图文", "图文", "gemini-3.1-flash-lite-image", 2, "TEXT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := ResolveResponseModalities(tt.defaults, tt.model)
			if len(res) != tt.wantLen {
				t.Fatalf("len=%d, want %d", len(res), tt.wantLen)
			}
			if res[0] != tt.want0 {
				t.Errorf("res[0]=%s, want %s", res[0], tt.want0)
			}
		})
	}
}
