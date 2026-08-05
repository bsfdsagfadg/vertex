package transform

import "testing"

// TestNormalizePartMultimodal 验证 normalizePart 对 OpenAI 风格多模态 part 的归一（Fix 7）：
// image_url(data:/http)/input_image、media/file/file_data、inline_data → 对应 Gemini part。
func TestNormalizePartMultimodal(t *testing.T) {
	got := normalizePart(map[string]any{
		"type":      "image_url",
		"image_url": map[string]any{"url": "data:image/png;base64,QQ=="},
	})
	id, _ := got["inlineData"].(map[string]any)
	if id == nil || id["mimeType"] != "image/png" || id["data"] != "QQ==" {
		t.Fatalf("image_url data: 应转 inlineData，got %v", got)
	}

	got = normalizePart(map[string]any{
		"type":      "image_url",
		"image_url": map[string]any{"url": "https://x.com/a.mp4"},
	})
	fd, _ := got["fileData"].(map[string]any)
	if fd == nil || fd["fileUri"] != "https://x.com/a.mp4" || fd["mimeType"] != "video/mp4" {
		t.Fatalf("image_url http 应转 fileData(video/mp4)，got %v", got)
	}

	got = normalizePart(map[string]any{
		"type": "file", "file_uri": "gs://b/x.pdf", "mime_type": "application/pdf",
	})
	fd, _ = got["fileData"].(map[string]any)
	if fd == nil || fd["fileUri"] != "gs://b/x.pdf" || fd["mimeType"] != "application/pdf" {
		t.Fatalf("file 应转 fileData，got %v", got)
	}

	got = normalizePart(map[string]any{
		"type": "inline_data", "inline_data": map[string]any{"mime_type": "audio/wav", "data": "ZGF0YQ=="},
	})
	id, _ = got["inlineData"].(map[string]any)
	if id == nil || id["mimeType"] != "audio/wav" || id["data"] != "ZGF0YQ==" {
		t.Fatalf("inline_data 应转 inlineData，got %v", got)
	}

	got = normalizePart(map[string]any{"type": "text", "text": "hi"})
	if got["text"] != "hi" {
		t.Fatalf("text part，got %v", got)
	}

	got = normalizePart(map[string]any{"type": "weird", "some_key": "v"})
	if got["someKey"] != "v" {
		t.Fatalf("未知 part 应 camelCase 透传，got %v", got)
	}
}

// TestGuessMIMEFromURI 验证多类型 mime 猜测覆盖图/视频/音频/pdf/txt。
func TestGuessMIMEFromURI(t *testing.T) {
	cases := map[string]string{
		"a.jpg": "image/jpeg", "a.png": "image/png", "a.webp": "image/webp", "a.gif": "image/gif",
		"a.mp4": "video/mp4", "a.mov": "video/quicktime", "a.webm": "video/webm",
		"a.mp3": "audio/mpeg", "a.wav": "audio/wav", "a.ogg": "audio/ogg",
		"a.pdf": "application/pdf", "a.txt": "text/plain", "a.xyz": "image/png",
		"http://x/a.MP4?t=1": "video/mp4",
	}
	for in, want := range cases {
		if got := guessMIMEFromURI(in); got != want {
			t.Errorf("guessMIMEFromURI(%q)=%q，want %q", in, got, want)
		}
	}
}

// splitAssistantContent：assistant 文本里的 markdown data-URI 图片必须重解析为 inlineData，
// 否则巨型 base64 markdown 作为文本进 model 角色，多轮改图被上游拒。
func TestSplitAssistantContent_ImageMarkdown(t *testing.T) {
	s := "这是图片：\n\n![image](data:image/png;base64,iVBORw0KGgoAAAANS) 完成"
	parts := splitAssistantContent(s)
	var hasInline, hasText bool
	for _, p := range parts {
		m := p.(map[string]any)
		if id, ok := m["inlineData"].(map[string]any); ok {
			if id["mimeType"] == "image/png" {
				hasInline = true
			}
		}
		if txt, ok := m["text"].(string); ok && txt != "" {
			hasText = true
		}
	}
	if !hasInline {
		t.Errorf("markdown 图片应重解析为 inlineData，got %v", parts)
	}
	if !hasText {
		t.Errorf("图片前后的文本应保留为 text part")
	}
}

func TestSplitAssistantContent_PlainText(t *testing.T) {
	parts := splitAssistantContent("纯文本回复")
	if len(parts) != 1 || parts[0].(map[string]any)["text"] != "纯文本回复" {
		t.Errorf("纯文本应原样为单个 text part，got %v", parts)
	}
}

