package transform

import (
	"encoding/base64"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/domain"
)

func TestTypedConvertChatRequest_Complete(t *testing.T) {
	cfg := config.StaticProvider(config.DefaultConfig())
	temp := 0.7
	topP := 0.95
	topK := 50
	seed := int64(42)
	maxTokens := 2048
	logprobs := true
	topLogprobs := 3

	req := &domain.ChatCompletionRequest{
		Model: "gemini-2.5-flash",
		Messages: []domain.ChatMessage{
			{
				Role:    "system",
				Content: "You are an AI assistant.",
			},
			{
				Role: "user",
				Content: []domain.MessageContentPart{
					{
						Type: "text",
						Text: "Look at this image and answer the question.",
					},
					{
						Type: "image_url",
						ImageURL: &domain.ImageURLPart{
							URL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
						},
					},
					{
						Type: "input_audio",
						InputAudio: &domain.InputAudioPart{
							Data:   "QUFB",
							Format: "mp3",
						},
					},
				},
			},
			{
				Role:    "assistant",
				Content: "Let me check the weather first.",
				ToolCalls: []domain.ToolCall{
					{
						ID:   "call_weather_1",
						Type: "function",
						Function: domain.FunctionCall{
							Name:      "get_weather",
							Arguments: `{"location":"San Francisco, CA"}`,
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: "call_weather_1",
				Content:    `{"temperature": 65, "condition": "Sunny"}`,
			},
		},
		Tools: []domain.Tool{
			{
				Type: "function",
				Function: domain.FunctionDeclaration{
					Name:        "get_weather",
					Description: "Get the current weather for a location.",
					Parameters: map[string]any{
						"$schema":              "http://json-schema.org/draft-07/schema#",
						"additionalProperties": false,
						"type":                 "object",
						"properties": map[string]any{
							"location": map[string]any{
								"type":        "string",
								"description": "City and state, e.g. San Francisco, CA",
							},
						},
						"required": []any{"location"},
					},
				},
			},
		},
		ToolChoice:      "auto",
		Temperature:     &temp,
		TopP:            &topP,
		TopK:            &topK,
		Seed:            &seed,
		MaxTokens:       &maxTokens,
		Logprobs:        &logprobs,
		TopLogprobs:     &topLogprobs,
		ReasoningEffort: "high",
		SafetySettings: []domain.SafetySetting{
			{
				Category:  "HARM_CATEGORY_HARASSMENT",
				Threshold: "BLOCK_NONE",
			},
		},
	}

	model, genReq, err := ConvertChatRequest(req, cfg)
	if err != nil {
		t.Fatalf("ConvertChatRequest returned error: %v", err)
	}

	if model != "gemini-2.5-flash" {
		t.Errorf("model = %q, want gemini-2.5-flash", model)
	}

	if genReq == nil {
		t.Fatal("genReq is nil")
	}

	// 1. System instruction
	if genReq.SystemInstruction == nil || len(genReq.SystemInstruction.Parts) == 0 {
		t.Fatal("expected SystemInstruction to be populated")
	}
	if genReq.SystemInstruction.Parts[0].Text != "You are an AI assistant." {
		t.Errorf("SystemInstruction = %q, want 'You are an AI assistant.'", genReq.SystemInstruction.Parts[0].Text)
	}

	// 2. Contents turns
	if len(genReq.Contents) != 3 {
		t.Fatalf("expected 3 contents turns (user, model, function), got %d", len(genReq.Contents))
	}

	// Turn 0: User multimodal parts
	userTurn := genReq.Contents[0]
	if userTurn.Role != "user" {
		t.Errorf("turn[0].Role = %q, want 'user'", userTurn.Role)
	}
	if len(userTurn.Parts) != 3 {
		t.Fatalf("turn[0] parts count = %d, want 3", len(userTurn.Parts))
	}
	if userTurn.Parts[0].Text != "Look at this image and answer the question." {
		t.Errorf("turn[0].parts[0].Text mismatch")
	}
	if userTurn.Parts[1].InlineData == nil || userTurn.Parts[1].InlineData.MimeType != "image/png" {
		t.Errorf("turn[0].parts[1].InlineData mismatch: %#v", userTurn.Parts[1].InlineData)
	}
	if userTurn.Parts[2].InlineData == nil || userTurn.Parts[2].InlineData.MimeType != "audio/mpeg" {
		t.Errorf("turn[0].parts[2].InlineData audio mismatch: %#v", userTurn.Parts[2].InlineData)
	}

	// Turn 1: Model tool call
	modelTurn := genReq.Contents[1]
	if modelTurn.Role != "model" {
		t.Errorf("turn[1].Role = %q, want 'model'", modelTurn.Role)
	}
	if len(modelTurn.Parts) != 2 {
		t.Fatalf("turn[1] parts count = %d, want 2 (text + functionCall)", len(modelTurn.Parts))
	}
	if modelTurn.Parts[0].Text != "Let me check the weather first." {
		t.Errorf("model turn text mismatch: %q", modelTurn.Parts[0].Text)
	}
	if modelTurn.Parts[1].FunctionCall == nil || modelTurn.Parts[1].FunctionCall.Name != "get_weather" {
		t.Errorf("model turn function call mismatch: %#v", modelTurn.Parts[1].FunctionCall)
	}
	if loc, ok := modelTurn.Parts[1].FunctionCall.Args["location"].(string); !ok || loc != "San Francisco, CA" {
		t.Errorf("functionCall args mismatch: %#v", modelTurn.Parts[1].FunctionCall.Args)
	}

	// Turn 2: Function response
	funcTurn := genReq.Contents[2]
	if funcTurn.Role != "function" {
		t.Errorf("turn[2].Role = %q, want 'function'", funcTurn.Role)
	}
	if len(funcTurn.Parts) != 1 || funcTurn.Parts[0].FunctionResponse == nil {
		t.Fatalf("expected 1 function response part, got %#v", funcTurn.Parts)
	}
	fr := funcTurn.Parts[0].FunctionResponse
	if fr.Name != "get_weather" {
		t.Errorf("function response name = %q, want 'get_weather'", fr.Name)
	}
	if fr.ID != "call_weather_1" {
		t.Errorf("function response ID = %q, want 'call_weather_1'", fr.ID)
	}

	// 3. Tools and parameters sanitization
	if len(genReq.Tools) != 1 || len(genReq.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools mismatch: %#v", genReq.Tools)
	}
	decl := genReq.Tools[0].FunctionDeclarations[0]
	if decl.Name != "get_weather" {
		t.Errorf("decl.Name = %q", decl.Name)
	}
	if _, ok := decl.Parameters["$schema"]; ok {
		t.Error("$schema was not stripped from function parameters")
	}
	if _, ok := decl.Parameters["additionalProperties"]; ok {
		t.Error("additionalProperties was not stripped from function parameters")
	}

	// 4. ToolConfig
	if genReq.ToolConfig == nil || genReq.ToolConfig.FunctionCallingConfig == nil {
		t.Fatal("expected ToolConfig to be set")
	}
	if genReq.ToolConfig.FunctionCallingConfig.Mode != "AUTO" {
		t.Errorf("toolConfig mode = %q, want 'AUTO'", genReq.ToolConfig.FunctionCallingConfig.Mode)
	}

	// 5. GenerationConfig
	gc := genReq.GenerationConfig
	if gc == nil {
		t.Fatal("expected GenerationConfig to be set")
	}
	if gc.Temperature == nil || *gc.Temperature != 0.7 {
		t.Errorf("temperature mismatch")
	}
	if gc.TopP == nil || *gc.TopP != 0.95 {
		t.Errorf("topP mismatch")
	}
	if gc.TopK == nil || *gc.TopK != 50 {
		t.Errorf("topK mismatch")
	}
	if gc.MaxOutputTokens == nil || *gc.MaxOutputTokens != 2048 {
		t.Errorf("maxOutputTokens mismatch")
	}
	if gc.ThinkingConfig == nil || gc.ThinkingConfig.ThinkingLevel != "HIGH" {
		t.Errorf("thinkingLevel mismatch: %#v", gc.ThinkingConfig)
	}

	// 6. BuildVertexVariables integration test with the typed request
	vars := BuildVertexVariables(model, genReq, cfg)
	if vars["model"] != "gemini-2.5-flash" {
		t.Errorf("vars.model = %v", vars["model"])
	}
	varsContents, ok := vars["contents"].([]any)
	if !ok || len(varsContents) != 3 {
		t.Fatalf("vars.contents len=%d, want 3", len(varsContents))
	}
}

func TestSchemaSanitizer_Visitor(t *testing.T) {
	sanitizer := NewNativeSchemaSanitizer()

	rawSchema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://example.com/product.schema.json",
		"title":                "Product",
		"type":                 "object",
		"additionalProperties": false,
		"minProperties":        1,
		"maxProperties":        10,
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "integer",
				"description": "The unique identifier for a product",
			},
			"name": map[string]any{
				"type":      "string",
				"minLength": 2,
				"maxLength": 100,
			},
			"tags": map[string]any{
				"type":     "array",
				"minItems": 0,
				"maxItems": 20,
				"items": map[string]any{
					"type": "string",
				},
			},
			"status": map[string]any{
				"type": []any{"string", "null"},
				"enum": []any{"active", "inactive"},
			},
		},
		"required": []any{"id", "name"},
	}

	sanitized := sanitizer.Sanitize(rawSchema).(map[string]any)

	// Verify unsupported keys stripped
	for _, key := range []string{"$schema", "$id", "title", "additionalProperties"} {
		if _, exists := sanitized[key]; exists {
			t.Errorf("key %q was not stripped by sanitizer", key)
		}
	}

	// Verify uppercase type
	if sanitized["type"] != "OBJECT" {
		t.Errorf("sanitized type = %v, want 'OBJECT'", sanitized["type"])
	}

	// Verify numeric constraints formatted to string
	if sanitized["minProperties"] != "1" || sanitized["maxProperties"] != "10" {
		t.Errorf("minProperties/maxProperties string formatting mismatch: %v, %v", sanitized["minProperties"], sanitized["maxProperties"])
	}

	// Verify properties converted to native key-value slice
	propsSlice, ok := sanitized["properties"].([]any)
	if !ok {
		t.Fatalf("properties should be []any of key/value maps, got %T", sanitized["properties"])
	}

	propMap := make(map[string]map[string]any)
	for _, item := range propsSlice {
		entry := item.(map[string]any)
		key := entry["key"].(string)
		val := entry["value"].(map[string]any)
		propMap[key] = val
	}

	if propMap["id"]["type"] != "INTEGER" {
		t.Errorf("id.type = %v, want 'INTEGER'", propMap["id"]["type"])
	}

	if propMap["name"]["minLength"] != "2" || propMap["name"]["maxLength"] != "100" {
		t.Errorf("name minLength/maxLength string formatting mismatch")
	}

	if propMap["tags"]["type"] != "ARRAY" || propMap["tags"]["items"].(map[string]any)["type"] != "STRING" {
		t.Errorf("tags array schema mismatch")
	}

	if propMap["status"]["type"] != "STRING" {
		t.Errorf("status nullable type should resolve to 'STRING', got %v", propMap["status"]["type"])
	}
}

