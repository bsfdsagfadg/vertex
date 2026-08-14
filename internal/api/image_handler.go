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

// ImageHandler 提供 OpenAI /v1/images/* 入口。
// 汇聚点使用 Image family 的 typed 提交通道（ExecuteImageGenerate + ImageAdaptor）。
type ImageHandler struct {
	handler
}

func (img *ImageHandler) handleImageGenerations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	var body transform.ImagesRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError("请求体必须是合法JSON", "", nil))
		return
	}

	geminiReq, rawModel, convErr := transform.NewImageAdaptor().ToGeminiRequest(&body, img.cfg)
	if convErr != nil {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError("请求参数有误: "+convErr.Error(), "", nil))
		return
	}

	resolved := transform.ResolveModel(rawModel, img.cfg)
	if resolved.Family != transform.FamilyImage {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError(fmt.Sprintf("模型 %s 不是生图模型，无法用于图片生成", rawModel), "", nil))
		return
	}

	respFmt := body.ResponseFormat
	if respFmt == "" {
		respFmt = "b64_json"
	}
	n, nErr := resolveN(body.N, 8)
	if nErr != "" {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError(nErr, "n", nil))
		return
	}

	size := body.Size
	if size == "" {
		size = "1024x1024"
	}
	log.Printf("[Server] [ImageGenerations] 收到请求: 模型=%s, 尺寸=%s, 格式=%s, n=%d", resolved.ActualModel, size, respFmt, n)

	if rawModel == "" {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError("缺少model字段", "model", nil))
		return
	}
	if body.Prompt == "" {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError("缺少 prompt 字段 (missing prompt)", "prompt", nil))
		return
	}

	requestCtx, cancel := img.newRequestCtx(r)
	defer cancel()

	img.runOAIImageRequest(requestCtx, w, resolved, geminiReq, n, respFmt)
}

func (img *ImageHandler) handleImageEdits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	if err := r.ParseMultipartForm(multipartMemoryLimit); err != nil {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError("图片编辑请求解析失败，请检查 multipart 表单 (failed to parse edit request)", "", nil))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	imageUploads := formUploads(r, "image")
	if len(imageUploads) == 0 {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError("缺少 image 字段 (image is required)", "image", nil))
		return
	}
	images, err := uploadsToInlineImages(imageUploads)
	if err != nil {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError(err.Error(), "", nil))
		return
	}
	var mask *transform.InlineImage
	if maskUploads := formUploads(r, "mask"); len(maskUploads) > 0 {
		m, err := uploadToInlineImage(maskUploads[0])
		if err != nil {
			writeOAIError(w, r.Context(), vertex.NewInvalidParamError(err.Error(), "", nil))
			return
		}
		mask = &m
	}

	rawModel := transform.ResolveImageModel(formValue(r, "model"))
	resolved := transform.ResolveModel(rawModel, img.cfg)
	if resolved.Family != transform.FamilyImage {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError(fmt.Sprintf("模型 %s 不是生图模型，无法用于图片编辑", rawModel), "", nil))
		return
	}

	prompt := firstNonEmptyStr(formValue(r, "prompt"), "Edit the provided image.")
	prompt = transform.AppendNegativePrompt(prompt, formValue(r, "negative_prompt"))
	n := coerceOAIN(formValue(r, "n"))
	respFmt := firstNonEmptyStr(formValue(r, "response_format"), "b64_json")

	log.Printf("[Server] [ImageEdits] 收到请求: 模型=%s, 格式=%s, 图片数=%d", resolved.ActualModel, respFmt, len(images))

	geminiReq := transform.BuildTypedImageRequest(resolved.ActualModel, prompt, images, mask,
		formValue(r, "size"), formValue(r, "quality"), formValue(r, "style"),
		formValue(r, "background"), "edit")

	requestCtx, cancel := img.newRequestCtx(r)
	defer cancel()

	img.runOAIImageRequest(requestCtx, w, resolved, geminiReq, n, respFmt)
}

func (img *ImageHandler) handleImageVariations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}
	if err := r.ParseMultipartForm(multipartMemoryLimit); err != nil {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError("图片变体请求解析失败，请检查 multipart 表单 (failed to parse variation request)", "", nil))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	imageUploads := formUploads(r, "image")
	if len(imageUploads) == 0 {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError("缺少 image 字段 (image is required)", "image", nil))
		return
	}
	images, err := uploadsToInlineImages(imageUploads)
	if err != nil {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError(err.Error(), "", nil))
		return
	}

	rawModel := transform.ResolveImageModel(formValue(r, "model"))
	resolved := transform.ResolveModel(rawModel, img.cfg)
	if resolved.Family != transform.FamilyImage {
		writeOAIError(w, r.Context(), vertex.NewInvalidParamError(fmt.Sprintf("模型 %s 不是生图模型，无法用于图片变体", rawModel), "", nil))
		return
	}

	prompt := firstNonEmptyStr(formValue(r, "prompt"), "Create a variation of the provided image.")
	prompt = transform.AppendNegativePrompt(prompt, formValue(r, "negative_prompt"))
	n := coerceOAIN(formValue(r, "n"))
	respFmt := firstNonEmptyStr(formValue(r, "response_format"), "b64_json")

	log.Printf("[Server] [ImageVariations] 收到请求: 模型=%s, 格式=%s, 图片数=%d", resolved.ActualModel, respFmt, len(images))

	geminiReq := transform.BuildTypedImageRequest(resolved.ActualModel, prompt, images, nil,
		formValue(r, "size"), formValue(r, "quality"), formValue(r, "style"), "", "variation")

	requestCtx, cancel := img.newRequestCtx(r)
	defer cancel()

	img.runImageRequest(requestCtx, w, resolved, geminiReq, n, respFmt)
}

// runOAIImageRequest 并发 n 路图片生成请求并聚合 base64/url。
func (img *ImageHandler) runOAIImageRequest(ctx context.Context, w http.ResponseWriter, resolved *transform.ResolvedModel, geminiReq *transform.GeminiRequest, n int, responseFormat string) {
	img.runImageRequest(ctx, w, resolved, geminiReq, n, responseFormat)
}

func (img *ImageHandler) runImageRequest(ctx context.Context, w http.ResponseWriter, resolved *transform.ResolvedModel, geminiReq *transform.GeminiRequest, n int, responseFormat string) {
	wantURL := responseFormat == "url"

	type rResult struct {
		images []transform.ImagePayload
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
			resp, ve := img.ExecuteImageGenerate(ctx, resolved, geminiReq)
			if ve != nil {
				results[idx] = rResult{err: ve}
				return
			}
			results[idx] = rResult{images: transform.ExtractImagesTyped(resp)}
		}(i)
	}
	wg.Wait()

	var allImages []*transform.ImagePayload
	var firstErr *vertex.VertexError
	for _, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		for i := range r.images {
			allImages = append(allImages, &r.images[i])
		}
	}
	if len(allImages) == 0 {
		if firstErr == nil {
			firstErr = vertex.NewEmptyResponseError("上游未返回图片数据 (no image returned)", nil)
		}
		writeOAIError(w, ctx, firstErr)
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
