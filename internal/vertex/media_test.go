package vertex

import (
	"encoding/base64"
	"testing"
)

// makeResult 构造一个最小 Gemini 响应：candidates[0].content.parts = parts。
func makeResult(parts []any) map[string]any {
	return map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{"parts": parts},
			},
		},
	}
}

// ---- ExtractImageResponse ----

func TestExtractImageResponseInlineData(t *testing.T) {
	result := makeResult([]any{
		map[string]any{"inlineData": map[string]any{"data": "AAAA", "mimeType": "image/png"}},
		map[string]any{"inlineData": map[string]any{"data": "BBBB", "mimeType": "image/jpeg"}},
	})
	imgs := ExtractImageResponse(result)
	if len(imgs) != 2 {
		t.Fatalf("应抽出 2 张图，实际 %d", len(imgs))
	}
	if imgs[0].B64JSON != "AAAA" || imgs[0].MimeType != "image/png" {
		t.Errorf("图1 不符: %+v", imgs[0])
	}
	if imgs[1].B64JSON != "BBBB" || imgs[1].MimeType != "image/jpeg" {
		t.Errorf("图2 不符: %+v", imgs[1])
	}
}

func TestExtractImageResponseInlineDataDefaultMime(t *testing.T) {
	result := makeResult([]any{
		map[string]any{"inlineData": map[string]any{"data": "CCCC"}}, // 无 mimeType
	})
	imgs := ExtractImageResponse(result)
	if len(imgs) != 1 || imgs[0].MimeType != "image/png" {
		t.Fatalf("缺 mimeType 应默认 image/png，实际 %+v", imgs)
	}
}

func TestExtractImageResponseInlineDataSkipsEmptyData(t *testing.T) {
	result := makeResult([]any{
		map[string]any{"inlineData": map[string]any{"data": "", "mimeType": "image/png"}},
		map[string]any{"inlineData": map[string]any{"data": "DDDD", "mimeType": "image/png"}},
	})
	imgs := ExtractImageResponse(result)
	if len(imgs) != 1 || imgs[0].B64JSON != "DDDD" {
		t.Fatalf("空 data 段应跳过，实际 %+v", imgs)
	}
}

func TestExtractImageResponseMarkdownFallback(t *testing.T) {
	// 无 inlineData，全文以 markdown data-URI 开头 → 退化抠取。
	md := "![Generated Image](data:image/png;base64,EEEE)"
	result := makeResult([]any{
		map[string]any{"text": md},
	})
	imgs := ExtractImageResponse(result)
	if len(imgs) != 1 {
		t.Fatalf("markdown 退化应抽 1 张，实际 %d", len(imgs))
	}
	if imgs[0].B64JSON != "EEEE" {
		t.Errorf("应抠出 EEEE，实际 %q", imgs[0].B64JSON)
	}
}

func TestExtractImageResponseNoImage(t *testing.T) {
	result := makeResult([]any{
		map[string]any{"text": "just plain text, no image"},
	})
	if imgs := ExtractImageResponse(result); len(imgs) != 0 {
		t.Errorf("无图应返回空，实际 %+v", imgs)
	}
}

func TestExtractImageResponseEmptyCandidates(t *testing.T) {
	if imgs := ExtractImageResponse(map[string]any{}); len(imgs) != 0 {
		t.Errorf("空响应应返回空，实际 %+v", imgs)
	}
	if imgs := ExtractImageResponse(map[string]any{"candidates": []any{}}); len(imgs) != 0 {
		t.Errorf("空 candidates 应返回空，实际 %+v", imgs)
	}
}

// ---- ExtractAudioResponse：多段拼接守护回归（不能只取首段）----

