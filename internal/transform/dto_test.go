package transform

import (
	"encoding/json"
	"testing"
)

func TestToolConfig_UnmarshalJSON_DualAlias(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantMode   string
		wantRet    bool
		wantSN     string
	}{
		{
			name:     "camelCase retrievalConfig only",
			raw:      `{"retrievalConfig": {"disableAttribution": true}}`,
			wantMode: "",
			wantRet:  true,
		},
		{
			name:     "snake_case retrieval_config fallback",
			raw:      `{"retrieval_config": {"googleSearchRetrieval": {}}}`,
			wantMode: "",
			wantRet:  true,
		},
		{
			name:       "camelCase wins when both keys present",
			raw:        `{"retrievalConfig": {"camel": 1}, "retrieval_config": {"snake": 1}}`,
			wantMode:   "",
			wantRet:    true,
			wantSN:     "camel",
		},
		{
			name:       "functionCallingConfig preserved",
			raw:        `{"functionCallingConfig": {"mode": "AUTO", "allowedFunctionNames": ["f1"]}}`,
			wantMode:   "AUTO",
			wantRet:    false,
		},
		{
			name:       "combined functionCallingConfig and retrievalConfig",
			raw:        `{"functionCallingConfig": {"mode": "NONE"}, "retrievalConfig": {"disableAttribution": false}}`,
			wantMode:   "NONE",
			wantRet:    true,
		},
		{
			name:     "empty object",
			raw:      `{}`,
			wantMode: "",
			wantRet:  false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var tc ToolConfig
			if err := json.Unmarshal([]byte(c.raw), &tc); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if c.wantMode == "" {
				if tc.FunctionCallingConfig != nil {
					t.Fatalf("expected nil FunctionCallingConfig, got %v", tc.FunctionCallingConfig)
				}
			} else {
				if tc.FunctionCallingConfig == nil || tc.FunctionCallingConfig.Mode != c.wantMode {
					t.Fatalf("expected mode %q, got %v", c.wantMode, tc.FunctionCallingConfig)
				}
			}
			if !c.wantRet {
				if tc.RetrievalConfig != nil {
					t.Fatalf("expected nil RetrievalConfig, got %v", tc.RetrievalConfig)
				}
				return
			}
			if tc.RetrievalConfig == nil {
				t.Fatalf("expected non-nil RetrievalConfig")
			}
			if c.wantSN != "" {
				m, ok := tc.RetrievalConfig.(map[string]any)
				if !ok {
					t.Fatalf("expected map RetrievalConfig, got %T", tc.RetrievalConfig)
				}
				if _, ok := m[c.wantSN]; !ok {
					t.Fatalf("expected key %q in RetrievalConfig, got %v", c.wantSN, m)
				}
			}
		})
	}
}

func TestToolConfig_MarshalJSON_CamelCaseOnly(t *testing.T) {
	tc := &ToolConfig{
		FunctionCallingConfig: &FunctionCallingConfig{Mode: "AUTO"},
		RetrievalConfig:       map[string]any{"disableAttribution": true},
	}
	b, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, ok := m["retrievalConfig"]; !ok {
		t.Fatalf("expected camelCase retrievalConfig in marshal output, got %s", b)
	}
	if _, ok := m["retrieval_config"]; ok {
		t.Fatalf("snake_case key must not appear in marshal output, got %s", b)
	}
	if fcc, ok := m["functionCallingConfig"].(map[string]any); !ok || fcc["mode"] != "AUTO" {
		t.Fatalf("functionCallingConfig not preserved in marshal output, got %s", b)
	}
}

func TestToolConfig_UnmarshalJSON_CaseInsensitive(t *testing.T) {
	// 对齐 encoding/json 原语义：精确键缺失时大小写不敏感回退匹配
	cases := []struct {
		name     string
		raw      string
		wantMode string
		wantRet  bool
	}{
		{
			name:     "UPPERCASE functionCallingConfig matches",
			raw:      `{"FUNCTIONCALLINGCONFIG": {"mode": "AUTO"}}`,
			wantMode: "AUTO",
		},
		{
			name:    "UPPERCASE RETRIEVALCONFIG matches",
			raw:     `{"RETRIEVALCONFIG": {"disableAttribution": true}}`,
			wantRet: true,
		},
		{
			name:    "mixed case retrieval_config matches",
			raw:     `{"Retrieval_Config": {"googleSearchRetrieval": {}}}`,
			wantRet: true,
		},
		{
			name: "exact camelCase still wins over folded match",
			raw:  `{"retrievalConfig": {"camel": 1}, "RETRIEVALCONFIG": {"upper": 1}}`,
			wantRet: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var tc ToolConfig
			if err := json.Unmarshal([]byte(c.raw), &tc); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if c.wantMode == "" {
				if tc.FunctionCallingConfig != nil {
					t.Fatalf("expected nil FunctionCallingConfig, got %v", tc.FunctionCallingConfig)
				}
			} else if tc.FunctionCallingConfig == nil || tc.FunctionCallingConfig.Mode != c.wantMode {
				t.Fatalf("expected mode %q, got %v", c.wantMode, tc.FunctionCallingConfig)
			}
			if !c.wantRet {
				if tc.RetrievalConfig != nil {
					t.Fatalf("expected nil RetrievalConfig, got %v", tc.RetrievalConfig)
				}
				return
			}
			if tc.RetrievalConfig == nil {
				t.Fatalf("expected non-nil RetrievalConfig")
			}
			if c.name == "exact camelCase still wins over folded match" {
				m, ok := tc.RetrievalConfig.(map[string]any)
				if !ok {
					t.Fatalf("expected map RetrievalConfig, got %T", tc.RetrievalConfig)
				}
				if _, ok := m["camel"]; !ok {
					t.Fatalf("expected camelCase key to win, got %v", m)
				}
			}
		})
	}
}

func TestTool_JSONTagsCamelCase(t *testing.T) {
	// 固化 Tool 字段 tag 均为 camelCase 且与官方字段名一致（防止回归）
	tool := Tool{
		GoogleSearch:          GoogleSearch{},
		GoogleMaps:            GoogleMaps{},
		GoogleSearchRetrieval: GoogleSearch{},
		CodeExecution:         GoogleSearch{},
		Retrieval:             GoogleSearch{},
		FunctionDeclarations:  []FunctionDeclaration{{Name: "fn"}},
	}
	b, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	for _, key := range []string{
		"googleSearch", "googleMaps", "googleSearchRetrieval",
		"codeExecution", "retrieval", "functionDeclarations",
	} {
		if !jsonContains(b, key) {
			t.Fatalf("expected key %q in marshal output, got %s", key, b)
		}
	}
}

func jsonContains(b []byte, key string) bool {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}