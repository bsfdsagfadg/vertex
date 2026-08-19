package transform

import "testing"

func TestTypesSerialization(t *testing.T) {
	content := Content{
		Role: "user",
		Parts: []Part{
			{Text: "hello world"},
			{InlineData: &Blob{MimeType: "image/png", Data: "iVBORw0KGgoAAAANS"}},
			{FunctionCall: &FunctionCall{Name: "get_weather", Args: map[string]any{"city": "Paris"}}},
		},
	}

	if len(content.Parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(content.Parts))
	}
	if content.Parts[0].Text != "hello world" {
		t.Errorf("part[0].Text mismatch")
	}
	if content.Parts[1].InlineData.MimeType != "image/png" {
		t.Errorf("part[1].InlineData.MimeType mismatch")
	}
	if content.Parts[2].FunctionCall.Name != "get_weather" {
		t.Errorf("part[2].FunctionCall.Name mismatch")
	}

	cand := Candidate{
		Index: 0,
		Content: &content,
		FinishReason: "STOP",
	}
	if cand.FinishReason != "STOP" {
		t.Errorf("cand.FinishReason mismatch")
	}

	usage := UsageMetadata{
		PromptTokenCount: 10,
		CandidatesTokenCount: 20,
		TotalTokenCount: 30,
	}
	if usage.TotalTokenCount != 30 {
		t.Errorf("usage.TotalTokenCount mismatch")
	}
}
