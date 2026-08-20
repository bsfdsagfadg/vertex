package api

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/admin"
	"github.com/bsfdsagfadg/vertex/internal/subscriptions"
)

type AdminHandler struct {
	handler
	subscriptionService *subscriptions.Service
	routesOnce          sync.Once
	router              http.Handler
	nodeTestMu          sync.Mutex
	nodeTestCancel      context.CancelFunc
	nodeTestGeneration  uint64
	proxyTestMu         sync.Mutex
	proxyTestState      entryProxyTestProgress
	proxyTestGeneration uint64
}

func (adm *AdminHandler) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/admin" {
		http.Redirect(w, r, "/admin/", http.StatusFound)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/admin/")
	if name == "" {
		name = "admin.html"
	}
	data, err := fs.ReadFile(admin.Assets, "assets/"+name)
	if err != nil {
		oaiError(w, http.StatusNotFound, "not found", "invalid_request_error")
		return
	}
	w.Header().Set("Content-Type", contentTypeFor(name))
	if strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".css") || strings.HasSuffix(name, ".js") {
		// Embedded assets change only when the binary changes. They deliberately
		// share stable URLs, so caching them would keep the previous frontend
		// alive after an upgrade until users perform a hard refresh.
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	_, _ = w.Write(data)
}

func contentTypeFor(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func (adm *AdminHandler) adminUploadBg(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		adm.adminMethodNotAllowed(w)
		return
	}

	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr("解析上传文件失败 (parse error)"))
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr("未找到文件字段 (missing file)"))
		return
	}
	defer file.Close()

	assetsDir := filepath.Join(filepath.Dir(adm.cfg.ConfigDir()), "assets")
	_ = os.MkdirAll(assetsDir, 0o755)

	filename := fmt.Sprintf("background%d.jpg", time.Now().UnixMilli())
	targetPath := filepath.Join(assetsDir, filename)

	out, err := os.Create(targetPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("无法保存文件 (create error)"))
		return
	}
	defer out.Close()

	if _, err = io.Copy(out, file); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("保存文件失败 (copy error)"))
		return
	}

	bgURL := "url('/assets/" + filename + "')"
	err = adm.writeSettings(map[string]any{"background_image": bgURL})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("更新配置失败 (save config error)"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": bgURL})
}

func (adm *AdminHandler) adminDeleteBg(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		adm.adminMethodNotAllowed(w)
		return
	}
	var body struct {
		Filename string `json:"filename"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if body.Filename == "" || strings.Contains(body.Filename, "/") || strings.Contains(body.Filename, "\\") {
		writeJSON(w, http.StatusBadRequest, adminErr("文件名无效"))
		return
	}
	if !strings.HasPrefix(body.Filename, "background") {
		writeJSON(w, http.StatusForbidden, adminErr("无权删除该文件"))
		return
	}

	assetsDir := filepath.Join(filepath.Dir(adm.cfg.ConfigDir()), "assets")
	targetPath := filepath.Join(assetsDir, body.Filename)
	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		writeJSON(w, http.StatusInternalServerError, adminErr("删除文件失败"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminListBgs(w http.ResponseWriter, r *http.Request) {
	assetsDir := filepath.Join(filepath.Dir(adm.cfg.ConfigDir()), "assets")
	files, err := os.ReadDir(assetsDir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": []string{}})
		return
	}

	var bgs []string
	for _, f := range files {
		if !f.IsDir() && strings.HasPrefix(f.Name(), "background") {
			bgs = append(bgs, f.Name())
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": bgs})
}

func (adm *AdminHandler) adminGetLog(w http.ResponseWriter, r *http.Request) {
	logPath := filepath.Join(filepath.Dir(adm.cfg.ConfigDir()), "logs", "logs_latest.log")

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		logPath = "logs/logs_latest.log"
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "content": ""})
			return
		}
		writeJSON(w, http.StatusInternalServerError, adminErr("无法读取日志文件 (read error)"))
		return
	}

	lines := strings.Split(string(data), "\n")
	var validLines []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			validLines = append(validLines, l)
		}
	}
	if len(validLines) > 200 {
		validLines = validLines[len(validLines)-200:]
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "content": strings.Join(validLines, "\n")})
}
