package transform

import "testing"

func TestIsToolEmpty(t *testing.T) {
	cases := []struct {
		name string
		tool Tool
		want bool
	}{
		{
			name: "empty tool",
			tool: Tool{},
			want: true,
		},
		{
			name: "googleSearch present",
			tool: Tool{GoogleSearch: GoogleSearch{}},
			want: false,
		},
		{
			name: "googleMaps present",
			tool: Tool{GoogleMaps: GoogleMaps{}},
			want: false,
		},
		{
			name: "functionDeclarations present",
			tool: Tool{FunctionDeclarations: []FunctionDeclaration{{Name: "fn"}}},
			want: false,
		},
		{
			name: "codeExecution present",
			tool: Tool{CodeExecution: struct{}{}},
			want: false,
		},
		{
			name: "retrieval present",
			tool: Tool{Retrieval: struct{}{}},
			want: false,
		},
		{
			name: "urlContext present",
			tool: Tool{URLContext: struct{}{}},
			want: false,
		},
		{
			name: "computerUse present",
			tool: Tool{ComputerUse: struct{}{}},
			want: false,
		},
		{
			name: "mcpTool present",
			tool: Tool{MCPTool: struct{}{}},
			want: false,
		},
		{
			name: "fileSearch present",
			tool: Tool{FileSearch: struct{}{}},
			want: false,
		},
		{
			name: "googleSearchRetrieval present",
			tool: Tool{GoogleSearchRetrieval: struct{}{}},
			want: false,
		},
		{
			name: "empty functionDeclarations slice",
			tool: Tool{FunctionDeclarations: []FunctionDeclaration{}},
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsToolEmpty(c.tool); got != c.want {
				t.Errorf("IsToolEmpty = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFilterEmptyTools(t *testing.T) {
	cases := []struct {
		name    string
		tools   []Tool
		wantN   int
		wantNil bool
	}{
		{
			name:    "nil input",
			tools:   nil,
			wantN:   0,
			wantNil: true,
		},
		{
			name:    "empty input",
			tools:   []Tool{},
			wantN:   0,
			wantNil: true,
		},
		{
			name:    "all empty",
			tools:   []Tool{{}, {}, {}},
			wantN:   0,
			wantNil: true,
		},
		{
			name:  "mixed empty and non-empty",
			tools: []Tool{{}, {GoogleSearch: GoogleSearch{}}, {}},
			wantN: 1,
		},
		{
			name:  "all non-empty",
			tools: []Tool{{GoogleSearch: GoogleSearch{}}, {GoogleMaps: GoogleMaps{}}},
			wantN: 2,
		},
		{
			name:  "single non-empty",
			tools: []Tool{{FunctionDeclarations: []FunctionDeclaration{{Name: "f"}}}},
			wantN: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FilterEmptyTools(c.tools)
			if c.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if len(got) != c.wantN {
				t.Errorf("expected %d tools, got %d: %+v", c.wantN, len(got), got)
			}
		})
	}
}
