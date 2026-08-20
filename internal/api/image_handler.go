package api

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
	"github.com/bsfdsagfadg/vertex/internal/transform"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ImageHandler struct {
	handler
}

func (img *ImageHandler) handleImageGenerations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	body, ok := img.readJSONObject(w, r)
	if !ok {
		return
	}

	rawModel := transform.ResolveImageModel(getStr(body, "model", ""))
	prompt := getStr(body, "prompt", "")
	size := getStr(body, "size", "1024x1024")
	respFmt := getStr(body, "response_format", "b64_json")

	log.Printf("[Server] [ImageGenerations] 收到请求: 模型=%s, 尺寸=%s, 格式=%s", rawModel, size, respFmt)

	cfg := img.requestConfig(r)
	model, _, modelOK := resolveConfiguredModel(rawModel, cfg)
	if !modelOK {
		oaiModelNotFound(w, rawModel)
		return
	}
	if !transform.IsImageModel(model) {
		oaiResourceError(w, http.StatusBadRequest, "model_capability_unsupported", "selected model does not advertise image output capability", "model")
		return
	}
	if respFmt != "b64_json" && respFmt != "url" {
		oaiResourceError(w, http.StatusBadRequest, "unsupported_response_format", "response_format must be b64_json or url", "response_format")
		return
	}
	if prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "缺少 prompt 字段 (missing prompt)", "type": "invalid_request_error", "code": 400}})
		return
	}

	geminiPayload := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}},
	}
	transform.ApplyImageConfig(geminiPayload, body, model)
	transform.ApplyImageDefaults(geminiPayload, model, cfg.DefaultImageSize(), cfg.DefaultResponseModalities())

	images, vErr := img.vc.CompleteChatImage(r.Context(), model, geminiPayload)
	if vErr != nil {
		ve := toVertexError(vErr)
		writeJSON(w, ve.Code, vertexErrorToOAI(ve))
		return
	}

	data := make([]any, 0, len(images))
	for _, generated := range images {
		if generated.B64JSON == "" {
			continue
		}
		item, err := img.imageResultItem(generated.B64JSON, generated.MimeType, respFmt)
		if err != nil {
			img.oaiBadRequest(w, err.Error())
			return
		}
		data = append(data, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": time.Now().Unix(), "data": data})
}

func (img *ImageHandler) handleImageEdits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	if err := r.ParseMultipartForm(multipartMemoryLimit); err != nil {
		img.oaiBadRequest(w, "图片编辑请求解析失败，请检查 multipart 表单 (failed to parse edit request)")
		return
	}

	imageUploads := formUploads(r, "image")
	if len(imageUploads) == 0 {
		img.oaiBadRequest(w, "缺少 image 字段 (image is required)")
		return
	}
	images, err := uploadsToInlineImages(imageUploads)
	if err != nil {
		img.oaiBadRequest(w, err.Error())
		return
	}
	var mask *transform.InlineImage
	if maskUploads := formUploads(r, "mask"); len(maskUploads) > 0 {
		m, err := uploadToInlineImage(maskUploads[0])
		if err != nil {
			img.oaiBadRequest(w, err.Error())
			return
		}
		mask = &m
	}

	rawModel := transform.ResolveImageModel(formValue(r, "model"))
	cfg := img.requestConfig(r)
	model, _, modelOK := resolveConfiguredModel(rawModel, cfg)
	if !modelOK {
		oaiModelNotFound(w, rawModel)
		return
	}
	if !transform.IsImageModel(model) || !strings.HasPrefix(model, "gemini") {
		oaiResourceError(w, http.StatusBadRequest, "model_capability_unsupported", "selected model does not support multimodal image editing", "model")
		return
	}
	prompt := firstNonEmptyStr(formValue(r, "prompt"), "Edit the provided image.")
	prompt = transform.AppendNegativePrompt(prompt, formValue(r, "negative_prompt"))
	n := coerceOAIN(formValue(r, "n"))
	respFmt := firstNonEmptyStr(formValue(r, "response_format"), "b64_json")
	if respFmt != "b64_json" && respFmt != "url" {
		img.oaiBadRequest(w, "response_format must be b64_json or url")
		return
	}

	log.Printf("[Server] [ImageEdits] 收到请求: 模型=%s, 格式=%s, 图片数=%d", model, respFmt, len(images))

	geminiPayload := transform.BuildImagePayload(model, prompt, images, mask,
		formValue(r, "size"), formValue(r, "quality"), formValue(r, "style"),
		formValue(r, "background"), "edit")
	transform.ApplyImageDefaults(geminiPayload, model, cfg.DefaultImageSize(), cfg.DefaultResponseModalities())

	img.runOAIImageRequest(r.Context(), w, model, geminiPayload, n, respFmt)
}