func TestExtractAudioResponseConcatenatesAllSegments(t *testing.T) {
	// 三段 audio/L16，原始字节分别 3 / 4 / 5 字节。
	seg1 := []byte{0x01, 0x02, 0x03}
	seg2 := []byte{0x04, 0x05, 0x06, 0x07}
	seg3 := []byte{0x08, 0x09, 0x0a, 0x0b, 0x0c}
	enc := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

	result := makeResult([]any{
		map[string]any{"inlineData": map[string]any{"data": enc(seg1), "mimeType": "audio/L16;rate=24000"}},
		map[string]any{"inlineData": map[string]any{"data": enc(seg2), "mimeType": "audio/L16;rate=24000"}},
		map[string]any{"inlineData": map[string]any{"data": enc(seg3), "mimeType": "audio/L16;rate=24000"}},
	})

	audio := ExtractAudioResponse(result)
	if audio.Data == "" {
		t.Fatalf("应返回拼接音频")
	}
	decoded, err := base64.StdEncoding.DecodeString(audio.Data)
	if err != nil {
		t.Fatalf("结果 base64 解码失败: %v", err)
	}
	wantLen := len(seg1) + len(seg2) + len(seg3) // 12
	if len(decoded) != wantLen {
		t.Fatalf("🔴 拼接长度应为 3 段之和 %d（守护「只取首段=截断」回归），实际 %d", wantLen, len(decoded))
	}
	// 验证字节序正确拼接。
	want := append(append(append([]byte{}, seg1...), seg2...), seg3...)
	for i := range want {
		if decoded[i] != want[i] {
			t.Fatalf("拼接字节序错误 @%d: got %#x want %#x", i, decoded[i], want[i])
		}
	}
	// mime 取首个有效段。
	if audio.MimeType != "audio/L16;rate=24000" {
		t.Errorf("mime 应取首段，实际 %q", audio.MimeType)
	}
}

func TestExtractAudioResponseMimeFromFirstValid(t *testing.T) {
	enc := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	// 首段无 mime（应默认），但因首个有效段无 mime → mime 取默认。
	result := makeResult([]any{
		map[string]any{"inlineData": map[string]any{"data": enc([]byte{1, 2})}}, // 无 mimeType
		map[string]any{"inlineData": map[string]any{"data": enc([]byte{3, 4}), "mimeType": "audio/wav"}},
	})
	audio := ExtractAudioResponse(result)
	if audio.MimeType != "audio/L16;rate=24000" {
		t.Errorf("首个有效段无 mime 时应默认 audio/L16;rate=24000，实际 %q", audio.MimeType)
	}
	decoded, _ := base64.StdEncoding.DecodeString(audio.Data)
	if len(decoded) != 4 {
		t.Errorf("应拼接两段共 4 字节，实际 %d", len(decoded))
	}
}

func TestExtractAudioResponseSkipsNonAudio(t *testing.T) {
	enc := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	result := makeResult([]any{
		map[string]any{"inlineData": map[string]any{"data": enc([]byte{1, 2, 3}), "mimeType": "image/png"}}, // 跳过
		map[string]any{"inlineData": map[string]any{"data": enc([]byte{4, 5}), "mimeType": "audio/L16"}},
	})
	audio := ExtractAudioResponse(result)
	decoded, _ := base64.StdEncoding.DecodeString(audio.Data)
	if len(decoded) != 2 {
		t.Errorf("非 audio/* 段应跳过，应只拼 2 字节，实际 %d", len(decoded))
	}
	if audio.MimeType != "audio/L16" {
		t.Errorf("mime 应取首个 audio 段，实际 %q", audio.MimeType)
	}
}

func TestExtractAudioResponseEmpty(t *testing.T) {
	result := makeResult([]any{
		map[string]any{"text": "no audio here"},
	})
	audio := ExtractAudioResponse(result)
	if audio.Data != "" || audio.MimeType != "" {
		t.Errorf("无音频应返回空 AudioData，实际 %+v", audio)
	}
}

// ---- decodeBase64Loose ----

func TestDecodeBase64Loose(t *testing.T) {
	std := base64.StdEncoding.EncodeToString([]byte("hello world"))

	cases := []struct {
		name    string
		in      string
		want    []byte
		wantErr bool
	}{
		{"standard base64", std, []byte("hello world"), false},
		// URL-safe: 含 -/_ 字符。bytes 0xfb 0xff 0xbf → std "+/+/" 中含 +、/
		{"url-safe with dash underscore", "-_-_", mustStdDecode("+/+/"), false},
		{"missing padding restored", "aGVsbG8", []byte("hello"), false}, // "hello" std = aGVsbG8= (缺1个=)
		{"empty string", "", []byte{}, false},
		{"invalid chars", "@@@@", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := decodeBase64Loose(c.in)
			if c.wantErr {
				if err == nil {
					t.Errorf("应返回错误，实际 got=%v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if string(got) != string(c.want) {
				t.Errorf("decodeBase64Loose(%q)=%v，期望 %v", c.in, got, c.want)
			}
		})
	}
}

func mustStdDecode(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
