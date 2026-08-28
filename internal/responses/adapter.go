// Package responses contains the protocol boundary for OpenAI Responses.
// It deliberately has no HTTP or database dependencies, so the same canonical
// request can be used by synchronous and streaming handlers.
package responses

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/transform"
)

type CreateRequest struct {
	Model              string                     `json:"model"`
	Input              any                        `json:"input"`
	Instructions       any                        `json:"instructions,omitempty"`
	Tools              []any                      `json:"tools,omitempty"`
	ToolChoice         any                        `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool                      `json:"parallel_tool_calls,omitempty"`
	MaxOutputTokens    any                        `json:"max_output_tokens,omitempty"`
	Reasoning          map[string]any             `json:"reasoning,omitempty"`
	Text               map[string]any             `json:"text,omitempty"`
	Stream             bool                       `json:"stream,omitempty"`
	Store              *bool                      `json:"store,omitempty"`
	Background         bool                       `json:"background,omitempty"`
	PreviousResponseID string                     `json:"previous_response_id,omitempty"`
	Conversation       any                        `json:"conversation,omitempty"`
	Metadata           map[string]any             `json:"metadata,omitempty"`
	Include            []string                   `json:"include,omitempty"`
	Temperature        any                        `json:"temperature,omitempty"`
	TopP               any                        `json:"top_p,omitempty"`
	ServiceTier        any                        `json:"service_tier,omitempty"`
	RawExtensions      map[string]json.RawMessage `json:"-"`
}

var requestFields = []string{"model", "input", "instructions", "tools", "tool_choice", "parallel_tool_calls", "max_output_tokens", "reasoning", "text", "stream", "store", "background", "previous_response_id", "conversation", "metadata", "include", "temperature", "top_p", "service_tier"}

func (r *CreateRequest) UnmarshalJSON(data []byte) error {
	type plain CreateRequest
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	known := make(map[string]bool, len(requestFields))
	for _, k := range requestFields {
		known[k] = true
	}
	unknown := make(map[string]json.RawMessage)
	for k, v := range fields {
		if !known[k] {
			unknown[k] = v
		}
	}
	*r = CreateRequest(p)
	r.RawExtensions = unknown
	return nil
}

func (r CreateRequest) UnknownFields() []string {
	out := make([]string, 0, len(r.RawExtensions))
	for k := range r.RawExtensions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type AdapterError struct{ Code, Param, Message string }

func (e *AdapterError) Error() string { return e.Message }

func NormalizeInput(input any) ([]any, error) {
	switch v := input.(type) {
	case string:
		return []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": v}}}}, nil
	case map[string]any:
		return []any{v}, nil
	case []any:
		return v, nil
	case nil:
		return nil, &AdapterError{Code: "missing_required_parameter", Param: "input", Message: "input is required"}
	default:
		return nil, &AdapterError{Code: "invalid_parameter", Param: "input", Message: "input must be a string, item, or item array"}
	}
}

func BuildGemini(r CreateRequest, history []any) (map[string]any, []any, error) {
	if strings.TrimSpace(r.Model) == "" {
		return nil, nil, &AdapterError{Code: "missing_required_parameter", Param: "model", Message: "model is required"}
	}
	items, err := NormalizeInput(r.Input)
	if err != nil {
		return nil, nil, err
	}
	all := append(append([]any{}, history...), items...)
	contents, err := itemsToContents(all)
	if err != nil {
		return nil, nil, err
	}
	p := map[string]any{"contents": contents}
	if parts := textParts(r.Instructions); len(parts) > 0 {
		p["systemInstruction"] = map[string]any{"parts": parts}
	}
	if len(r.Tools) > 0 {
		decl := make([]any, 0, len(r.Tools))
		for i, raw := range r.Tools {
			t, ok := raw.(map[string]any)
			if !ok {
				return nil, nil, &AdapterError{Code: "invalid_parameter", Param: fmt.Sprintf("tools[%d]", i), Message: "tool must be an object"}
			}
			typ := strings.ToLower(stringValue(t["type"]))
			if typ != "function" {
				return nil, nil, &AdapterError{Code: "unsupported_tool", Param: fmt.Sprintf("tools[%d].type", i), Message: "only function tools are supported"}
			}
			fn := t
			if x, ok := t["function"].(map[string]any); ok {
				fn = x
			}
			name := strings.TrimSpace(stringValue(fn["name"]))
			if name == "" {
				return nil, nil, &AdapterError{Code: "missing_required_parameter", Param: fmt.Sprintf("tools[%d].name", i), Message: "function tool name is required"}
			}
			d := map[string]any{"name": name}
			for _, k := range []string{"description", "parameters"} {
				if v, ok := fn[k]; ok {
					d[k] = v
				}
			}
			decl = append(decl, d)
		}
		p["tools"] = []any{map[string]any{"functionDeclarations": decl}}
	}
	if r.ToolChoice != nil {
		mode := strings.ToUpper(stringValue(r.ToolChoice))
		if mode == "REQUIRED" {
			mode = "ANY"
		}
		if mode == "AUTO" || mode == "ANY" || mode == "NONE" {
			p["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": mode}}
		} else if c, ok := r.ToolChoice.(map[string]any); ok {
			if f, ok := c["function"].(map[string]any); ok {
				c = f
			}
			if name := stringValue(c["name"]); name != "" {
				p["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY", "allowedFunctionNames": []any{name}}}
			} else {
				return nil, nil, &AdapterError{Code: "invalid_parameter", Param: "tool_choice", Message: "tool_choice function name is required"}
			}
		} else {
			return nil, nil, &AdapterError{Code: "invalid_parameter", Param: "tool_choice", Message: "unsupported tool_choice"}
		}
	}
	g := map[string]any{}
	if r.MaxOutputTokens != nil {
		g["maxOutputTokens"] = r.MaxOutputTokens
	}
	if r.Temperature != nil {
		g["temperature"] = r.Temperature
	}
	if r.TopP != nil {
		g["topP"] = r.TopP
	}
	if effort := strings.TrimSpace(stringValue(r.Reasoning["effort"])); effort != "" {
		allowed := map[string]bool{"low": true, "medium": true, "high": true, "minimal": true}
		if !allowed[strings.ToLower(effort)] {
			return nil, nil, &AdapterError{Code: "invalid_parameter", Param: "reasoning.effort", Message: "unsupported reasoning effort"}
		}
		g["thinkingConfig"] = map[string]any{"thinkingLevel": strings.ToUpper(effort)}
	}
	if f, ok := r.Text["format"].(map[string]any); ok {
		switch stringValue(f["type"]) {
		case "json_object":
			g["responseMimeType"] = "application/json"
		case "json_schema":
			g["responseMimeType"] = "application/json"
			if s, ok := f["schema"].(map[string]any); ok {
				g["responseSchema"] = s
			}
		default:
			return nil, nil, &AdapterError{Code: "invalid_parameter", Param: "text.format.type", Message: "unsupported text format"}
		}
	}
	if len(g) > 0 {
		p["generationConfig"] = g
	}
	transform.ApplyModelCapabilities(p, r.Model)
	if r.ServiceTier != nil {
		p["serviceTier"] = r.ServiceTier
	}
	return p, items, nil
}

func itemsToContents(items []any) ([]any, error) {
	out := []any{}
	calls := map[string]string{}
	for i, raw := range items {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, &AdapterError{Code: "invalid_item", Param: fmt.Sprintf("input[%d]", i), Message: "input item must be an object"}
		}
		typ := strings.ToLower(stringValue(m["type"]))
		if typ == "" {
			typ = "message"
		}
		switch typ {
		case "message":
			role := strings.ToLower(stringValue(m["role"]))
			if role == "" {
				role = "user"
			}
			if role == "assistant" {
				role = "model"
			}
			if role == "system" || role == "developer" {
				role = "user"
			}
			parts, partErr := responseContentParts(m["content"])
			if partErr != nil {
				return nil, partErr
			}
			if len(parts) > 0 {
				out = append(out, map[string]any{"role": role, "parts": parts})
			}
		case "input_text", "input_image", "input_file", "input_audio":
			part, partErr := responseContentPart(m)
			if partErr != nil {
				return nil, partErr
			}
			out = append(out, map[string]any{"role": "user", "parts": []any{part}})
		case "function_call":
			id := firstString(m["call_id"], m["id"])
			name := stringValue(m["name"])
			if id == "" || name == "" {
				return nil, &AdapterError{Code: "tool_call_identity_missing", Param: fmt.Sprintf("input[%d]", i), Message: "function_call requires call_id and name"}
			}
			calls[id] = name
			out = append(out, map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"id": id, "name": name, "args": normalizeArgs(m["arguments"])}}}})
		case "function_call_output":
			id := firstString(m["call_id"], m["id"])
			name := calls[id]
			if id == "" || name == "" {
				return nil, &AdapterError{Code: "tool_state_missing", Param: fmt.Sprintf("input[%d].call_id", i), Message: "function call output cannot be restored from local state"}
			}
			part := map[string]any{"functionResponse": map[string]any{"id": id, "name": name, "response": normalizeOutput(m["output"])}}
			if len(out) > 0 {
				if prev, ok := out[len(out)-1].(map[string]any); ok && prev["role"] == "function" {
					if ps, ok := prev["parts"].([]any); ok {
						prev["parts"] = append(ps, part)
						continue
					}
				}
			}
			out = append(out, map[string]any{"role": "function", "parts": []any{part}})
		default:
			return nil, &AdapterError{Code: "unsupported_input_item", Param: fmt.Sprintf("input[%d].type", i), Message: "input item type is not supported"}
		}
	}
	return out, nil
}

func textParts(v any) []any {
	switch x := v.(type) {
	case string:
		return []any{map[string]any{"text": x}}
	case []any:
		out := []any{}
		for _, raw := range x {
			m, _ := raw.(map[string]any)
			typ := strings.ToLower(stringValue(m["type"]))
			switch typ {
			case "text", "input_text", "output_text":
				out = append(out, map[string]any{"text": stringValue(m["text"])})
			case "input_image", "image_url":
				if part := mediaPart(m, false); part != nil {
					out = append(out, part)
				}
			case "input_file":
				if part := mediaPart(m, false); part != nil {
					out = append(out, part)
				}
			case "input_audio":
				if part := mediaPart(m, true); part != nil {
					out = append(out, part)
				}
			}
		}
		return out
	}
	return nil
}

func responseContentParts(v any) ([]any, error) {
	if s, ok := v.(string); ok {
		return []any{map[string]any{"text": s}}, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return textParts(v), nil
	}
	out := make([]any, 0, len(arr))
	for i, raw := range arr {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, &AdapterError{Code: "invalid_parameter", Param: fmt.Sprintf("input.content[%d]", i), Message: "content part must be an object"}
		}
		p, err := responseContentPart(m)
		if err != nil {
			return nil, err
		}
		if p != nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func responseContentPart(m map[string]any) (map[string]any, error) {
	typ := strings.ToLower(stringValue(m["type"]))
	switch typ {
	case "text", "input_text", "output_text":
		if s := stringValue(m["text"]); s != "" {
			return map[string]any{"text": s}, nil
		}
		return nil, &AdapterError{Code: "invalid_parameter", Param: "input.content.text", Message: "text is required"}
	case "input_image", "image_url":
		var u string
		switch x := m["image_url"].(type) {
		case string:
			u = x
		case map[string]any:
			u = firstString(x["url"], x["file_id"])
		}
		if u == "" {
			if x, ok := m["input_image"].(string); ok {
				u = x
			}
			if x, ok := m["input_image"].(map[string]any); ok {
				u = firstString(x["url"], x["file_id"])
			}
		}
		if u == "" {
			u = firstString(m["file_id"], m["file_url"], m["url"])
		}
		if strings.HasPrefix(strings.ToLower(u), "data:") {
			if i := strings.Index(u, ","); i > 5 {
				meta, data := u[5:i], u[i+1:]
				mime := strings.Split(meta, ";")[0]
				return map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": transform.NormalizeBase64(data)}}, nil
			}
		}
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "gs://") {
			return map[string]any{"fileData": map[string]any{"mimeType": "image/*", "fileUri": u}}, nil
		}
		return nil, &AdapterError{Code: "invalid_parameter", Param: "input.content.image", Message: "image URL is required"}
	case "input_audio":
		a, _ := m["input_audio"].(map[string]any)
		if a == nil {
			a = m
		}
		data := stringValue(a["data"])
		if data == "" {
			return nil, &AdapterError{Code: "invalid_parameter", Param: "input.content.input_audio", Message: "audio data is required"}
		}
		mime := "audio/wav"
		if mt := firstString(a["mime_type"], a["mimeType"]); mt != "" {
			mime = mt
		}
		if f := stringValue(a["format"]); f != "" {
			mime = "audio/" + strings.ToLower(f)
		}
		return map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": transform.NormalizeBase64(data)}}, nil
	case "input_file", "file":
		u := firstString(m["file_id"], m["file_url"], m["file_uri"], m["uri"], m["url"])
		if u != "" {
			return map[string]any{"fileData": map[string]any{"mimeType": "application/octet-stream", "fileUri": u}}, nil
		}
		return nil, &AdapterError{Code: "unsupported_input_item", Param: "input.content.type", Message: "input content type is not supported"}
	}
	return nil, &AdapterError{Code: "unsupported_input_item", Param: "input.content.type", Message: "input content type is not supported"}
}

// mediaPart converts Responses multimodal content blocks to Gemini parts.
// Remote image/file references become fileData; inline audio becomes inlineData.
func mediaPart(m map[string]any, audio bool) map[string]any {
	if audio {
		h := m["input_audio"]
		if h == nil {
			h = m
		}
		if hm, ok := h.(map[string]any); ok {
			data := stringValue(hm["data"])
			if data == "" {
				data = stringValue(m["data"])
			}
			if data != "" {
				mime := firstString(hm["mime_type"], hm["mimeType"], m["mime_type"], "audio/wav")
				return map[string]any{"inlineData": map[string]any{"data": data, "mimeType": mime}}
			}
		}
		return nil
	}
	uri := firstString(m["file_id"], m["file_url"], m["image_url"], m["url"])
	if holder, ok := m["image_url"].(map[string]any); ok {
		uri = firstString(holder["url"], holder["file_id"], uri)
	}
	if uri == "" {
		return nil
	}
	if strings.HasPrefix(uri, "data:") {
		if i := strings.Index(uri, ";base64,"); i > 5 {
			return map[string]any{"inlineData": map[string]any{"mimeType": uri[5:i], "data": uri[i+8:]}}
		}
	}
	mime := firstString(m["mime_type"], m["mimeType"])
	if mime == "" {
		mime = "application/octet-stream"
		lower := strings.ToLower(uri)
		if strings.Contains(lower, ".png") {
			mime = "image/png"
		} else if strings.Contains(lower, ".jpg") || strings.Contains(lower, ".jpeg") {
			mime = "image/jpeg"
		} else if strings.Contains(lower, ".pdf") {
			mime = "application/pdf"
		}
	}
	return map[string]any{"fileData": map[string]any{"fileUri": uri, "mimeType": mime}}
}
func stringValue(v any) string { s, _ := v.(string); return s }
func firstString(v ...any) string {
	for _, x := range v {
		if s := stringValue(x); s != "" {
			return s
		}
	}
	return ""
}
func normalizeArgs(v any) any {
	if s, ok := v.(string); ok {
		var x any
		if json.Unmarshal([]byte(s), &x) == nil {
			return x
		}
		return map[string]any{"raw": s}
	}
	if v == nil {
		return map[string]any{}
	}
	return v
}
func normalizeOutput(v any) any {
	if v == nil {
		return map[string]any{"output": ""}
	}
	if s, ok := v.(string); ok {
		return map[string]any{"output": s}
	}
	return v
}

func OutputItems(resp map[string]any) ([]any, map[string]any, string) {
	items := []any{}
	status := "completed"
	responseID := stringValue(resp["responseId"])
	if responseID == "" {
		responseID = "unknown"
	}
	for _, raw := range responseCandidates(resp) {
		c, _ := raw.(map[string]any)
		content, _ := c["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		var text strings.Builder
		for _, rawp := range parts {
			p, _ := rawp.(map[string]any)
			if s := stringValue(p["text"]); s != "" && !truthy(p["thought"]) {
				text.WriteString(s)
			}
			if call, ok := p["functionCall"].(map[string]any); ok {
				args, _ := json.Marshal(call["args"])
				id := firstString(call["id"], call["call_id"])
				if id == "" {
					id = fmt.Sprintf("call_%d", len(items))
				}
				items = append(items, map[string]any{"id": "fc_" + id, "type": "function_call", "status": "completed", "call_id": id, "name": stringValue(call["name"]), "arguments": string(args)})
			}
		}
		if text.Len() > 0 {
			items = append(items, map[string]any{"id": "msg_" + responseID, "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text.String(), "annotations": []any{}}}})
		}
		finish := strings.ToUpper(stringValue(c["finishReason"]))
		if finish != "" && finish != "STOP" && finish != "FINISH_REASON_UNSPECIFIED" {
			status = "incomplete"
		}
	}
	usage := map[string]any{}
	if m, ok := resp["usageMetadata"].(map[string]any); ok {
		u := transform.ConvertUsage(m)
		usage = map[string]any{"input_tokens": u["prompt_tokens"], "output_tokens": u["completion_tokens"], "total_tokens": u["total_tokens"]}
		for k, v := range m {
			usage[k] = v
		}
	}
	return items, usage, status
}
func responseCandidates(resp map[string]any) []any { v, _ := resp["candidates"].([]any); return v }
func truthy(v any) bool                            { b, _ := v.(bool); return b }
