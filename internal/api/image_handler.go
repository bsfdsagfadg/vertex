package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/transform"
	"github.com/bsfdsagfadg/vertex/internal/vertex"
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

	model := transform.ResolveImageModel(getStr(body, "model", ""))
	prompt := getStr(body, "prompt", "")
	size := getStr(body, "size", "1024x1024")
	respFmt := getStr(body, "response_format", "b64_json")

	n, nErr := resolveN(body["n"], 8)
	if nErr != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": nErr, "type": "invalid_request_error", "code": 400, "param": "n",
		}})
		return
	}

	log.Printf("[Server] [ImageGenerations] 收到请求: 模型=%s, 尺寸=%s, 格式=%s, n=%d", model, size, respFmt, n)

	if model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"message": "缺少model字段", "type": "invalid_request_error", "code": nil}})
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
	transform.ApplyDefaultThinking(geminiPayload, img.cfg.DefaultThinkingLevel(), model)
	if !hasImageSize(geminiPayload) {
		gc, _ := geminiPayload["generationConfig"].(map[string]any)
		if gc == nil {
			gc = map[string]any{}
			geminiPayload["generationConfig"] = gc
		}
		ic, _ := gc["imageConfig"].(map[string]any)
		if ic == nil {
			ic = map[string]any{}
			gc["imageConfig"] = ic
		}
		ic["imageSize"] = transform.ResolveImageSize(img.cfg.DefaultImageSize(), model)
	}

	img.runOAIImageRequest(r.Context(), w, model, geminiPayload, n, respFmt)
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

	model := transform.ResolveImageModel(formValue(r, "model"))
	prompt := firstNonEmptyStr(formValue(r, "prompt"), "Edit the provided image.")
	prompt = transform.AppendNegativePrompt(prompt, formValue(r, "negative_prompt"))
	n := coerceOAIN(formValue(r, "n"))
	respFmt := firstNonEmptyStr(formValue(r, "response_format"), "b64_json")

	log.Printf("[Server] [ImageEdits] 收到请求: 模型=%s, 格式=%s, 图片数=%d", model, respFmt, len(images))

	geminiPayload := transform.BuildImagePayload(model, prompt, images, mask,
		formValue(r, "size"), formValue(r, "quality"), formValue(r, "style"),
		formValue(r, "background"), "edit")

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

	model := transform.ResolveImageModel(formValue(r, "model"))
	prompt := firstNonEmptyStr(formValue(r, "prompt"), "Create a variation of the provided image.")
	prompt = transform.AppendNegativePrompt(prompt, formValue(r, "negative_prompt"))
	n := coerceOAIN(formValue(r, "n"))
	respFmt := firstNonEmptyStr(formValue(r, "response_format"), "b64_json")

	log.Printf("[Server] [ImageVariations] 收到请求: 模型=%s, 格式=%s, 图片数=%d", model, respFmt, len(images))

	geminiPayload := transform.BuildImagePayload(model, prompt, images, nil,
		formValue(r, "size"), formValue(r, "quality"), formValue(r, "style"), "", "variation")

	img.runOAIImageRequest(r.Context(), w, model, geminiPayload, n, respFmt)
}

func (img *ImageHandler) runOAIImageRequest(ctx context.Context, w http.ResponseWriter, model string, geminiPayload map[string]any, n int, responseFormat string) {
	wantURL := responseFormat == "url"

	type rResult struct {
		images []vertex.ImageData
		err    *vertex.VertexError
	}
	results := make([]rResult, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					results[idx] = rResult{err: vertex.NewInternalError(fmt.Sprintf("candidate panic: %v", rec), nil)}
				}
			}()
			log.Printf("[Server] [runOAIImageRequest] 开始获取图片 (第 %d/%d 张)", idx+1, n)
			resp, ve := img.coreGenerate(ctx, model, geminiPayload)
			if ve != nil {
				results[idx] = rResult{err: ve}
				return
			}
			results[idx] = rResult{images: vertex.ExtractImageResponse(resp)}
		}(i)
	}
	wg.Wait()

	var allImages []vertex.ImageData
	var firstErr *vertex.VertexError
	for _, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		allImages = append(allImages, r.images...)
	}
	if len(allImages) == 0 {
		if firstErr != nil {
			writeJSON(w, firstErr.Code, vertexErrorToOAI(firstErr))
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{
			"message": "上游未返回图片数据 (no image returned)", "type": "server_error", "code": 502}})
		return
	}

	items := make([]any, 0, len(allImages))
	for _, imgData := range allImages {
		if imgData.B64JSON == "" {
			continue
		}
		if wantURL {
			mimeType := imgData.MimeType
			if mimeType == "" {
				mimeType = "image/png"
			}
			items = append(items, map[string]any{"url": "data:" + mimeType + ";base64," + imgData.B64JSON})
		} else {
			items = append(items, map[string]any{"b64_json": imgData.B64JSON})
		}
	}
	if len(items) > n {
		items = items[:n]
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": time.Now().Unix(), "data": items})
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

	var buf strings.Builder
	enc := base64.NewEncoder(base64.StdEncoding, &buf)
	written, err := io.Copy(enc, f)
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
	mimeType := fh.Header.Get("Content-Type")
	if mimeType == "" {
		if ext := filepath.Ext(fh.Filename); ext != "" {
			mimeType = mime.TypeByExtension(ext)
		}
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
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
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
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