func TestToolCallCoordinator_NameResolutionAndPairing(t *testing.T) {
	coord := NewToolCallCoordinator()

	// 1. Assistant generates tool calls
	parts := coord.RegisterAssistantToolCalls([]domain.ToolCall{
		{
			ID:   "call_1",
			Type: "function",
			Function: domain.FunctionCall{
				Name:      "search_database",
				Arguments: `{"query":"golang"}`,
			},
		},
		{
			ID:   "call_2",
			Type: "function",
			Function: domain.FunctionCall{
				Name:      "fetch_weather",
				Arguments: `{"city":"Berlin"}`,
			},
		},
	})

	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[0].FunctionCall.Name != "search_database" || parts[1].FunctionCall.Name != "fetch_weather" {
		t.Errorf("function call names mismatch")
	}

	// 2. Tool response paired by ID (even if returned in reverse order)
	resp2 := coord.PairToolResponse("call_2", "", `{"temp": 18}`)
	if resp2.FunctionResponse == nil || resp2.FunctionResponse.Name != "fetch_weather" {
		t.Errorf("pair by ID for call_2 failed: %#v", resp2.FunctionResponse)
	}

	resp1 := coord.PairToolResponse("call_1", "", `{"results": 42}`)
	if resp1.FunctionResponse == nil || resp1.FunctionResponse.Name != "search_database" {
		t.Errorf("pair by ID for call_1 failed: %#v", resp1.FunctionResponse)
	}

	// 3. Fallback to positional matching if ID is absent
	coord2 := NewToolCallCoordinator()
	coord2.RegisterAssistantToolCalls([]domain.ToolCall{
		{
			Function: domain.FunctionCall{
				Name:      "func_a",
				Arguments: `{}`,
			},
		},
		{
			Function: domain.FunctionCall{
				Name:      "func_b",
				Arguments: `{}`,
			},
		},
	})

	posResp1 := coord2.PairToolResponse("", "", "result_a")
	if posResp1.FunctionResponse.Name != "func_a" {
		t.Errorf("positional match 1 = %q, want 'func_a'", posResp1.FunctionResponse.Name)
	}

	posResp2 := coord2.PairToolResponse("", "", "result_b")
	if posResp2.FunctionResponse.Name != "func_b" {
		t.Errorf("positional match 2 = %q, want 'func_b'", posResp2.FunctionResponse.Name)
	}
}

