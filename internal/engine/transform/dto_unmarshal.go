package transform

import "encoding/json"

// 本文件是 DTO 反序列化契约层：别名兼容（snake_case / 大小写折叠）与
// inlineData.data Base64 自动规范化（宽进严出，业务层消费纯净模型）。
//
// 注意：Part / InlineData 为请求/响应共用 DTO，响应侧反序列化
// （media_typed.go ExtractImagesTyped / ExtractAudioTyped）同样会触发归一。
// NormalizeBase64 幂等且上游输出本就是标准 base64，响应侧归一无害。

// 结构体字段别名表：canonical → snake_case 候选（可多个）。
// 声明式维护，杜绝逐字段手写 if/else 链（custom unmarshaler drift）。
//
//nolint:gochecknoglobals // Read-only alias tables
var (
	inlineDataAliases = map[string][]string{
		"mimeType": {"mime_type"},
	}
	fileDataAliases = map[string][]string{
		"fileUri":  {"file_uri"},
		"mimeType": {"mime_type"},
	}
	partAliases = map[string][]string{
		"inlineData":          {"inline_data"},
		"fileData":            {"file_data"},
		"thoughtSignature":    {"thought_signature"},
		"mediaResolution":     {"media_resolution"},
		"functionCall":        {"function_call"},
		"functionResponse":    {"function_response"},
		"executableCode":      {"executable_code"},
		"codeExecutionResult": {"code_execution_result"},
		"videoMetadata":       {"video_metadata"},
	}
	toolAliases = map[string][]string{
		"functionDeclarations":  {"function_declarations"},
		"googleSearch":          {"google_search"},
		"googleSearchRetrieval": {"google_search_retrieval"},
		"codeExecution":         {"code_execution"},
		"retrieval":             {"retrieval"},
		"googleMaps":            {"google_maps"},
		"urlContext":            {"url_context"},
		"computerUse":           {"computer_use"},
		"mcpServer":             {"mcp_server", "mcpTool", "mcp_tool"},
		"fileSearch":            {"file_search"},
	}
)

// unmarshalWithAliases 反序列化 + 别名归一：
// 1) 解入 map[string]json.RawMessage（值保留原始字节，非 map[string]any 树）；
// 2) canonical 键缺失时按候选键 + findFoldKey 大小写不敏感回退注入；
// 3) 回封字节后反序列化进强类型 dst（嵌套对象的自定义 UnmarshalJSON 自动触发）。
// 最终产物仍为强类型 struct，符合"零 map 往返"铁律。
func unmarshalWithAliases(raw []byte, dst any, aliases map[string][]string) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	for canonical, alts := range aliases {
		if _, ok := m[canonical]; !ok {
			if v := findFoldKey(m, append([]string{canonical}, alts...)...); v != nil {
				m[canonical] = v
			}
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// UnmarshalJSON 兼容 mimeType / mime_type 双键名反序列化，并对 data 自动执行
// NormalizeBase64 清洗（Data URI 前缀剥离 / URL-safe 还原 / padding 补齐 / 空白剥离）。
func (d *InlineData) UnmarshalJSON(data []byte) error {
	type alias InlineData
	var out alias
	if err := unmarshalWithAliases(data, &out, inlineDataAliases); err != nil {
		return err
	}
	out.Data = NormalizeBase64(out.Data)
	*d = InlineData(out)
	return nil
}

// UnmarshalJSON 兼容 fileUri / file_uri、mimeType / mime_type 双键名反序列化。
func (d *FileData) UnmarshalJSON(data []byte) error {
	type alias FileData
	var out alias
	if err := unmarshalWithAliases(data, &out, fileDataAliases); err != nil {
		return err
	}
	*d = FileData(out)
	return nil
}

// UnmarshalJSON 兼容 Part 全部可变体字段的 snake_case 别名反序列化：
// inlineData / inline_data、fileData / file_data、thoughtSignature / thought_signature、
// mediaResolution / media_resolution、functionCall / function_call、
// functionResponse / function_response、executableCode / executable_code、
// codeExecutionResult / code_execution_result、videoMetadata / video_metadata。
// 通过 alias 全量透传 Part 全部 11 个字段（text / thought / thoughtSignature / inlineData /
// fileData / functionCall / functionResponse / executableCode / codeExecutionResult /
// videoMetadata / mediaResolution），杜绝手写分支静默丢弃字段（custom unmarshaler drift）。
func (p *Part) UnmarshalJSON(data []byte) error {
	type alias Part
	var out alias
	if err := unmarshalWithAliases(data, &out, partAliases); err != nil {
		return err
	}
	*p = Part(out)
	return nil
}

// UnmarshalJSON 兼容 Tool 全部可变体字段的 snake_case 别名反序列化：
// googleSearch / google_search、googleMaps / google_maps、
// functionDeclarations / function_declarations、codeExecution / code_execution、
// retrieval / retrieval、googleSearchRetrieval / google_search_retrieval、
// urlContext / url_context、computerUse / computer_use、
// mcpServer / mcp_server / mcpTool、fileSearch / file_search。
func (t *Tool) UnmarshalJSON(data []byte) error {
	type alias Tool
	var out alias
	if err := unmarshalWithAliases(data, &out, toolAliases); err != nil {
		return err
	}
	*t = Tool(out)
	return nil
}
