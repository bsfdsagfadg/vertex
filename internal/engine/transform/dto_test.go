package transform

import (
	"encoding/json"
	"testing"
)

func TestToolConfig_UnmarshalJSON_DualAlias(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantMode string
		wantRet  bool
		wantSN   string
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
			name:     "camelCase wins when both keys present",
			raw:      `{"retrievalConfig": {"camel": 1}, "retrieval_config": {"snake": 1}}`,
			wantMode: "",
			wantRet:  true,
			wantSN:   "camel",
		},
		{
			name:     "functionCallingConfig preserved",
			raw:      `{"functionCallingConfig": {"mode": "AUTO", "allowedFunctionNames": ["f1"]}}`,
			wantMode: "AUTO",
			wantRet:  false,
		},
		{
			name:     "combined functionCallingConfig and retrievalConfig",
			raw:      `{"functionCallingConfig": {"mode": "NONE"}, "retrievalConfig": {"disableAttribution": false}}`,
			wantMode: "NONE",
			wantRet:  true,
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
			name:    "exact camelCase still wins over folded match",
			raw:     `{"retrievalConfig": {"camel": 1}, "RETRIEVALCONFIG": {"upper": 1}}`,
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

func TestInlineData_UnmarshalJSON_DualAlias(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantType string
		wantData string
	}{
		{
			name:     "snake_case with data uri prefix",
			raw:      `{"mime_type": "image/jpeg", "data": "data:image/jpeg;base64,YWJj"}`,
			wantType: "image/jpeg",
			wantData: "YWJj",
		},
		{
			name:     "camelCase wins over snake_case",
			raw:      `{"mimeType": "image/png", "mime_type": "image/jpeg", "data": "YQ=="}`,
			wantType: "image/png",
			wantData: "YQ==",
		},
		{
			name:     "case insensitive fold",
			raw:      `{"MIME_TYPE": "image/gif", "DATA": "Z2lm"}`,
			wantType: "image/gif",
			wantData: "Z2lm",
		},
		{
			name:     "interior whitespace stripped",
			raw:      `{"mimeType": "image/png", "data": "YW Jj"}`,
			wantType: "image/png",
			wantData: "YWJj",
		},
		{
			name:     "empty object",
			raw:      `{}`,
			wantType: "",
			wantData: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var d InlineData
			if err := json.Unmarshal([]byte(c.raw), &d); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if d.MimeType != c.wantType || d.Data != c.wantData {
				t.Errorf("got %+v，期望 MimeType=%q Data=%q", d, c.wantType, c.wantData)
			}
		})
	}
}

func TestFileData_UnmarshalJSON_DualAlias(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantURI  string
		wantType string
	}{
		{
			name:     "snake_case",
			raw:      `{"file_uri": "gs://bucket/file.mp3", "mime_type": "audio/mp3"}`,
			wantURI:  "gs://bucket/file.mp3",
			wantType: "audio/mp3",
		},
		{
			name:     "camelCase wins over snake_case",
			raw:      `{"fileUri": "gs://bucket/a.mp3", "file_uri": "gs://bucket/b.mp3", "mimeType": "audio/wav"}`,
			wantURI:  "gs://bucket/a.mp3",
			wantType: "audio/wav",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var d FileData
			if err := json.Unmarshal([]byte(c.raw), &d); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if d.FileURI != c.wantURI || d.MimeType != c.wantType {
				t.Errorf("got %+v，期望 FileURI=%q MimeType=%q", d, c.wantURI, c.wantType)
			}
		})
	}
}

