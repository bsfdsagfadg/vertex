package transform

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/domain"
)

// ToolCallCoordinator 集中管理工具声明转换、ToolChoice 校验、多轮对话工具调用 ID 映射与结果配对。
type ToolCallCoordinator struct {
	toolIDToName           map[string]string
	declaredToolNames      map[string]bool
	lastModelFunctionCalls []string
	responseIndex          int
}

// NewToolCallCoordinator 创建新的工具调用协调器。
func NewToolCallCoordinator() *ToolCallCoordinator {
	return &ToolCallCoordinator{
		toolIDToName:      make(map[string]string),
		declaredToolNames: make(map[string]bool),
	}
}

// ConvertTools 将 OpenAI Tools 转为 Gemini Tools 并提取已声明函数名。
func (c *ToolCallCoordinator) ConvertTools(oaiTools []domain.Tool, legacyFunctions []any) ([]domain.GeminiTool, error) {
	var funcDecls []domain.FunctionDeclaration

	for _, t := range oaiTools {
		f := t.Function
		name := strings.TrimSpace(f.Name)
		if name == "" {
			continue
		}
		c.declaredToolNames[name] = true

		decl := domain.FunctionDeclaration{
			Name:        name,
			Description: f.Description,
		}

		if len(f.Parameters) > 0 {
			if cleaned, ok := CleanFunctionParameters(f.Parameters).(map[string]any); ok {
				decl.Parameters = cleaned
			} else {
				decl.Parameters = f.Parameters
			}
		} else {
			decl.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		funcDecls = append(funcDecls, decl)
	}

	// 兼容顶层已废弃的 functions 字段
	if len(funcDecls) == 0 && len(legacyFunctions) > 0 {
		for _, rawFn := range legacyFunctions {
			fnMap := extractOAIFunctionTool(rawFn)
			if fnMap == nil {
				continue
			}
			name := strings.TrimSpace(toString(fnMap["name"]))
			if name == "" {
				continue
			}
			c.declaredToolNames[name] = true
			decl := domain.FunctionDeclaration{
				Name: name,
			}
			if desc, ok := fnMap["description"].(string); ok {
				decl.Description = desc
			}
			if params, ok := fnMap["parameters"].(map[string]any); ok && len(params) > 0 {
				if cleaned, ok := CleanFunctionParameters(params).(map[string]any); ok {
					decl.Parameters = cleaned
				} else {
					decl.Parameters = params
				}
			} else {
				decl.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			funcDecls = append(funcDecls, decl)
		}
	}

	if len(funcDecls) == 0 {
		return nil, nil
	}

	return []domain.GeminiTool{
		{
			FunctionDeclarations: funcDecls,
		},
	}, nil
}

// ConvertToolChoice 将 tool_choice (或 legacy function_call) 转换为 Gemini ToolConfig。
func (c *ToolCallCoordinator) ConvertToolChoice(toolChoice any) (*domain.ToolConfig, error) {
	if toolChoice == nil || !isTruthy(toolChoice) {
		return nil, nil
	}

	switch v := toolChoice.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "none":
			return &domain.ToolConfig{
				FunctionCallingConfig: &domain.FunctionCallingConfig{Mode: "NONE"},
			}, nil
		case "auto":
			return &domain.ToolConfig{
				FunctionCallingConfig: &domain.FunctionCallingConfig{Mode: "AUTO"},
			}, nil
		case "required":
			if len(c.declaredToolNames) == 0 {
				return nil, fmt.Errorf("tool_choice='required' requires at least one tool")
			}
			return &domain.ToolConfig{
				FunctionCallingConfig: &domain.FunctionCallingConfig{Mode: "ANY"},
			}, nil
		}

	case map[string]any:
		var fnName string
		if v["type"] == "function" {
			if fn, ok := v["function"].(map[string]any); ok {
				fnName, _ = fn["name"].(string)
			}
		} else if n, ok := v["name"].(string); ok {
			fnName = n
		}
		if fnName != "" {
			if len(c.declaredToolNames) > 0 && !c.declaredToolNames[fnName] {
				return nil, fmt.Errorf("tool_choice references unknown function: %s", fnName)
			}
			return &domain.ToolConfig{
				FunctionCallingConfig: &domain.FunctionCallingConfig{
					Mode:                 "ANY",
					AllowedFunctionNames: []string{fnName},
				},
			}, nil
		}
	}

	return nil, nil
}

// RegisterAssistantToolCalls 记录 assistant 消息中产生的 tool calls，维护 ID 映射表。
func (c *ToolCallCoordinator) RegisterAssistantToolCalls(toolCalls []domain.ToolCall) []domain.Part {
	c.lastModelFunctionCalls = nil
	c.responseIndex = 0

	var parts []domain.Part
	for _, tc := range toolCalls {
		name := strings.TrimSpace(tc.Function.Name)
		if name == "" {
			continue
		}
		id := strings.TrimSpace(tc.ID)
		if id != "" {
			c.toolIDToName[id] = name
		}
		c.lastModelFunctionCalls = append(c.lastModelFunctionCalls, name)

		args := CoerceFunctionArgs(tc.Function.Arguments)
		argsMap, ok := args.(map[string]any)
		if !ok {
			argsMap = map[string]any{"raw": args}
		}

		parts = append(parts, domain.Part{
			FunctionCall: &domain.GeminiFunctionCall{
				Name: name,
				Args: argsMap,
				ID:   id,
			},
		})
	}
	return parts
}

// PairToolResponse 配对 tool 角色消息为 Gemini FunctionResponse Part。
func (c *ToolCallCoordinator) PairToolResponse(toolCallID, name string, content any) domain.Part {
	resolvedName := strings.TrimSpace(name)
	if resolvedName == "" && toolCallID != "" {
		resolvedName = c.toolIDToName[toolCallID]
	}
	if resolvedName == "" && c.responseIndex < len(c.lastModelFunctionCalls) {
		resolvedName = c.lastModelFunctionCalls[c.responseIndex]
	}
	if resolvedName == "" {
		resolvedName = "unknown"
	}
	c.responseIndex++

	respMap := CoerceFunctionResponse(content)
	return domain.Part{
		FunctionResponse: &domain.GeminiFunctionResponse{
			Name:     resolvedName,
			Response: respMap,
			ID:       toolCallID,
		},
	}
}

// PairFunctionResponse 配对 legacy function 角色消息。
func (c *ToolCallCoordinator) PairFunctionResponse(name string, content any) domain.Part {
	resolvedName := strings.TrimSpace(name)
	if resolvedName == "" {
		resolvedName = "unknown"
	}
	respMap := CoerceFunctionResponse(content)
	return domain.Part{
		FunctionResponse: &domain.GeminiFunctionResponse{
			Name:     resolvedName,
			Response: respMap,
		},
	}
}

// CoerceFunctionArgs 将 tool_call.arguments 规范成 map[string]any 或结构体。
func CoerceFunctionArgs(args any) any {
	if s, ok := args.(string); ok {
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			return parsed
		}
		return map[string]any{"raw": s}
	}
	if args == nil {
		return map[string]any{}
	}
	return args
}

// CoerceFunctionResponse 将 tool/function 角色的 content 规范成 Gemini functionResponse.response。
func CoerceFunctionResponse(raw any) map[string]any {
	obj := raw
	if s, ok := raw.(string); ok {
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			obj = parsed
		} else {
			obj = map[string]any{"result": s}
		}
	}
	if m, ok := obj.(map[string]any); ok {
		return m
	}
	return map[string]any{"result": obj}
}

