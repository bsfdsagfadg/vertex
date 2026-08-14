package api

import (
	"encoding/binary"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/transform"
)

type AudioHandler struct {
	handler
}

const ttsDefaultModel = "gemini-3.1-flash-tts-preview"

type ttsFormat struct {
	contentType string
	wrapWAV     bool
}

//nolint:gochecknoglobals // Audio format formats
var ttsFormatInfo = map[string]ttsFormat{
	"mp3":  {"audio/wav", true},
	"wav":  {"audio/wav", true},
	"pcm":  {"audio/L16", false},
	"opus": {"audio/wav", true},
	"aac":  {"audio/wav", true},
	"flac": {"audio/wav", true},
}

func (a *AudioHandler) handleAudioSpeech(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}

	var body transform.SpeechRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "请求体必须是合法JSON (invalid JSON)", "type": "invalid_request_error", "code": 400}})
		return
	}

	voice := transform.ResolveVoice(body.Voice)
	respFmt := strings.ToLower(firstNonEmptyStr(body.ResponseFormat, "mp3"))

	geminiReq, rawModel, convErr := transform.NewAudioAdaptor().ToGeminiRequest(&body, a.cfg)
	if convErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "请求参数有误: " + convErr.Error(), "type": "invalid_request_error", "code": 400}})
		return
	}
	if rawModel == "" {
		rawModel = ttsDefaultModel
	}

	resolved := transform.ResolveModel(rawModel, a.cfg)
	log.Printf("[Server] [AudioSpeech] 收到请求: 模型=%s, 真模型=%s, 语音=%s, 格式=%s", rawModel, resolved.ActualModel, voice, respFmt)

	fmtInfo, ok := ttsFormatInfo[respFmt]
	if !ok {
		fmtInfo = ttsFormat{"audio/wav", true}
	}

	resp, ve := a.ExecuteAudioSpeech(r.Context(), resolved, geminiReq)
	if ve != nil {
		writeJSON(w, ve.Code, vertexErrorToOAI(ve))
		return
	}

	audio := transform.ExtractAudioTyped(resp)
	out := audio.Bytes
	if fmtInfo.wrapWAV {
		sampleRate := audio.SampleRate
		if sampleRate <= 0 {
			sampleRate = 24000
		}
		out = append(ttsWAVHeader(len(out), sampleRate), out...)
	}

	w.Header().Set("Content-Type", fmtInfo.contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

func ttsResolveVoice(voice any) string {
	v, ok := voice.(string)
	if !ok {
		return transform.ResolveVoice("")
	}
	return transform.ResolveVoice(v)
}

func ttsWAVHeader(dataLen, sampleRate int) []byte {
	const bits, channels = 16, 1
	byteRate := sampleRate * channels * bits / 8
	blockAlign := channels * bits / 8

	h := make([]byte, 0, 44)
	h = append(h, "RIFF"...)
	h = appendU32LE(h, uint32(36+dataLen))
	h = append(h, "WAVE"...)
	h = append(h, "fmt "...)
	h = appendU32LE(h, 16)
	h = appendU16LE(h, 1)
	h = appendU16LE(h, uint16(channels))
	h = appendU32LE(h, uint32(sampleRate))
	h = appendU32LE(h, uint32(byteRate))
	h = appendU16LE(h, uint16(blockAlign))
	h = appendU16LE(h, uint16(bits))
	h = append(h, "data"...)
	h = appendU32LE(h, uint32(dataLen))
	return h
}

func appendU32LE(b []byte, v uint32) []byte {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	return append(b, tmp[:]...)
}

func appendU16LE(b []byte, v uint16) []byte {
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], v)
	return append(b, tmp[:]...)
}

func coerceSpeed(speed any) float64 {
	switch v := speed.(type) {
	case nil:
		return 1.0
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return 1.0
}

// ttsParsePCMRate 从 mime 里的 rate= 取采样率，缺省 24000。
func ttsParsePCMRate(mimeType string) int {
	const def = 24000
	if mimeType == "" {
		return def
	}
	for _, token := range strings.Split(mimeType, ";") {
		token = strings.ToLower(strings.TrimSpace(token))
		if strings.HasPrefix(token, "rate=") {
			if n, err := strconv.Atoi(token[5:]); err == nil {
				return n
			}
		}
	}
	return def
}

// ttsBuildGeminiPayload 构造 TTS Gemini 载荷（旧 map 版，供单元测试锁定结构语义）。
func ttsBuildGeminiPayload(text, voice string, speed any) map[string]any {
	prompt := text
	spd := coerceSpeed(speed)
	if spd != 0 && abs(spd-1.0) > 1e-6 {
		pace := "faster"
		if spd < 1.0 {
			pace = "more slowly"
		}
		prompt = "Say the following " + pace + ": " + text
	}
	return map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}},
		"generationConfig": map[string]any{
			"responseModalities": []any{"AUDIO"},
			"speechConfig": map[string]any{
				"voiceConfig": map[string]any{
					"prebuiltVoiceConfig": map[string]any{"voiceName": voice},
				},
			},
		},
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
