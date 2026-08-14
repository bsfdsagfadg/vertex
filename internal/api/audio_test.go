package api

import (
	"encoding/binary"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

// ---- ttsWAVHeader：字节级断言 RIFF/WAVE 头结构 ----

func TestTTSWAVHeader(t *testing.T) {
	dataLen := 1000
	sampleRate := 24000
	h := ttsWAVHeader(dataLen, sampleRate)

	if len(h) != 44 {
		t.Fatalf("WAV 头长度应为 44，实际 %d", len(h))
	}
	if string(h[0:4]) != "RIFF" {
		t.Errorf("魔数[0:4] 应为 RIFF，实际 %q", string(h[0:4]))
	}
	// ChunkSize = 36 + dataLen
	if got := binary.LittleEndian.Uint32(h[4:8]); got != uint32(36+dataLen) {
		t.Errorf("ChunkSize 应为 %d，实际 %d", 36+dataLen, got)
	}
	if string(h[8:12]) != "WAVE" {
		t.Errorf("魔数[8:12] 应为 WAVE，实际 %q", string(h[8:12]))
	}
	if string(h[12:16]) != "fmt " {
		t.Errorf("子块[12:16] 应为 'fmt '，实际 %q", string(h[12:16]))
	}
	if got := binary.LittleEndian.Uint32(h[16:20]); got != 16 {
		t.Errorf("fmt chunk size 应为 16，实际 %d", got)
	}
	if got := binary.LittleEndian.Uint16(h[20:22]); got != 1 {
		t.Errorf("audio format 应为 1 (PCM)，实际 %d", got)
	}
	if got := binary.LittleEndian.Uint16(h[22:24]); got != 1 {
		t.Errorf("channels 应为 1，实际 %d", got)
	}
	if got := binary.LittleEndian.Uint32(h[24:28]); got != uint32(sampleRate) {
		t.Errorf("sampleRate 应为 %d，实际 %d", sampleRate, got)
	}
	// byteRate = sampleRate * channels * bits/8 = sampleRate * 2
	wantByteRate := uint32(sampleRate * 2)
	if got := binary.LittleEndian.Uint32(h[28:32]); got != wantByteRate {
		t.Errorf("byteRate 应为 sampleRate*2=%d，实际 %d", wantByteRate, got)
	}
	if got := binary.LittleEndian.Uint16(h[32:34]); got != 2 {
		t.Errorf("blockAlign 应为 2，实际 %d", got)
	}
	if got := binary.LittleEndian.Uint16(h[34:36]); got != 16 {
		t.Errorf("bitsPerSample 应为 16，实际 %d", got)
	}
	if string(h[36:40]) != "data" {
		t.Errorf("子块[36:40] 应为 'data'，实际 %q", string(h[36:40]))
	}
	if got := binary.LittleEndian.Uint32(h[40:44]); got != uint32(dataLen) {
		t.Errorf("data 长度应为 %d，实际 %d", dataLen, got)
	}
}

func TestTTSWAVHeaderByteRateVaries(t *testing.T) {
	// byteRate 必须随 sampleRate 变化（=sampleRate*2）。
	for _, sr := range []int{8000, 16000, 24000, 48000} {
		h := ttsWAVHeader(0, sr)
		if got := binary.LittleEndian.Uint32(h[28:32]); got != uint32(sr*2) {
			t.Errorf("sampleRate=%d 时 byteRate 应为 %d，实际 %d", sr, sr*2, got)
		}
	}
}

// ---- ttsParsePCMRate ----

func TestTTSParsePCMRate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"standard", "audio/L16;rate=24000", 24000},
		{"with spaces", "audio/L16; rate=16000", 16000},
		{"uppercase rate", "audio/L16;RATE=48000", 48000},
		{"empty default", "", 24000},
		{"no rate token default", "audio/L16", 24000},
		{"malformed rate value default", "audio/L16;rate=abc", 24000},
		{"rate prefix only default", "audio/L16;rate=", 24000},
		{"different rate", "audio/L16;rate=8000", 8000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ttsParsePCMRate(c.in); got != c.want {
				t.Errorf("ttsParsePCMRate(%q)=%d，期望 %d", c.in, got, c.want)
			}
		})
	}
}

// ---- ttsResolveVoice ----

func TestTTSResolveVoice(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"openai alloy maps to Kore", "alloy", "Kore"},
		{"openai echo maps to Puck", "echo", "Puck"},
		{"openai fable maps to Charon", "fable", "Charon"},
		{"openai nova maps to Aoede", "nova", "Aoede"},
		{"openai uppercase still maps", "ALLOY", "Kore"},
		{"gemini native passthrough", "Kore", "Kore"},
		{"gemini native passthrough Zephyr", "Zephyr", "Zephyr"},
		{"gemini native passthrough Sulafat", "Sulafat", "Sulafat"},
		{"unknown defaults to Kore", "nonexistent-voice", "Kore"},
		{"empty defaults to Kore", "", "Kore"},
		{"whitespace defaults to Kore", "   ", "Kore"},
		{"nil defaults to Kore", nil, "Kore"},
		{"non-string defaults to Kore", 123, "Kore"},
		{"trimmed gemini native", "  Puck  ", "Puck"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ttsResolveVoice(c.in); got != c.want {
				t.Errorf("ttsResolveVoice(%v)=%q，期望 %q", c.in, got, c.want)
			}
		})
	}
}