func (img *ImageHandler) handleImageVariations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	if err := r.ParseMultipartForm(multipartMemoryLimit); err != nil {
		img.oaiBadRequest(w, "图片变体请求解析失败，请检查 multipart 表单 (failed to parse variation request)")
		return
	}

	imageUploads := formUploads(r, "image")
	if len(imageUploads) == 0 {
		img.oaiBadRequest(w, "缺少 image 字段 (image is required)")
		return
	}
	images, err := uploadsToInlineImages(imageUploads)
	if err != nil {
		img.oaiBadRequest(w, err.Error())
		return
	}

	rawModel := transform.ResolveImageModel(formValue(r, "model"))
	cfg := img.requestConfig(r)
	model, _, modelOK := resolveConfiguredModel(rawModel, cfg)
	if !modelOK {
		oaiModelNotFound(w, rawModel)
		return
	}
	if !transform.IsImageModel(model) || !strings.HasPrefix(model, "gemini") {
		oaiResourceError(w, http.StatusBadRequest, "model_capability_unsupported", "selected model does not support image variation input", "model")
		return
	}
	prompt := firstNonEmptyStr(formValue(r, "prompt"), "Create a variation of the provided image.")
	prompt = transform.AppendNegativePrompt(prompt, formValue(r, "negative_prompt"))
	n := coerceOAIN(formValue(r, "n"))
	respFmt := firstNonEmptyStr(formValue(r, "response_format"), "b64_json")
	if respFmt != "b64_json" && respFmt != "url" {
		img.oaiBadRequest(w, "response_format must be b64_json or url")
		return
	}

	log.Printf("[Server] [ImageVariations] 收到请求: 模型=%s, 格式=%s, 图片数=%d", model, respFmt, len(images))

	geminiPayload := transform.BuildImagePayload(model, prompt, images, nil,
		formValue(r, "size"), formValue(r, "quality"), formValue(r, "style"), "", "variation")
	transform.ApplyImageDefaults(geminiPayload, model, cfg.DefaultImageSize(), cfg.DefaultResponseModalities())

	img.runOAIImageRequest(r.Context(), w, model, geminiPayload, n, respFmt)
}

func (img *ImageHandler) runOAIImageRequest(ctx context.Context, w http.ResponseWriter, model string, geminiPayload map[string]any, n int, responseFormat string) {
	wantURL := responseFormat == "url"
	items := make([]any, 0, n)
	for i := 0; i < n; i++ {
		log.Printf("[Server] [runOAIImageRequest] 开始获取图片 (第 %d/%d 张)", i+1, n)
		images, vErr := img.vc.CompleteChatImage(ctx, model, geminiPayload)
		if vErr != nil {
			ve := toVertexError(vErr)
			writeJSON(w, ve.Code, vertexErrorToOAI(ve))
			return
		}
		for _, generated := range images {
			if generated.B64JSON == "" {
				continue
			}
			format := "b64_json"
			if wantURL {
				format = "url"
			}
			item, err := img.imageResultItem(generated.B64JSON, generated.MimeType, format)
			if err != nil {
				img.oaiBadRequest(w, err.Error())
				return
			}
			items = append(items, item)
		}
		if len(items) >= n {
			break
		}
	}

	if len(items) == 0 {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{
			"message": "上游未返回图片数据 (no image returned)", "type": "server_error", "code": 502}})
		return
	}
	if len(items) > n {
		items = items[:n]
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": time.Now().Unix(), "data": items})
}

