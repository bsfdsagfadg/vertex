package transform

// IsToolEmpty 严格检查 Tool 是否无任何已初始化的有效工具分支：
// 所有内置工具标记均为 nil 且无 FunctionDeclarations 时判定为空。
func IsToolEmpty(t Tool) bool {
	return t.GoogleSearch == nil &&
		t.GoogleMaps == nil &&
		t.GoogleSearchRetrieval == nil &&
		len(t.FunctionDeclarations) == 0 &&
		t.CodeExecution == nil &&
		t.Retrieval == nil &&
		t.URLContext == nil &&
		t.ComputerUse == nil &&
		t.MCPTool == nil &&
		t.FileSearch == nil
}

// FilterEmptyTools 过滤切片中所有空 Tool（IsToolEmpty == true）。
// 若过滤后长度为 0，返回 nil（收敛空切片为 nil，防止上游 Proto 序列化歧义）。
func FilterEmptyTools(tools []Tool) []Tool {
	var out []Tool
	for _, t := range tools {
		if !IsToolEmpty(t) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