// ============ 多模态 content 转换 ============

func TestConvertUserContent_ImageDataURI(t *testing.T) {
	content := []any{
		map[string]any{"type": "text", "text": "看图"},
		map[string]any{"type": "image_url", "image_url": map[string]any{
			"url": "data:image/png;base64,AAAA",
		}},
	}
	parts := convertUserContent(content)
	if len(parts) < 2 {
		t.Fatalf("parts len=%d, want at least 2", len(parts))
	}
	id, ok := parts[1].(map[string]any)["inlineData"].(map[string]any)
	if !ok {
		t.Fatalf("part[1] 不是 inlineData: %v", parts[1])
	}
	if id["mimeType"] != "image/png" || id["data"] != "AAAA" {
		t.Errorf("inlineData=%v", id)
	}
}

func TestConvertUserContent_ImageRemoteURL(t *testing.T) {
	content := []any{
		map[string]any{"type": "image_url", "image_url": map[string]any{
			"url": "https://example.com/cat.jpeg",
		}},
	}
	parts := convertUserContent(content)
	if len(parts) < 1 {
		t.Fatalf("parts len=%d, want at least 1", len(parts))
	}
	fd, ok := parts[0].(map[string]any)["fileData"].(map[string]any)
	if !ok {
		t.Fatalf("远程 URL 应转 fileData: %v", parts[0])
	}
	if fd["mimeType"] != "image/jpeg" || fd["fileUri"] != "https://example.com/cat.jpeg" {
		t.Errorf("fileData=%v", fd)
	}
}

func TestConvertUserContent_Video(t *testing.T) {
	// data: URI 不带 video/* mime → 回退 video/mp4
	content := []any{
		map[string]any{"type": "video_url", "video_url": map[string]any{"url": "data:application/octet-stream;base64,QkJC"}},
	}
	parts := convertUserContent(content)
	if len(parts) < 1 {
		t.Fatalf("parts len=%d, want at least 1", len(parts))
	}
	id := parts[0].(map[string]any)["inlineData"].(map[string]any)
	if id["mimeType"] != "video/mp4" {
		t.Errorf("video mime=%v, want video/mp4 回退", id["mimeType"])
	}
	// input_video 字段名 + 显式 video mime
	content2 := []any{
		map[string]any{"type": "input_video", "input_video": "data:video/webm;base64,QkJC"},
	}
	parts2 := convertUserContent(content2)
	if len(parts2) < 1 {
		t.Fatalf("parts2 len=%d, want at least 1", len(parts2))
	}
	id2 := parts2[0].(map[string]any)["inlineData"].(map[string]any)
	if id2["mimeType"] != "video/webm" {
		t.Errorf("input_video mime=%v", id2["mimeType"])
	}
}

func TestConvertUserContent_InputAudio(t *testing.T) {
	// {data, format} 形态
	content := []any{
		map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "QUFB", "format": "mp3"}},
	}
	parts := convertUserContent(content)
	if len(parts) < 1 {
		t.Fatalf("parts len=%d, want at least 1", len(parts))
	}
	id := parts[0].(map[string]any)["inlineData"].(map[string]any)
	if id["mimeType"] != "audio/mpeg" {
		t.Errorf("audio mime=%v, want audio/mpeg", id["mimeType"])
	}
	// 未知 format → 回退 audio/wav
	content2 := []any{
		map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "QUFB", "format": "xyz"}},
	}
	parts2 := convertUserContent(content2)
	if len(parts2) < 1 {
		t.Fatalf("parts2 len=%d, want at least 1", len(parts2))
	}
	id2 := parts2[0].(map[string]any)["inlineData"].(map[string]any)
	if id2["mimeType"] != "audio/wav" {
		t.Errorf("未知 format mime=%v, want audio/wav 回退", id2["mimeType"])
	}
	// data: URI 形态
	content3 := []any{
		map[string]any{"type": "input_audio", "input_audio": "data:audio/flac;base64,QUFB"},
	}
	parts3 := convertUserContent(content3)
	if len(parts3) < 1 {
		t.Fatalf("parts3 len=%d, want at least 1", len(parts3))
	}
	id3 := parts3[0].(map[string]any)["inlineData"].(map[string]any)
	if id3["mimeType"] != "audio/flac" {
		t.Errorf("audio data URI mime=%v", id3["mimeType"])
	}
}