func (img *ImageHandler) imageResultItem(encoded, mimeType, responseFormat string) (map[string]any, error) {
	if responseFormat != "url" {
		return map[string]any{"b64_json": encoded}, nil
	}
	if img.platform == nil {
		return nil, errors.New("local image URL storage is unavailable")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("upstream image data is invalid base64")
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
	file, err := img.platform.createGeneratedFile("image_output", "openai", data, mimeType, time.Now().Add(time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	return map[string]any{"url": "/v1/files/" + file.ID + "/content"}, nil
}

func (img *ImageHandler) oaiBadRequest(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
		"message": message, "type": "invalid_request_error", "code": 400}})
}

func (img *ImageHandler) readJSONObject(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "请求体必须是合法JSON", "type": "invalid_request_error", "code": nil}})
		return nil, false
	}
	return body, true
}

const multipartMemoryLimit = 8 << 20

func formValue(r *http.Request, key string) string {
	if r.MultipartForm == nil {
		return ""
	}
	if vs := r.MultipartForm.Value[key]; len(vs) > 0 {
		return vs[0]
	}
	return ""
}

func formUploads(r *http.Request, key string) []*multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}
	var out []*multipart.FileHeader
	prefix := key + "["
	for k, fhs := range r.MultipartForm.File {
		if k == key || k == key+"[]" || strings.HasPrefix(k, prefix) {
			out = append(out, fhs...)
		}
	}
	return out
}

func uploadToInlineImage(fh *multipart.FileHeader) (transform.InlineImage, error) {
	f, err := fh.Open()
	if err != nil {
		return transform.InlineImage{}, &badRequestError{msg: "无法读取上传文件 (cannot read upload)"}
	}
	defer func() { _ = f.Close() }()
	reader := bufio.NewReader(f)
	header, _ := reader.Peek(512)
	detectedType := http.DetectContentType(header)
	if !strings.HasPrefix(strings.ToLower(detectedType), "image/") {
		return transform.InlineImage{}, &badRequestError{msg: "上传内容不是受支持的图片 (uploaded content is not an image)"}
	}

	var buf strings.Builder
	enc := base64.NewEncoder(base64.StdEncoding, &buf)
	written, err := io.Copy(enc, reader)
	if err != nil {
		return transform.InlineImage{}, &badRequestError{msg: "无法读取上传文件 (cannot read upload)"}
	}
	_ = enc.Close()
	if written == 0 {
		name := fh.Filename
		if name == "" {
			name = "image"
		}
		return transform.InlineImage{}, &badRequestError{msg: name + " 内容为空 (empty file)"}
	}
	mimeType := detectedType
	return transform.InlineImage{MimeType: mimeType, Data: buf.String()}, nil
}

func uploadsToInlineImages(fhs []*multipart.FileHeader) ([]transform.InlineImage, error) {
	out := make([]transform.InlineImage, 0, len(fhs))
	for _, fh := range fhs {
		img, err := uploadToInlineImage(fh)
		if err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	return out, nil
}

type badRequestError struct{ msg string }

func (e *badRequestError) Error() string { return e.msg }

func coerceOAIN(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 1
	}
	if n < 1 {
		return 1
	}
	if n > 8 {
		return 8
	}
	return n
}

func firstNonEmptyStr(a, b string) string {
	return strutil.FirstNonEmptyStr(a, b)
}

func getStr(body map[string]any, key, def string) string {
	v, ok := body[key]
	if !ok {
		return def
	}
	s, ok := v.(string)
	if !ok {
		return def
	}
	return s
}

func hasImageSize(payload map[string]any) bool {
	gc, ok := payload["generationConfig"].(map[string]any)
	if !ok {
		return false
	}
	ic, ok := gc["imageConfig"].(map[string]any)
	if !ok {
		return false
	}
	v, ok := ic["imageSize"]
	if !ok {
		return false
	}
	s, _ := v.(string)
	return s != ""
}
