package transform

import (
	"reflect"
	"testing"
)

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