func TestPart_UnmarshalJSON_DualAlias(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantDat string
		wantURI string
		wantSig string
	}{
		{
			name:    "snake_case full aliases",
			raw:     `{"inline_data": {"mime_type": "image/jpeg", "data": "data:image/jpeg;base64,YWJj"}, "file_data": {"file_uri": "gs://b/f.mp3", "mime_type": "audio/mp3"}, "thought_signature": "c2ln"}`,
			wantDat: "YWJj",
			wantURI: "gs://b/f.mp3",
			wantSig: "c2ln",
		},
		{
			name:    "camelCase wins over snake_case",
			raw:     `{"inlineData": {"mimeType": "image/png", "data": "YQ=="}, "inline_data": {"mime_type": "image/jpeg", "data": "YmJi"}}`,
			wantDat: "YQ==",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p Part
			if err := json.Unmarshal([]byte(c.raw), &p); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if c.wantDat != "" && (p.InlineData == nil || p.InlineData.Data != c.wantDat) {
				t.Errorf("inlineData=%v，期望 data=%q", p.InlineData, c.wantDat)
			}
			if c.wantURI != "" && (p.FileData == nil || p.FileData.FileURI != c.wantURI) {
				t.Errorf("fileData=%v，期望 fileUri=%q", p.FileData, c.wantURI)
			}
			if c.wantSig != "" && p.ThoughtSignature != c.wantSig {
				t.Errorf("thoughtSignature=%q，期望 %q", p.ThoughtSignature, c.wantSig)
			}
		})
	}
}

func TestPart_UnmarshalJSON_ToolFieldAliases(t *testing.T) {
	raw := `{
		"function_call": {"name": "fn", "args": {"a": 1}, "id": "call-1"},
		"function_response": {"name": "fn", "response": {"result": 42}, "id": "resp-1"},
		"executable_code": {"code": "print(1)", "codeLanguage": "PYTHON"},
		"code_execution_result": {"output": "1", "outcome": "OUTCOME_OK"},
		"video_metadata": {"startOffset": {"seconds": 0}}
	}`
	var p Part
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if p.FunctionCall == nil || p.FunctionCall.Name != "fn" || p.FunctionCall.ID != "call-1" {
		t.Errorf("function_call 别名丢失: %+v", p.FunctionCall)
	}
	if p.FunctionResponse == nil || p.FunctionResponse.Name != "fn" || p.FunctionResponse.ID != "resp-1" {
		t.Errorf("function_response 别名丢失: %+v", p.FunctionResponse)
	}
	if p.ExecutableCode == nil || p.ExecutableCode.Code != "print(1)" || p.ExecutableCode.CodeLanguage != "PYTHON" {
		t.Errorf("executable_code 别名丢失: %+v", p.ExecutableCode)
	}
	if p.CodeExecutionResult == nil || p.CodeExecutionResult.Output != "1" || p.CodeExecutionResult.Outcome != "OUTCOME_OK" {
		t.Errorf("code_execution_result 别名丢失: %+v", p.CodeExecutionResult)
	}
	if p.VideoMetadata == nil {
		t.Error("video_metadata 别名丢失")
	}
}

