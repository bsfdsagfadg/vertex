package api

import "testing"

// ---- stripFakePrefix：剥离 "假非流-" 前缀 ----

func TestStripFakePrefix(t *testing.T) {
	fakePrefixes := []string{"假非流-"}
	cases := []struct {
		name      string
		in        string
		wantModel string
		wantFake  bool
	}{
		{"chinese prefix", "假非流-gemini-2.5-flash", "gemini-2.5-flash", true},
		{"no prefix passthrough", "gemini-2.5-flash", "gemini-2.5-flash", false},
		{"empty passthrough", "", "", false},
		{"chinese prefix only", "假非流-", "", true},
		{"old fake- prefix not recognized", "fake-gemini-2.5-pro", "fake-gemini-2.5-pro", false},
		{"old 假流式- prefix not recognized", "假流式-gemini-2.5-flash", "假流式-gemini-2.5-flash", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotModel, gotFake := stripFakePrefix(c.in, fakePrefixes)
			if gotModel != c.wantModel || gotFake != c.wantFake {
				t.Errorf("stripFakePrefix(%q)=(%q,%v)，期望 (%q,%v)",
					c.in, gotModel, gotFake, c.wantModel, c.wantFake)
			}
		})
	}
}
