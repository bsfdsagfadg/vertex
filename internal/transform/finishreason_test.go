package transform

import "testing"

func TestCleanFinishReasonUnspecified(t *testing.T) {
	cases := []struct {
		name string
		resp *GeminiResponse
		want string
	}{
		{
			name: "nil response",
			resp: nil,
			want: "",
		},
		{
			name: "all unspecified cleared",
			resp: &GeminiResponse{Candidates: []*Candidate{
				{FinishReason: FinishReasonUnspecified},
				{FinishReason: FinishReasonUnspecified},
			}},
			want: "",
		},
		{
			name: "first real finish reason returned",
			resp: &GeminiResponse{Candidates: []*Candidate{
				{FinishReason: FinishReasonUnspecified},
				{FinishReason: "STOP"},
				{FinishReason: "MAX_TOKENS"},
			}},
			want: "STOP",
		},
		{
			name: "unspecified mutated to empty",
			resp: &GeminiResponse{Candidates: []*Candidate{
				{FinishReason: FinishReasonUnspecified},
				{FinishReason: "STOP"},
			}},
			want: "STOP",
		},
		{
			name: "empty candidate list",
			resp: &GeminiResponse{Candidates: []*Candidate{}},
			want: "",
		},
		{
			name: "nil candidate skipped",
			resp: &GeminiResponse{Candidates: []*Candidate{nil, {FinishReason: "SAFETY"}}},
			want: "SAFETY",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CleanFinishReasonUnspecified(c.resp)
			if got != c.want {
				t.Errorf("CleanFinishReasonUnspecified=%q, want %q", got, c.want)
			}
		})
	}

	// 就地变异断言：UNSPECIFIED 必须被清空为 ""。
	resp := &GeminiResponse{Candidates: []*Candidate{
		{FinishReason: FinishReasonUnspecified},
		{FinishReason: "STOP"},
	}}
	CleanFinishReasonUnspecified(resp)
	if resp.Candidates[0].FinishReason != "" {
		t.Errorf("UNSPECIFIED 候选应被清空为 \"\", got %q", resp.Candidates[0].FinishReason)
	}
	if resp.Candidates[1].FinishReason != "STOP" {
		t.Errorf("真实 finishReason 不应被改动, got %q", resp.Candidates[1].FinishReason)
	}
}

func TestMapFinishReason(t *testing.T) {
	cases := []struct {
		finish     string
		hasToolCalls bool
		want       string
	}{
		{"STOP", false, "stop"},
		{"stop", false, "stop"},
		{"MAX_TOKENS", false, "length"},
		{"SAFETY", false, "content_filter"},
		{"TOOL_CALLS", false, "tool_calls"},
		{"", false, "stop"},
		{"", true, "tool_calls"},
		{"STOP", true, "tool_calls"},
		{"UNKNOWN_XYZ", false, "stop"},
	}
	for _, c := range cases {
		if got := MapFinishReason(c.finish, c.hasToolCalls); got != c.want {
			t.Errorf("MapFinishReason(%q, %v)=%q, want %q", c.finish, c.hasToolCalls, got, c.want)
		}
	}
}