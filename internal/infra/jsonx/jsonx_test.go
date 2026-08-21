package jsonx

import (
	"bytes"
	"testing"
)

func TestMarshal(t *testing.T) {
	tests := []struct {
		name    string
		v       any
		want    []byte
		wantErr bool
	}{
		{ //nolint:exhaustruct
			name: "no html escape",
			v:    map[string]string{"html": "<script>alert(1)</script> & foo"},
			want: []byte(`{"html":"<script>alert(1)</script> & foo"}`),
		},
		{ //nolint:exhaustruct
			name: "unicode preserved",
			v:    map[string]string{"text": "你好世界"},
			want: []byte(`{"text":"你好世界"}`),
		},
		{ //nolint:exhaustruct
			name: "simple string",
			v:    "test",
			want: []byte(`"test"`),
		},
		{
			name: "nil",
			v:    nil,
			want: []byte(`null`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Marshal(tt.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("Marshal() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("Marshal() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestTruthy(t *testing.T) {
	tests := []struct { //nolint:govet
		name string
		v    any
		want bool
	}{
		{"nil", nil, false},
		{"bool true", true, true},
		{"bool false", false, false},
		{"string empty", "", false},
		{"string non-empty", "hello", true},
		{"float64 zero", 0.0, false},
		{"float64 non-zero", 1.5, true},
		{"float64 negative", -1.5, true},
		{"int zero (default true for unhandled)", 0, true}, // Truthy function doesn't specifically handle int
		{"slice empty", []any{}, false},
		{"slice non-empty", []any{1}, true},
		{"map empty", map[string]any{}, false},
		{"map non-empty", map[string]any{"a": 1}, true},
		{"custom struct", struct{}{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Truthy(tt.v); got != tt.want {
				t.Errorf("Truthy(%v) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}
