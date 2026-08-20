package api

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	coremodel "github.com/bsfdsagfadg/vertex/internal/core/model"
	"github.com/bsfdsagfadg/vertex/internal/repository"
	responseadapter "github.com/bsfdsagfadg/vertex/internal/responses"
	"github.com/bsfdsagfadg/vertex/internal/strutil"
	"github.com/bsfdsagfadg/vertex/internal/transform"
)

const (
	localFileTTL    = 30 * 24 * time.Hour
	batchTTL        = 30 * 24 * time.Hour
	defaultCacheTTL = time.Hour
	maxCacheTTL     = 7 * 24 * time.Hour
)

type PlatformHandler struct {
	handler
	blobs *blobStore

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	jobs   map[string]context.CancelFunc
	wg     sync.WaitGroup
}

func NewPlatformHandler(h handler) (*PlatformHandler, error) {
	if h.repository == nil {
		return nil, nil
	}
	store, err := newBlobStore(filepath.Join(filepath.Dir(h.repository.Path()), "blobs"))
	if err != nil {
		return nil, err
	}
	return &PlatformHandler{handler: h, blobs: store, jobs: map[string]context.CancelFunc{}}, nil
}

func (h *PlatformHandler) Start(parent context.Context) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel == nil {
		h.ctx, h.cancel = context.WithCancel(parent)
		h.wg.Add(1)
		go h.cleanupLoop(h.ctx)
	}
}

func (h *PlatformHandler) cleanupLoop(ctx context.Context) {
	defer h.wg.Done()
	_ = h.CleanupExpired(ctx, time.Now())
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_ = h.CleanupExpired(ctx, now)
		}
	}
}

func (h *PlatformHandler) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.cancel != nil {
		h.cancel()
	}
	for _, cancel := range h.jobs {
		cancel()
	}
	h.jobs = map[string]context.CancelFunc{}
	h.cancel = nil
	h.ctx = nil
	h.mu.Unlock()
	h.wg.Wait()
}

func (h *PlatformHandler) CleanupExpired(ctx context.Context, now time.Time) error {
	files, err := h.repository.ListExpiredLocalFiles(ctx, now)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := h.blobs.Delete(file.StoragePath); err != nil {
			return err
		}
		if err := h.repository.DeleteLocalFile(ctx, file.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func (h *PlatformHandler) handleOpenAIFiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createFile(w, r, "openai")
	case http.MethodGet:
		h.listFiles(w, r, "openai")
	default:
		w.Header().Set("Allow", "GET, POST")
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
	}
}

func (h *PlatformHandler) handleOpenAIFilesSubtree(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/files/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		oaiResourceError(w, http.StatusNotFound, "not_found", "file not found", nil)
		return
	}
	file, err := h.repository.GetLocalFileDialect(r.Context(), parts[0], "openai", time.Now())
	if err != nil {
		h.writePlatformError(w, err, "openai")
		return
	}
	if len(parts) == 2 && parts[1] == "content" {
		if r.Method != http.MethodGet {
			oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
			return
		}
		h.serveFileContent(w, r, file)
		return
	}
	if len(parts) != 1 {
		oaiResourceError(w, http.StatusNotFound, "not_found", "file endpoint not found", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, openAIFileObject(file))
	case http.MethodDelete:
		if err := h.blobs.Delete(file.StoragePath); err != nil {
			h.writePlatformError(w, err, "openai")
			return
		}
		if err := h.repository.DeleteLocalFile(r.Context(), file.ID); err != nil {
			h.writePlatformError(w, err, "openai")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": file.ID, "object": "file", "deleted": true})
	default:
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
	}
}

