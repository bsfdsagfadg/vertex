package transform

import (
	"encoding/base64"
	"testing"
)

// makeMediaResponse 构造一个最小强类型 Gemini 响应：candidates[0].content.parts = parts。
func makeMediaResponse(parts []Part) *GeminiResponse {
	return &GeminiResponse{
		Candidates: []*Candidate{
			{Content: &Content{Parts: parts}},
		},
	}
}

// ---- ExtractImagesTyped ----

func TestExtractImagesTypedInlineData(t *testing.T) {
	resp := makeMediaResponse([]Part{
		{InlineData: &InlineData{Data: "AAAA", MimeType: "image/png"}},
		{InlineData: &InlineData{Data: "BBBB", MimeType: "image/jpeg"}},
	})
	imgs := ExtractImagesTyped(resp)
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

func TestExtractImagesTypedInlineDataDefaultMime(t *testing.T) {
	resp := makeMediaResponse([]Part{
		{InlineData: &InlineData{Data: "CCCC"}}, // 无 mimeType
	})
	imgs := ExtractImagesTyped(resp)
	if len(imgs) != 1 || imgs[0].MimeType != "image/png" {
		t.Fatalf("缺 mimeType 应默认 image/png，实际 %+v", imgs)
	}
}

func TestExtractImagesTypedInlineDataSkipsEmptyData(t *testing.T) {
	resp := makeMediaResponse([]Part{
		{InlineData: &InlineData{Data: "", MimeType: "image/png"}},
		{InlineData: &InlineData{Data: "DDDD", MimeType: "image/png"}},
	})
	imgs := ExtractImagesTyped(resp)
	if len(imgs) != 1 || imgs[0].B64JSON != "DDDD" {
		t.Fatalf("空 data 段应跳过，实际 %+v", imgs)
	}
}

func TestExtractImagesTypedMarkdownFallback(t *testing.T) {
	// 无 inlineData，全文以 markdown data-URI 开头 → 退化抠取。
	md := "![Generated Image](data:image/png;base64,EEEE)"
	resp := makeMediaResponse([]Part{
		{Text: md},
	})
	imgs := ExtractImagesTyped(resp)
	if len(imgs) != 1 {
		t.Fatalf("markdown 退化应抽 1 张，实际 %d", len(imgs))
	}
	if imgs[0].B64JSON != "EEEE" {
		t.Errorf("应抠出 EEEE，实际 %q", imgs[0].B64JSON)
	}
}

func TestExtractImagesTypedNoImage(t *testing.T) {
	resp := makeMediaResponse([]Part{
		{Text: "just plain text, no image"},
	})
	if imgs := ExtractImagesTyped(resp); len(imgs) != 0 {
		t.Errorf("无图应返回空，实际 %+v", imgs)
	}
}

func TestExtractImagesTypedEmptyCandidates(t *testing.T) {
	if imgs := ExtractImagesTyped(&GeminiResponse{}); len(imgs) != 0 {
		t.Errorf("空响应应返回空，实际 %+v", imgs)
	}
	if imgs := ExtractImagesTyped(&GeminiResponse{Candidates: []*Candidate{}}); len(imgs) != 0 {
		t.Errorf("空 candidates 应返回空，实际 %+v", imgs)
	}
}

// ---- ExtractAudioTyped：多段拼接守护回归（不能只取首段）----

func TestExtractAudioTypedConcatenatesAllSegments(t *testing.T) {
	// 三段 audio/L16，原始字节分别 3 / 4 / 5 字节。
	seg1 := []byte{0x01, 0x02, 0x03}
	seg2 := []byte{0x04, 0x05, 0x06, 0x07}
	seg3 := []byte{0x08, 0x09, 0x0a, 0x0b, 0x0c}
	enc := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

	resp := makeMediaResponse([]Part{
		{InlineData: &InlineData{Data: enc(seg1), MimeType: "audio/L16;rate=24000"}},
		{InlineData: &InlineData{Data: enc(seg2), MimeType: "audio/L16;rate=24000"}},
		{InlineData: &InlineData{Data: enc(seg3), MimeType: "audio/L16;rate=24000"}},
	})

	audio := ExtractAudioTyped(resp)
	if len(audio.Bytes) == 0 {
		t.Fatalf("应返回拼接音频")
	}
	wantLen := len(seg1) + len(seg2) + len(seg3) // 12
	if len(audio.Bytes) != wantLen {
		t.Fatalf("拼接长度应为 3 段之和 %d（守护「只取首段=截断」回归），实际 %d", wantLen, len(audio.Bytes))
	}
	// 验证字节序正确拼接。
	want := append(append(append([]byte{}, seg1...), seg2...), seg3...)
	for i := range want {
		if audio.Bytes[i] != want[i] {
			t.Fatalf("拼接字节序错误 @%d: got %#x want %#x", i, audio.Bytes[i], want[i])
		}
	}
	// mime 取首个有效段。
	if audio.MIME != "audio/L16;rate=24000" {
		t.Errorf("mime 应取首段，实际 %q", audio.MIME)
	}
	if audio.SampleRate != 24000 {
		t.Errorf("SampleRate 应从 mime 解析为 24000，实际 %d", audio.SampleRate)
	}
}

func TestExtractAudioTypedMimeFromFirstValid(t *testing.T) {
	enc := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	// 首段无 mime（应默认），但因首个有效段无 mime → mime 取默认。
	resp := makeMediaResponse([]Part{
		{InlineData: &InlineData{Data: enc([]byte{1, 2})}}, // 无 mimeType
		{InlineData: &InlineData{Data: enc([]byte{3, 4}), MimeType: "audio/wav"}},
	})
	audio := ExtractAudioTyped(resp)
	if audio.MIME != "audio/L16;rate=24000" {
		t.Errorf("首个有效段无 mime 时应默认 audio/L16;rate=24000，实际 %q", audio.MIME)
	}
	if len(audio.Bytes) != 4 {
		t.Errorf("应拼接两段共 4 字节，实际 %d", len(audio.Bytes))
	}
}

func TestExtractAudioTypedSkipsNonAudio(t *testing.T) {
	enc := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	resp := makeMediaResponse([]Part{
		{InlineData: &InlineData{Data: enc([]byte{1, 2, 3}), MimeType: "image/png"}}, // 跳过
		{InlineData: &InlineData{Data: enc([]byte{4, 5}), MimeType: "audio/L16"}},
	})
	audio := ExtractAudioTyped(resp)
	if len(audio.Bytes) != 2 {
		t.Errorf("非 audio/* 段应跳过，应只拼 2 字节，实际 %d", len(audio.Bytes))
	}
	if audio.MIME != "audio/L16" {
		t.Errorf("mime 应取首个 audio 段，实际 %q", audio.MIME)
	}
}

func TestExtractAudioTypedEmpty(t *testing.T) {
	resp := makeMediaResponse([]Part{
		{Text: "no audio here"},
	})
	audio := ExtractAudioTyped(resp)
	if len(audio.Bytes) != 0 || audio.MIME != "" {
		t.Errorf("无音频应返回空 AudioData，实际 %+v", audio)
	}
}