// ---- coerceSpeed ----

func TestCoerceSpeed(t *testing.T) {
	cases := []struct { //nolint:govet
		name string
		in   any
		want float64
	}{
		{"nil to 1.0", nil, 1.0},
		{"float passthrough", 1.5, 1.5},
		{"float slow", 0.5, 0.5},
		{"int coerced", 2, 2.0},
		{"string number", "1.25", 1.25},
		{"string number trimmed", "  0.75 ", 0.75},
		{"illegal string to 1.0", "fast", 1.0},
		{"empty string to 1.0", "", 1.0},
		{"unsupported type to 1.0", []int{1}, 1.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := coerceSpeed(c.in); got != c.want {
				t.Errorf("coerceSpeed(%v)=%v，期望 %v", c.in, got, c.want)
			}
		})
	}
}

// ---- ttsBuildGeminiPayload ----

func TestTTSBuildGeminiPayloadBasic(t *testing.T) {
	payload := ttsBuildGeminiPayload("hello world", "Kore", nil)

	gc, ok := payload["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 generationConfig")
	}
	mods, ok := gc["responseModalities"].([]any)
	if !ok || len(mods) != 1 || mods[0] != "AUDIO" {
		t.Errorf("responseModalities 应为 [AUDIO]，实际 %v", gc["responseModalities"])
	}

	sc, ok := gc["speechConfig"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 speechConfig")
	}
	vc, _ := sc["voiceConfig"].(map[string]any)
	pvc, _ := vc["prebuiltVoiceConfig"].(map[string]any)
	if pvc["voiceName"] != "Kore" {
		t.Errorf("voiceName 应为 Kore，实际 %v", pvc["voiceName"])
	}

	// speed=1.0 (nil) → prompt 即原文，不加 "Say the following"。
	contents, _ := payload["contents"].([]any)
	c0, _ := contents[0].(map[string]any)
	parts, _ := c0["parts"].([]any)
	p0, _ := parts[0].(map[string]any)
	if p0["text"] != "hello world" {
		t.Errorf("speed=1 时 prompt 应为原文，实际 %v", p0["text"])
	}
	if c0["role"] != "user" {
		t.Errorf("role 应为 user，实际 %v", c0["role"])
	}
}

func TestTTSBuildGeminiPayloadSpeedPrompt(t *testing.T) {
	promptOf := func(payload map[string]any) string {
		contents, _ := payload["contents"].([]any)
		c0, _ := contents[0].(map[string]any)
		parts, _ := c0["parts"].([]any)
		p0, _ := parts[0].(map[string]any)
		return p0["text"].(string)
	}

	// speed > 1 → faster
	fast := ttsBuildGeminiPayload("text body", "Kore", 1.5)
	if got := promptOf(fast); !strings.Contains(got, "faster") || !strings.HasPrefix(got, "Say the following ") {
		t.Errorf("speed>1 应带 'Say the following faster: '，实际 %q", got)
	}

	// speed < 1 → more slowly
	slow := ttsBuildGeminiPayload("text body", "Kore", 0.5)
	if got := promptOf(slow); !strings.Contains(got, "more slowly") || !strings.HasPrefix(got, "Say the following ") {
		t.Errorf("speed<1 应带 'Say the following more slowly: '，实际 %q", got)
	}

	// speed == 1 (string "1.0") → 原文不变
	one := ttsBuildGeminiPayload("text body", "Kore", "1.0")
	if got := promptOf(one); got != "text body" {
		t.Errorf("speed=1 应为原文，实际 %q", got)
	}
}

// 确保 payload 结构整体可被独立验证（防止意外多/少键）。
func TestTTSBuildGeminiPayloadShape(t *testing.T) {
	payload := ttsBuildGeminiPayload("x", "Puck", nil)
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	want := map[string]bool{"contents": true, "generationConfig": true}
	if len(keys) != len(want) {
		t.Errorf("顶层键应为 %v，实际 %v", reflect.ValueOf(want).MapKeys(), keys)
	}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("意外顶层键 %q", k)
		}
	}
}

// ---- audio empty response error handling ----

func TestAudioSpeech_EmptyResponse_Returns502(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	fx := newTestServerCustomMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		// 上游返回带有 STOP 但没有任何 audio inlineData 的空包
		resp := `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":{"candidates":[{"finishReason":"STOP","content":{"parts":[]}}]}}}}]}]`
		w.Write([]byte(resp))
	}, func(cfg *config.AppConfig) {
		cfg.ParallelPoolEnabled = false
		cfg.MaxRetries = 0
		cfg.RequestTimeoutSeconds = 30
	})

	body := map[string]any{
		"model": "gemini-2.5-flash",
		"input": "hello world",
		"voice": "alloy",
	}
	resp := doPost(t, fx.server.URL+"/v1/audio/speech", "sk-test-key", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502", resp.StatusCode)
	}
}