func TestPartFinalizer_SignaturesAndNormalization(t *testing.T) {
	finalizer := NewPartFinalizer()

	// 1. Part with FunctionCall gets skipThoughtSentinel if no signature provided
	fcPart := &domain.Part{
		FunctionCall: &domain.GeminiFunctionCall{
			Name: "test_fn",
			Args: map[string]any{"x": 1},
		},
	}
	finalizer.FinalizeDomainPart(fcPart)
	if fcPart.ThoughtSignature != skipThoughtSentinel {
		t.Errorf("expected skipThoughtSentinel on function call part, got %q", fcPart.ThoughtSignature)
	}

	// 2. Part with text and no thought clears signature
	textPart := &domain.Part{
		Text:             "Hello world",
		ThoughtSignature: "some_sig",
	}
	finalizer.FinalizeDomainPart(textPart)
	if textPart.ThoughtSignature != "" {
		t.Errorf("expected thoughtSignature to be cleared for plain text, got %q", textPart.ThoughtSignature)
	}

	// 3. EnsureBase64Signature preserves valid base64 and encodes sentinel
	encodedSentinel := EnsureBase64Signature(skipThoughtSentinel)
	decoded, err := base64.StdEncoding.DecodeString(encodedSentinel)
	if err != nil || string(decoded) != skipThoughtSentinel {
		t.Errorf("EnsureBase64Signature(sentinel) failed: %v", err)
	}

	// 4. Inlined Base64 normalization
	rawB64 := "data:image/png;base64,a-b_c"
	normalized := NormalizeBase64(rawB64)
	if normalized != "a+b/c===" {
		t.Errorf("NormalizeBase64 = %q, want 'a+b/c==='", normalized)
	}
}