func TestPart_MarshalJSON_CamelCaseOnly(t *testing.T) {
	p := Part{
		InlineData: &InlineData{MimeType: "image/png", Data: "YQ=="},
		FileData:   &FileData{FileURI: "gs://b/f.mp3", MimeType: "audio/mp3"},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	inline, ok := m["inlineData"].(map[string]any)
	if !ok {
		t.Fatalf("期望 inlineData 键: %s", b)
	}
	if inline["mimeType"] != "image/png" || inline["data"] != "YQ==" {
		t.Errorf("inlineData 输出异常: %v", inline)
	}
	if _, has := inline["mime_type"]; has {
		t.Errorf("snake_case 键 mime_type 不得出现在输出中: %s", b)
	}
	file, ok := m["fileData"].(map[string]any)
	if !ok {
		t.Fatalf("期望 fileData 键: %s", b)
	}
	if file["fileUri"] != "gs://b/f.mp3" || file["mimeType"] != "audio/mp3" {
		t.Errorf("fileData 输出异常: %v", file)
	}
	if _, has := file["file_uri"]; has {
		t.Errorf("snake_case 键 file_uri 不得出现在输出中: %s", b)
	}
}

func TestPart_AllFieldsRoundTrip(t *testing.T) {
	// golden 测试：防 custom unmarshaler drift。
	// Part 全部 11 个字段必须完整透传，任何新增/遗漏字段都会在此暴露。
	raw := `{
		"text": "hello",
		"thought": true,
		"thoughtSignature": "c2ln",
		"inlineData": {"mimeType": "image/png", "data": "aGVsbG8="},
		"fileData": {"fileUri": "gs://bucket/file.mp3", "mimeType": "audio/mp3"},
		"functionCall": {"name": "fn", "args": {"a": 1}, "id": "call-1"},
		"functionResponse": {"name": "fn", "response": {"result": 42}, "id": "resp-1"},
		"executableCode": {"code": "print(1)", "codeLanguage": "PYTHON"},
		"codeExecutionResult": {"output": "1", "outcome": "OUTCOME_OK"},
		"videoMetadata": {"startOffset": {"seconds": 0}},
		"mediaResolution": "LOW"
	}`
	var p Part
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if p.Text != "hello" {
		t.Errorf("text 丢失: %q", p.Text)
	}
	if !p.Thought {
		t.Error("thought 丢失")
	}
	if p.ThoughtSignature != "c2ln" {
		t.Errorf("thoughtSignature 丢失: %q", p.ThoughtSignature)
	}
	if p.InlineData == nil || p.InlineData.MimeType != "image/png" || p.InlineData.Data != "aGVsbG8=" {
		t.Errorf("inlineData 丢失或损坏: %+v", p.InlineData)
	}
	if p.FileData == nil || p.FileData.FileURI != "gs://bucket/file.mp3" || p.FileData.MimeType != "audio/mp3" {
		t.Errorf("fileData 丢失或损坏: %+v", p.FileData)
	}
	if p.FunctionCall == nil || p.FunctionCall.Name != "fn" || p.FunctionCall.ID != "call-1" {
		t.Errorf("functionCall 丢失或损坏: %+v", p.FunctionCall)
	} else if args, ok := p.FunctionCall.Args.(map[string]any); !ok || args["a"] != float64(1) {
		t.Errorf("functionCall.args 丢失或损坏: %+v", p.FunctionCall.Args)
	}
	if p.FunctionResponse == nil || p.FunctionResponse.Name != "fn" || p.FunctionResponse.ID != "resp-1" {
		t.Errorf("functionResponse 丢失或损坏: %+v", p.FunctionResponse)
	} else if resp, ok := p.FunctionResponse.Response.(map[string]any); !ok || resp["result"] != float64(42) {
		t.Errorf("functionResponse.response 丢失或损坏: %+v", p.FunctionResponse.Response)
	}
	if p.ExecutableCode == nil || p.ExecutableCode.Code != "print(1)" || p.ExecutableCode.CodeLanguage != "PYTHON" {
		t.Errorf("executableCode 丢失或损坏: %+v", p.ExecutableCode)
	}
	if p.CodeExecutionResult == nil || p.CodeExecutionResult.Output != "1" || p.CodeExecutionResult.Outcome != "OUTCOME_OK" {
		t.Errorf("codeExecutionResult 丢失或损坏: %+v", p.CodeExecutionResult)
	}
	if p.VideoMetadata == nil {
		t.Error("videoMetadata 丢失")
	} else if vm, ok := p.VideoMetadata.(map[string]any); !ok {
		t.Errorf("videoMetadata 类型异常: %T", p.VideoMetadata)
	} else if so, ok := vm["startOffset"].(map[string]any); !ok || so["seconds"] != float64(0) {
		t.Errorf("videoMetadata.startOffset 丢失或损坏: %+v", vm)
	}
	if p.MediaResolution != "LOW" {
		t.Errorf("mediaResolution 丢失: %q", p.MediaResolution)
	}
}
