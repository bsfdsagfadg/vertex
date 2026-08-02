package cli

import (
	"reflect"
	"testing"

	"charm.land/bubbletea/v2"
)

func TestCtrlCQuitsTUI(t *testing.T) {
	m := TuiModel{activeReqs: make(map[string]*ReqState)}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+C 应返回退出命令")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("Ctrl+C 返回消息类型 %T, want tea.QuitMsg", msg)
	}
}

func TestStringWidth(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want int
	}{
		{"ASCII empty", "", 0},
		{"ASCII word", "hello", 5},
		{"Chinese standard", "你好", 4},
		{"Chinese mixed", "hello你好", 9},
		{"Emoji rocket", "🚀", 2},
		{"Emoji trophy", "🏆", 2},
		{"Emoji memo", "📝", 2},
		{"Emoji flag HK", "🇭🇰", 2}, // let's see what uniseg yields
		{"Emoji flag CN", "🇨🇳", 2}, // let's see what uniseg yields
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringWidth(tt.s)
			if got != tt.want {
				t.Errorf("stringWidth(%q) = %d; want %d", tt.s, got, tt.want)
			}
		})
	}
}

func TestPadOrTrunc(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxCol int
		want   string
	}{
		{"Empty string pad 5", "", 5, "     "},
		{"ASCII pad", "abc", 5, "abc  "},
		{"ASCII exact", "abcde", 5, "abcde"},
		{"ASCII trunc to 5", "abcdefg", 5, "abc.."},
		{"Chinese pad", "你好", 6, "你好  "},
		{"Chinese exact", "你好", 4, "你好"},
		{"Chinese trunc to 5", "你好啊", 5, "你.. "}, // "你好啊" is width 6. "你" is width 2. "你" + ".." is width 4 <= 5-2? Yes, 4 > 3, breaks! Returns "你" + ".." + pad = "你.. " (width 5).
		{"Chinese trunc to 4", "你好啊", 4, "你.."},  // cur=2. Next "好" (2). cur+gw = 4 > maxCol-2 (2)? Yes, 4 > 2, breaks. Returns "你" + ".." (width 4).
		{"Chinese trunc to 3", "你好啊", 3, ".. "},  // cur=0. Next "你" (2). cur+gw = 2 > maxCol-2 (1)? Yes, 2 > 1, breaks. Returns ".." + " " (width 3).
		{"Emoji pad", "🚀", 4, "🚀  "},
		{"Emoji trunc to 3", "🚀🏆", 3, ".. "}, // cur=0. Next "🚀" (2). cur+gw = 2 > maxCol-2 (1)? Yes, breaks. Returns ".." + " " (width 3).
		{"Emoji flag HK pad", "🇭🇰", 3, "🇭🇰 "},
		{"Emoji flag HK trunc to 3", "🇭🇰🏆", 3, ".. "},
		{"maxCol <= 2 (2)", "hello", 2, ".."},
		{"maxCol <= 2 (1)", "hello", 1, ".."},
		{"maxCol <= 2 (0)", "hello", 0, ".."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := padOrTrunc(tt.s, tt.maxCol)
			if got != tt.want {
				t.Errorf("padOrTrunc(%q, %d) = %q (width %d); want %q (width %d)",
					tt.s, tt.maxCol, got, stringWidth(got), tt.want, stringWidth(tt.want))
			}
		})
	}
}

func TestWordWrap(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		maxCol int
		want   []string
	}{
		{
			name:   "ASCII simple wrapping",
			text:   "hello world",
			maxCol: 5,
			want:   []string{"hello", "world"}, // Wait! Let's trace how the code handles "hello world" with maxCol=5.
			// Text: "hello world" (width 11).
			// i=0: "h" -> cur="h", curW=1
			// i=1: "e" -> cur="he", curW=2
			// i=2: "l" -> cur="hel", curW=3
			// i=3: "l" -> cur="hell", curW=4
			// i=4: "o" -> cur="hello", curW=5
			// i=5: " " (space). curW > 0.
			//   nextW = width of "world" = 5.
			//   curW+1+nextW = 5+1+5 = 11 > maxCol (5).
			//   Appends cur.String() + padding = "hello" + "" -> "hello"
			//   cur.Reset(), curW=0. i++ (skips space). continue.
			// i=6: "w" -> cur="w", curW=1
			// ... "world" -> cur="world", curW=5.
			// End of loop. cur.Len() > 0 -> appends "world" + "".
			// Result: []string{"hello", "world"}. Perfectly matches!
		},
		{
			name:   "ASCII with multiple spaces result",
			text:   "hello   world",
			maxCol: 5,
			want:   []string{"hello", "     ", "world"},
		},
		{
			name:   "Chinese wrap",
			text:   "你好世界",
			maxCol: 4,
			want:   []string{"你好", "世界"}, // "你好" width is 4, "世界" width is 4. Perfect fit.
		},
		{
			name:   "Chinese wrap odd width",
			text:   "你好世界",
			maxCol: 5,
			want:   []string{"你好 ", "世界 "}, // cur="你好" (width 4). Next "世" (width 2). 4+2=6 > 5 -> wraps to "你好 " (width 5). Then cur="世界" (width 4), wraps to "世界 " (width 5).
		},
		{
			name:   "Emoji wrap",
			text:   "🚀🏆📝",
			maxCol: 4,
			want:   []string{"🚀🏆", "📝  "}, // "🚀🏆" is width 4. "📝" is width 2, gets padded to 4.
		},
		{
			name:   "Mixed text wrap",
			text:   "Go🚀语言", // "Go" (2), "🚀" (2), "语言" (4). Total width 8.
			maxCol: 4,
			want:   []string{"Go🚀", "语言"}, // "Go🚀" width is 4. "语言" width is 4.
		},
		{
			name:   "Zero or negative maxCol",
			text:   "hello",
			maxCol: 0,
			want:   []string{"hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wordWrap(tt.text, tt.maxCol)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("wordWrap(%q, %d) = %q; want %q", tt.text, tt.maxCol, got, tt.want)
			}
			// Verify that every line in the output (except if maxCol <= 0) has exactly width maxCol
			if tt.maxCol > 0 {
				for i, line := range got {
					w := stringWidth(line)
					if w != tt.maxCol {
						t.Errorf("line %d %q has width %d; want %d", i, line, w, tt.maxCol)
					}
				}
			}
		})
	}
}