func (h *PlatformHandler) handleGeminiFiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createFile(w, r, "gemini")
	case http.MethodGet:
		h.listFiles(w, r, "gemini")
	default:
		geminiResourceError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *PlatformHandler) handleGeminiFilesSubtree(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1beta/files/"), "/")
	file, err := h.repository.GetLocalFileDialect(r.Context(), id, "gemini", time.Now())
	if err != nil {
		h.writePlatformError(w, err, "gemini")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, geminiFileObject(file))
	case http.MethodDelete:
		if err := h.blobs.Delete(file.StoragePath); err != nil {
			h.writePlatformError(w, err, "gemini")
			return
		}
		if err := h.repository.DeleteLocalFile(r.Context(), file.ID); err != nil {
			h.writePlatformError(w, err, "gemini")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		geminiResourceError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *PlatformHandler) createFile(w http.ResponseWriter, r *http.Request, dialect string) {
	if err := r.ParseMultipartForm(multipartMemoryLimit); err != nil {
		h.writePlatformBadRequest(w, dialect, "file upload must be valid multipart form data")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	uploads := formUploads(r, "file")
	if len(uploads) != 1 {
		h.writePlatformBadRequest(w, dialect, "exactly one file is required")
		return
	}
	upload := uploads[0]
	source, err := upload.Open()
	if err != nil {
		h.writePlatformBadRequest(w, dialect, "cannot read uploaded file")
		return
	}
	defer source.Close()
	reader := bufio.NewReader(source)
	header, _ := reader.Peek(512)
	mimeType := strings.TrimSpace(upload.Header.Get("Content-Type"))
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(header)
	}
	if mimeType == "application/octet-stream" {
		mimeType = mime.TypeByExtension(filepath.Ext(upload.Filename))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	id := "file_" + strutil.ReqID()
	path, size, digest, err := h.blobs.Put(id, reader, int64(h.requestConfig(r).MaxRequestMB())<<20)
	if err != nil {
		h.writePlatformBadRequest(w, dialect, err.Error())
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = h.blobs.Delete(path)
		}
	}()
	now := time.Now().UTC()
	displayName := filepath.Base(upload.Filename)
	name := displayName
	if dialect == "gemini" {
		name = "files/" + id
	}
	file := repository.LocalFile{ID: id, Dialect: dialect, Name: name, DisplayName: displayName,
		Purpose: strings.TrimSpace(formValue(r, "purpose")), MimeType: mimeType, SizeBytes: size, SHA256: digest,
		StoragePath: path, Status: "processed", MetadataJSON: []byte("{}"), CreatedAt: now.Unix(), ExpiresAt: now.Add(localFileTTL).Unix()}
	hash := sha256Hex([]byte(strings.Join([]string{dialect, displayName, file.Purpose, mimeType, digest}, "\x00")))
	record := repository.IdempotencyRecord{Endpoint: r.Method + " " + routeFamilyForFile(dialect), Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")),
		BodyHash: hash, ResourceKind: "file", ResourceID: id, CreatedAt: now.Unix(), ExpiresAt: now.Add(idempotencyTTL).Unix()}
	resourceID, replay, conflict, err := h.repository.CreateLocalFileIdempotent(r.Context(), file, record)
	if err != nil {
		h.writePlatformError(w, err, dialect)
		return
	}
	if conflict {
		h.writeIdempotencyConflict(w, dialect)
		return
	}
	if replay {
		existing, getErr := h.repository.GetLocalFile(r.Context(), resourceID, time.Now())
		if getErr != nil {
			h.writePlatformError(w, getErr, dialect)
			return
		}
		if dialect == "gemini" {
			writeJSON(w, http.StatusOK, map[string]any{"file": geminiFileObject(existing)})
		} else {
			writeJSON(w, http.StatusOK, openAIFileObject(existing))
		}
		return
	}
	committed = true
	if dialect == "gemini" {
		writeJSON(w, http.StatusOK, map[string]any{"file": geminiFileObject(file)})
	} else {
		writeJSON(w, http.StatusOK, openAIFileObject(file))
	}
}

func (h *PlatformHandler) listFiles(w http.ResponseWriter, r *http.Request, dialect string) {
	limit, _ := strconv.Atoi(firstNonEmptyStr(r.URL.Query().Get("limit"), r.URL.Query().Get("pageSize")))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	afterID := strings.TrimSpace(r.URL.Query().Get("after"))
	if dialect == "gemini" {
		if token := strings.TrimSpace(r.URL.Query().Get("pageToken")); token != "" {
			decoded, decodeErr := base64.RawURLEncoding.DecodeString(token)
			if decodeErr != nil {
				h.writePlatformBadRequest(w, dialect, "invalid pageToken")
				return
			}
			afterID = string(decoded)
		}
	}
	afterCreated := int64(-1)
	if afterID != "" {
		cursor, cursorErr := h.repository.GetLocalFileDialect(r.Context(), afterID, dialect, time.Now())
		if cursorErr != nil {
			h.writePlatformBadRequest(w, dialect, "invalid file cursor")
			return
		}
		afterCreated = cursor.CreatedAt
	}
	files, err := h.repository.ListLocalFiles(r.Context(), dialect, afterCreated, afterID, limit+1, time.Now())
	if err != nil {
		h.writePlatformError(w, err, dialect)
		return
	}
	hasMore := len(files) > limit
	if hasMore {
		files = files[:limit]
	}
	if dialect == "gemini" {
		data := make([]any, 0, len(files))
		for _, file := range files {
			data = append(data, geminiFileObject(file))
		}
		response := map[string]any{"files": data}
		if hasMore && len(files) > 0 {
			response["nextPageToken"] = base64.RawURLEncoding.EncodeToString([]byte(files[len(files)-1].ID))
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	data := make([]any, 0, len(files))
	for _, file := range files {
		data = append(data, openAIFileObject(file))
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data, "has_more": hasMore})
}

func (h *PlatformHandler) serveFileContent(w http.ResponseWriter, r *http.Request, file repository.LocalFile) {
	source, err := h.blobs.Open(file.StoragePath)
	if err != nil {
		h.writePlatformError(w, err, file.Dialect)
		return
	}
	defer source.Close()
	w.Header().Set("Content-Type", file.MimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(file.SizeBytes, 10))
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeDownloadName(file.DisplayName)+`"`)
	_, _ = io.Copy(w, source)
}

func safeDownloadName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	replacer := strings.NewReplacer("\r", "", "\n", "", `"`, "", `\`, "_")
	name = replacer.Replace(name)
	if name == "" || name == "." {
		return "download.bin"
	}
	return name
}

func openAIFileObject(file repository.LocalFile) map[string]any {
	return map[string]any{"id": file.ID, "object": "file", "bytes": file.SizeBytes, "created_at": file.CreatedAt,
		"expires_at": file.ExpiresAt, "filename": file.DisplayName, "purpose": file.Purpose, "status": file.Status}
}

func geminiFileObject(file repository.LocalFile) map[string]any {
	return map[string]any{"name": "files/" + file.ID, "displayName": file.DisplayName, "mimeType": file.MimeType,
		"sizeBytes": strconv.FormatInt(file.SizeBytes, 10), "createTime": time.Unix(file.CreatedAt, 0).UTC().Format(time.RFC3339Nano),
		"expirationTime": time.Unix(file.ExpiresAt, 0).UTC().Format(time.RFC3339Nano), "sha256Hash": file.SHA256,
		"uri": "vproxy://files/" + file.ID, "state": "ACTIVE"}
}

func routeFamilyForFile(dialect string) string {
	if dialect == "gemini" {
		return "/upload/v1beta/files"
	}
	return "/v1/files"
}

func (h *PlatformHandler) handleCachedContents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createCachedContent(w, r)
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
		values, err := h.repository.ListCachedContents(r.Context(), limit, time.Now())
		if err != nil {
			h.writePlatformError(w, err, "gemini")
			return
		}
		data := make([]any, 0, len(values))
		for _, value := range values {
			data = append(data, cachedContentObject(value))
		}
		writeJSON(w, http.StatusOK, map[string]any{"cachedContents": data})
	default:
		geminiResourceError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *PlatformHandler) createCachedContent(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		geminiResourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must be valid JSON")
		return
	}
	contents, ok := body["contents"].([]any)
	if !ok || len(contents) == 0 {
		geminiResourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "contents is required")
		return
	}
	extensions := unknownObjectFields(body, "model", "contents", "systemInstruction", "tools", "displayName", "ttl", "expireTime")
	if strings.EqualFold(h.requestConfig(r).GeminiParameterPolicy(), "strict") && len(extensions) > 0 {
		geminiResourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unsupported cached content field: "+firstSortedKey(extensions))
		return
	}
	now := time.Now().UTC()
	expiresAt, err := cacheExpiration(body, now)
	if err != nil {
		geminiResourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	contentsJSON, _ := json.Marshal(contents)
	systemJSON, _ := json.Marshal(body["systemInstruction"])
	toolsJSON, _ := json.Marshal(body["tools"])
	metadataJSON, _ := json.Marshal(map[string]any{"displayName": body["displayName"], "localExpansion": true, "extensions": extensions})
	value := repository.CachedContent{ID: "cache_" + strutil.ReqID(), Model: stringValueLocal(body["model"]), ContentsJSON: contentsJSON,
		SystemInstructionJSON: systemJSON, ToolsJSON: toolsJSON, MetadataJSON: metadataJSON, CreatedAt: now.Unix(), UpdatedAt: now.Unix(), ExpiresAt: expiresAt.Unix()}
	if err := h.repository.CreateCachedContent(r.Context(), value); err != nil {
		h.writePlatformError(w, err, "gemini")
		return
	}
	writeJSON(w, http.StatusOK, cachedContentObject(value))
}

func (h *PlatformHandler) handleCachedContentsSubtree(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1beta/cachedContents/"), "/")
	value, err := h.repository.GetCachedContent(r.Context(), id, time.Now())
	if err != nil {
		h.writePlatformError(w, err, "gemini")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, cachedContentObject(value))
	case http.MethodPatch:
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			geminiResourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must be valid JSON")
			return
		}
		if unknown := unknownObjectFields(body, "displayName", "ttl", "expireTime"); len(unknown) > 0 {
			geminiResourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "field cannot be updated: "+firstSortedKey(unknown))
			return
		}
		now := time.Now().UTC()
		expiresAt, expirationErr := cacheExpiration(body, now)
		if expirationErr != nil {
			geminiResourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", expirationErr.Error())
			return
		}
		value.UpdatedAt, value.ExpiresAt = now.Unix(), expiresAt.Unix()
		if display, ok := body["displayName"]; ok {
			value.MetadataJSON, _ = json.Marshal(map[string]any{"displayName": display, "localExpansion": true})
		}
		if err := h.repository.UpdateCachedContent(r.Context(), value); err != nil {
			h.writePlatformError(w, err, "gemini")
			return
		}
		writeJSON(w, http.StatusOK, cachedContentObject(value))
	case http.MethodDelete:
		if err := h.repository.DeleteCachedContent(r.Context(), id); err != nil {
			h.writePlatformError(w, err, "gemini")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		geminiResourceError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func unknownObjectFields(object map[string]any, allowed ...string) map[string]any {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	unknown := map[string]any{}
	for key, value := range object {
		if _, ok := allowedSet[key]; !ok {
			unknown[key] = value
		}
	}
	return unknown
}

func firstSortedKey(values map[string]any) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func cachedContentObject(value repository.CachedContent) map[string]any {
	var metadata map[string]any
	var contents []any
	var systemInstruction any
	var tools []any
	_ = json.Unmarshal(value.MetadataJSON, &metadata)
	_ = json.Unmarshal(value.ContentsJSON, &contents)
	_ = json.Unmarshal(value.SystemInstructionJSON, &systemInstruction)
	_ = json.Unmarshal(value.ToolsJSON, &tools)
	return map[string]any{"name": "cachedContents/" + value.ID, "model": value.Model, "displayName": metadata["displayName"],
		"createTime": time.Unix(value.CreatedAt, 0).UTC().Format(time.RFC3339Nano), "updateTime": time.Unix(value.UpdatedAt, 0).UTC().Format(time.RFC3339Nano),
		"expireTime":        time.Unix(value.ExpiresAt, 0).UTC().Format(time.RFC3339Nano),
		"contents":          contents,
		"systemInstruction": systemInstruction,
		"tools":             tools,
		"usageMetadata":     map[string]any{"localExpansion": true, "billingCache": false}}
}

func cacheExpiration(body map[string]any, now time.Time) (time.Time, error) {
	if raw := stringValueLocal(body["expireTime"]); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || !parsed.After(now) || parsed.After(now.Add(maxCacheTTL)) {
			return time.Time{}, errors.New("expireTime must be in the future and no more than 7 days away")
		}
		return parsed.UTC(), nil
	}
	ttl := defaultCacheTTL
	if raw := stringValueLocal(body["ttl"]); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 || parsed > maxCacheTTL {
			return time.Time{}, errors.New("ttl must be a positive duration no greater than 168h")
		}
		ttl = parsed
	}
	return now.Add(ttl), nil
}

func (h *PlatformHandler) handleBatches(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createBatch(w, r, "openai")
	case http.MethodGet:
		h.listBatches(w, r, "openai")
	default:
		oaiError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
	}
}

func (h *PlatformHandler) handleBatchesSubtree(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/batches/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "cancel" {
		h.cancelBatch(w, r, parts[0], "openai")
		return
	}
	if len(parts) != 1 {
		oaiResourceError(w, http.StatusNotFound, "not_found", "batch not found", nil)
		return
	}
	h.batchSubtree(w, r, parts[0], "openai")
}

func (h *PlatformHandler) handleGeminiBatches(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createBatch(w, r, "gemini")
	case http.MethodGet:
		h.listBatches(w, r, "gemini")
	default:
		geminiResourceError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *PlatformHandler) handleGeminiBatchGenerate(w http.ResponseWriter, r *http.Request, model string) {
	var body map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		geminiResourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "request body must be valid JSON")
		return
	}
	inputFileID := stringValueLocal(body["input_file_id"])
	if input, ok := body["inputConfig"].(map[string]any); ok && inputFileID == "" {
		inputFileID = firstNonEmptyStr(stringValueLocal(input["fileName"]), stringValueLocal(input["file_name"]))
	}
	request := createBatchRequest{InputFileID: inputFileID, Endpoint: "/v1beta/models/" + strings.TrimPrefix(model, "models/") + ":generateContent"}
	if metadata, ok := body["metadata"].(map[string]any); ok {
		request.Metadata = metadata
	}
	encoded, _ := json.Marshal(request)
	r.Body = io.NopCloser(bytes.NewReader(encoded))
	h.createBatch(w, r, "gemini")
}

func (h *PlatformHandler) handleGeminiBatchesSubtree(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1beta/batches/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && (parts[1] == "cancel" || parts[1] == "cancelOperation") {
		h.cancelBatch(w, r, parts[0], "gemini")
		return
	}
	if len(parts) != 1 {
		geminiResourceError(w, http.StatusNotFound, "NOT_FOUND", "batch not found")
		return
	}
	h.batchSubtree(w, r, parts[0], "gemini")
}

type createBatchRequest struct {
	InputFileID      string         `json:"input_file_id"`
	Endpoint         string         `json:"endpoint"`
	CompletionWindow string         `json:"completion_window"`
	Metadata         map[string]any `json:"metadata"`
}

func (h *PlatformHandler) createBatch(w http.ResponseWriter, r *http.Request, dialect string) {
	var request createBatchRequest
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		h.writePlatformBadRequest(w, dialect, "request body must be valid JSON")
		return
	}
	request.InputFileID = trimFileName(request.InputFileID)
	if request.InputFileID == "" || !supportedBatchEndpoint(request.Endpoint) {
		h.writePlatformBadRequest(w, dialect, "input_file_id and a supported generation endpoint are required")
		return
	}
	if _, err := h.repository.GetLocalFileDialect(r.Context(), request.InputFileID, dialect, time.Now()); err != nil {
		h.writePlatformError(w, err, dialect)
		return
	}
	requestJSON, _ := json.Marshal(request)
	metadataJSON, _ := json.Marshal(request.Metadata)
	now := time.Now().UTC()
	batch := repository.Batch{ID: "batch_" + strutil.ReqID(), Dialect: dialect, Endpoint: request.Endpoint, InputFileID: request.InputFileID,
		Status: "validating", RequestCountsJSON: []byte(`{"total":0,"completed":0,"failed":0}`), MetadataJSON: metadataJSON,
		ErrorJSON: []byte("null"), CreatedAt: now.Unix(), ExpiresAt: now.Add(batchTTL).Unix()}
	record := repository.IdempotencyRecord{Endpoint: r.Method + " " + batchRoute(dialect), Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")),
		BodyHash: sha256Hex(requestJSON), ResourceKind: "batch", ResourceID: batch.ID, CreatedAt: now.Unix(), ExpiresAt: now.Add(idempotencyTTL).Unix()}
	id, replay, conflict, err := h.repository.CreateBatchIdempotent(r.Context(), batch, record)
	if err != nil {
		h.writePlatformError(w, err, dialect)
		return
	}
	if conflict {
		h.writeIdempotencyConflict(w, dialect)
		return
	}
	if replay {
		batch, err = h.repository.GetBatch(r.Context(), id, time.Now())
		if err != nil {
			h.writePlatformError(w, err, dialect)
			return
		}
	} else if err := h.startBatch(batch); err != nil {
		batch.Status = "failed"
		batch.CompletedAt = time.Now().UTC().Unix()
		batch.ErrorJSON, _ = json.Marshal(map[string]any{"message": err.Error()})
		_, _ = h.repository.UpdateBatchIfActive(context.Background(), batch)
		h.writePlatformError(w, err, dialect)
		return
	}
	writeJSON(w, http.StatusOK, batchObject(batch))
}

func (h *PlatformHandler) startBatch(batch repository.Batch) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ctx == nil || h.ctx.Err() != nil {
		return errors.New("batch runtime is not started")
	}
	ctx, cancel := context.WithCancel(h.ctx)
	h.jobs[batch.ID] = cancel
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer func() {
			h.mu.Lock()
			delete(h.jobs, batch.ID)
			h.mu.Unlock()
		}()
		h.runBatch(ctx, batch)
	}()
	return nil
}

func (h *PlatformHandler) runBatch(ctx context.Context, batch repository.Batch) {
	input, err := h.repository.GetLocalFile(ctx, batch.InputFileID, time.Now())
	if err != nil {
		h.finishBatch(batch, nil, nil, 0, 0, err)
		return
	}
	file, err := h.blobs.Open(input.StoragePath)
	if err != nil {
		h.finishBatch(batch, nil, nil, 0, 0, err)
		return
	}
	defer file.Close()
	batch.Status = "in_progress"
	batch.InProgressAt = time.Now().UTC().Unix()
	_, _ = h.repository.UpdateBatchIfActive(context.Background(), batch)
	var output, failures bytes.Buffer
	completed, failed, total := 0, 0, 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), int(h.cfg.MaxRequestMB())<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return
		}
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		total++
		result, customID, lineErr := h.processBatchLine(ctx, scanner.Bytes(), batch.Endpoint)
		if lineErr != nil {
			failed++
			encoded, _ := json.Marshal(map[string]any{"custom_id": customID, "error": map[string]any{"message": lineErr.Error(), "type": "invalid_request_error"}})
			failures.Write(encoded)
			failures.WriteByte('\n')
			continue
		}
		completed++
		encoded, _ := json.Marshal(map[string]any{"id": "batch_req_" + strutil.ReqID(), "custom_id": customID,
			"response": map[string]any{"status_code": http.StatusOK, "request_id": "req_" + strutil.ReqID(), "body": result}, "error": nil})
		output.Write(encoded)
		output.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		h.finishBatch(batch, output.Bytes(), failures.Bytes(), completed, failed, err)
		return
	}
	h.finishBatch(batch, output.Bytes(), failures.Bytes(), completed, failed, nil)
	_ = total
}

func (h *PlatformHandler) processBatchLine(ctx context.Context, line []byte, endpoint string) (map[string]any, string, error) {
	var request struct {
		CustomID string         `json:"custom_id"`
		Method   string         `json:"method"`
		URL      string         `json:"url"`
		Body     map[string]any `json:"body"`
	}
	if err := json.Unmarshal(line, &request); err != nil {
		return nil, "", fmt.Errorf("invalid JSONL request: %w", err)
	}
	if request.CustomID == "" || strings.ToUpper(request.Method) != http.MethodPost || request.URL != endpoint {
		return nil, request.CustomID, errors.New("batch line custom_id, POST method, and url must match the batch endpoint")
	}
	modelName := stringValueLocal(request.Body["model"])
	if strings.HasPrefix(endpoint, "/v1beta/models/") {
		modelName = strings.TrimSuffix(strings.TrimPrefix(endpoint, "/v1beta/models/"), ":generateContent")
	}
	modelName, _, ok := resolveConfiguredModel(modelName, h.cfg)
	if !ok {
		return nil, request.CustomID, errors.New("batch line references an unavailable model")
	}
	var payload map[string]any
	switch endpoint {
	case "/v1/chat/completions":
		_, converted, err := transform.DefaultRequestConverter().Convert(request.Body, h.cfg)
		if err != nil {
			return nil, request.CustomID, err
		}
		payload = converted
	case "/v1/responses":
		encoded, _ := json.Marshal(request.Body)
		var create responseadapter.CreateRequest
		if err := json.Unmarshal(encoded, &create); err != nil {
			return nil, request.CustomID, err
		}
		converted, _, err := responseadapter.BuildGemini(create, nil)
		if err != nil {
			return nil, request.CustomID, err
		}
		payload = converted
	default:
		payload = request.Body
	}
	if err := h.expandLocalResources(ctx, payload); err != nil {
		return nil, request.CustomID, err
	}
	if h.toolStates != nil {
		if err := h.toolStates.RestoreOpenAIChat(ctx, payload); err != nil {
			return nil, request.CustomID, err
		}
	}
	policy := coremodel.ParsePolicy(h.cfg.GeminiParameterPolicy(), coremodel.PolicyPassthrough)
	if endpoint == "/v1/chat/completions" || endpoint == "/v1/responses" {
		policy = coremodel.ParsePolicy(h.cfg.OpenAIParameterPolicy(), coremodel.PolicyAdaptive)
	}
	if _, err := transform.ApplyModelPolicy(payload, modelName, policy); err != nil {
		return nil, request.CustomID, err
	}
	response, err := h.vc.CompleteChat(ctx, modelName, payload)
	if err != nil {
		return nil, request.CustomID, err
	}
	transform.AssignExternalFunctionCallIDs(response)
	switch endpoint {
	case "/v1/chat/completions":
		converted := transform.DefaultResponseConverter().ToOAI(response, modelName)
		responseID := stringValueLocal(converted["id"])
		if h.toolStates != nil {
			if err := h.toolStates.CaptureResponse(ctx, response, responseID, "", "batch.chat.completions"); err != nil {
				return nil, request.CustomID, err
			}
		}
		return converted, request.CustomID, nil
	case "/v1/responses":
		items, usage, status := responseadapter.OutputItems(response)
		responseID := "resp_" + strutil.ReqID()
		if h.toolStates != nil {
			if err := h.toolStates.CaptureResponse(ctx, response, responseID, "", "batch.responses"); err != nil {
				return nil, request.CustomID, err
			}
		}
		return map[string]any{"id": responseID, "object": "response", "status": status, "model": modelName, "output": items, "usage": usage}, request.CustomID, nil
	default:
		return response, request.CustomID, nil
	}
}

func (h *PlatformHandler) finishBatch(batch repository.Batch, output, failures []byte, completed, failed int, runErr error) {
	current, err := h.repository.GetBatch(context.Background(), batch.ID, time.Now())
	if err != nil || current.Status == "cancelled" {
		return
	}
	batch = current
	batch.Status = "finalizing"
	updated, updateErr := h.repository.UpdateBatchIfActive(context.Background(), batch)
	if updateErr != nil || !updated {
		return
	}
	createdFiles := make([]repository.LocalFile, 0, 2)
	if len(output) > 0 {
		if file, createErr := h.createGeneratedFile("batch_output", batch.Dialect, output, "application/jsonl", batch.ExpiresAt); createErr == nil {
			batch.OutputFileID = file.ID
			createdFiles = append(createdFiles, file)
		} else if runErr == nil {
			runErr = createErr
		}
	}
	if len(failures) > 0 {
		if file, createErr := h.createGeneratedFile("batch_errors", batch.Dialect, failures, "application/jsonl", batch.ExpiresAt); createErr == nil {
			batch.ErrorFileID = file.ID
			createdFiles = append(createdFiles, file)
		} else if runErr == nil {
			runErr = createErr
		}
	}
	batch.RequestCountsJSON, _ = json.Marshal(map[string]any{"total": completed + failed, "completed": completed, "failed": failed})
	batch.CompletedAt = time.Now().UTC().Unix()
	if runErr != nil {
		batch.Status = "failed"
		batch.ErrorJSON, _ = json.Marshal(map[string]any{"message": runErr.Error()})
	} else {
		batch.Status = "completed"
	}
	updated, updateErr = h.repository.UpdateBatchIfActive(context.Background(), batch)
	if updateErr != nil || !updated {
		for _, file := range createdFiles {
			_ = h.blobs.Delete(file.StoragePath)
			_ = h.repository.DeleteLocalFile(context.Background(), file.ID)
		}
	}
}

func (h *PlatformHandler) createGeneratedFile(purpose, dialect string, data []byte, mimeType string, expiresAt int64) (repository.LocalFile, error) {
	id := "file_" + strutil.ReqID()
	path, size, digest, err := h.blobs.Put(id, bytes.NewReader(data), int64(len(data))+1)
	if err != nil {
		return repository.LocalFile{}, err
	}
	file := repository.LocalFile{ID: id, Dialect: dialect, Name: purpose + ".jsonl", DisplayName: purpose + ".jsonl", Purpose: purpose,
		MimeType: mimeType, SizeBytes: size, SHA256: digest, StoragePath: path, Status: "processed", MetadataJSON: []byte("{}"), CreatedAt: time.Now().UTC().Unix(), ExpiresAt: expiresAt}
	if err := h.repository.CreateLocalFile(context.Background(), file); err != nil {
		_ = h.blobs.Delete(path)
		return repository.LocalFile{}, err
	}
	return file, nil
}

func (h *PlatformHandler) listBatches(w http.ResponseWriter, r *http.Request, dialect string) {
	limit, _ := strconv.Atoi(firstNonEmptyStr(r.URL.Query().Get("limit"), r.URL.Query().Get("pageSize")))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	afterID := strings.TrimSpace(r.URL.Query().Get("after"))
	if dialect == "gemini" {
		if token := strings.TrimSpace(r.URL.Query().Get("pageToken")); token != "" {
			decoded, decodeErr := base64.RawURLEncoding.DecodeString(token)
			if decodeErr != nil {
				h.writePlatformBadRequest(w, dialect, "invalid pageToken")
				return
			}
			afterID = string(decoded)
		}
	}
	afterCreated := int64(-1)
	if afterID != "" {
		cursor, cursorErr := h.repository.GetBatchDialect(r.Context(), afterID, dialect, time.Now())
		if cursorErr != nil {
			h.writePlatformBadRequest(w, dialect, "invalid batch cursor")
			return
		}
		afterCreated = cursor.CreatedAt
	}
	values, err := h.repository.ListBatches(r.Context(), dialect, afterCreated, afterID, limit+1, time.Now())
	if err != nil {
		h.writePlatformError(w, err, dialect)
		return
	}
	hasMore := len(values) > limit
	if hasMore {
		values = values[:limit]
	}
	data := make([]any, 0, len(values))
	for _, value := range values {
		data = append(data, batchObject(value))
	}
	if dialect == "gemini" {
		response := map[string]any{"batches": data}
		if hasMore && len(values) > 0 {
			response["nextPageToken"] = base64.RawURLEncoding.EncodeToString([]byte(values[len(values)-1].ID))
		}
		writeJSON(w, http.StatusOK, response)
	} else {
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data, "has_more": hasMore})
	}
}

func (h *PlatformHandler) batchSubtree(w http.ResponseWriter, r *http.Request, id, dialect string) {
	batch, err := h.repository.GetBatchDialect(r.Context(), id, dialect, time.Now())
	if err != nil {
		h.writePlatformError(w, err, dialect)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, batchObject(batch))
	case http.MethodDelete:
		if !batchTerminal(batch.Status) {
			h.writePlatformBadRequest(w, dialect, "active batch must be cancelled before deletion")
			return
		}
		if err := h.repository.DeleteBatch(r.Context(), id); err != nil {
			h.writePlatformError(w, err, dialect)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "batch", "deleted": true})
	default:
		h.writePlatformBadRequest(w, dialect, "method not allowed")
	}
}

func (h *PlatformHandler) cancelBatch(w http.ResponseWriter, r *http.Request, id, dialect string) {
	if r.Method != http.MethodPost {
		h.writePlatformBadRequest(w, dialect, "method not allowed")
		return
	}
	if _, err := h.repository.GetBatchDialect(r.Context(), id, dialect, time.Now()); err != nil {
		h.writePlatformError(w, err, dialect)
		return
	}
	h.mu.Lock()
	if cancel := h.jobs[id]; cancel != nil {
		cancel()
		delete(h.jobs, id)
	}
	h.mu.Unlock()
	batch, err := h.repository.CancelBatch(r.Context(), id, time.Now().UTC().Unix())
	if err != nil {
		h.writePlatformError(w, err, dialect)
		return
	}
	writeJSON(w, http.StatusOK, batchObject(batch))
}

func batchObject(batch repository.Batch) map[string]any {
	var counts, metadata map[string]any
	var batchError any
	_ = json.Unmarshal(batch.RequestCountsJSON, &counts)
	_ = json.Unmarshal(batch.MetadataJSON, &metadata)
	_ = json.Unmarshal(batch.ErrorJSON, &batchError)
	return map[string]any{"id": batch.ID, "name": "batches/" + batch.ID, "object": "batch", "endpoint": batch.Endpoint,
		"input_file_id": nilIfEmpty(batch.InputFileID), "output_file_id": nilIfEmpty(batch.OutputFileID), "error_file_id": nilIfEmpty(batch.ErrorFileID),
		"status": batch.Status, "request_counts": counts, "metadata": metadata, "errors": batchError, "created_at": batch.CreatedAt,
		"in_progress_at": batch.InProgressAt, "completed_at": batch.CompletedAt, "cancelled_at": batch.CancelledAt, "expires_at": batch.ExpiresAt}
}

func supportedBatchEndpoint(endpoint string) bool {
	return endpoint == "/v1/chat/completions" || endpoint == "/v1/responses" ||
		(strings.HasPrefix(endpoint, "/v1beta/models/") && strings.HasSuffix(endpoint, ":generateContent"))
}

func batchTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "expired" || status == "cancelled"
}

func batchRoute(dialect string) string {
	if dialect == "gemini" {
		return "/v1beta/batches"
	}
	return "/v1/batches"
}

func trimFileName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "files/")
	return value
}

func (h *PlatformHandler) writePlatformBadRequest(w http.ResponseWriter, dialect, message string) {
	if dialect == "gemini" {
		geminiResourceError(w, http.StatusBadRequest, "INVALID_ARGUMENT", message)
		return
	}
	oaiResourceError(w, http.StatusBadRequest, "invalid_parameter", message, nil)
}

func (h *PlatformHandler) writeIdempotencyConflict(w http.ResponseWriter, dialect string) {
	if dialect == "gemini" {
		geminiResourceError(w, http.StatusConflict, "ALREADY_EXISTS", "Idempotency-Key was already used with different content")
		return
	}
	oaiResourceError(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with different content", nil)
}

func (h *PlatformHandler) writePlatformError(w http.ResponseWriter, err error, dialect string) {
	if errors.Is(err, sql.ErrNoRows) {
		if dialect == "gemini" {
			geminiResourceError(w, http.StatusNotFound, "NOT_FOUND", "resource not found")
		} else {
			oaiResourceError(w, http.StatusNotFound, "not_found", "resource not found", nil)
		}
		return
	}
	if dialect == "gemini" {
		geminiResourceError(w, http.StatusInternalServerError, "INTERNAL", "local resource operation failed")
	} else {
		oaiResourceError(w, http.StatusInternalServerError, "internal_error", "local resource operation failed", nil)
	}
}

func (h *PlatformHandler) expandLocalResources(ctx context.Context, payload map[string]any) error {
	if h == nil {
		return nil
	}
	if raw := stringValueLocal(payload["cachedContent"]); raw != "" {
		id := strings.TrimPrefix(raw, "cachedContents/")
		cached, err := h.repository.GetCachedContent(ctx, id, time.Now())
		if err != nil {
			return fmt.Errorf("cached content state is not available: %w", err)
		}
		var contents []any
		if err := json.Unmarshal(cached.ContentsJSON, &contents); err != nil {
			return err
		}
		current, _ := payload["contents"].([]any)
		payload["contents"] = append(contents, current...)
		var system any
		if json.Unmarshal(cached.SystemInstructionJSON, &system) == nil && system != nil && payload["systemInstruction"] == nil {
			payload["systemInstruction"] = system
		}
		var tools []any
		if json.Unmarshal(cached.ToolsJSON, &tools) == nil && len(tools) > 0 {
			currentTools, _ := payload["tools"].([]any)
			payload["tools"] = append(tools, currentTools...)
		}
		delete(payload, "cachedContent")
	}
	return h.expandFileParts(ctx, payload)
}

func (h *PlatformHandler) expandFileParts(ctx context.Context, value any) error {
	switch current := value.(type) {
	case map[string]any:
		if fileData, ok := current["fileData"].(map[string]any); ok {
			uri := stringValueLocal(fileData["fileUri"])
			id := trimFileName(strings.TrimPrefix(uri, "vproxy://files/"))
			if strings.HasPrefix(id, "file_") {
				file, err := h.repository.GetLocalFile(ctx, id, time.Now())
				if err != nil {
					return fmt.Errorf("local file state is not available: %w", err)
				}
				data, err := h.blobs.Read(file.StoragePath, int64(h.cfg.MaxRequestMB())<<20)
				if err != nil {
					return err
				}
				delete(current, "fileData")
				current["inlineData"] = map[string]any{"mimeType": file.MimeType, "data": base64.StdEncoding.EncodeToString(data)}
			}
		}
		for _, child := range current {
			if err := h.expandFileParts(ctx, child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range current {
			if err := h.expandFileParts(ctx, child); err != nil {
				return err
			}
		}
	}
	return nil
}
