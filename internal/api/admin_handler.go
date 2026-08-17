package api

import (
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/admin"
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/subscriptions"
)

type AdminHandler struct {
	handler
	subscriptionService *subscriptions.Service
}

func (adm *AdminHandler) handleAdminAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin")
	log.Printf("[Server] [AdminAPI] 收到请求: %s %s", r.Method, path)

	// 1. 公开鉴权接口（无需管理员会话）
	switch path {
	case "/login":
		adm.adminLogin(w, r)
		return
	case "/check-auth":
		adm.adminCheckAuth(w, r)
		return
	}

	// 2. 统一会话权限拦截
	if !requireAdmin(r) {
		adm.adminUnauthorized(w)
		return
	}

	// 3. 动态路径分发
	if strings.HasPrefix(path, "/keys/") {
		adm.adminDeleteKey(w, r, strings.TrimPrefix(path, "/keys/"))
		return
	}

	// 4. 子模块路由分发
	switch {
	case strings.HasPrefix(path, "/nodes") || path == "/use-node":
		adm.dispatchNodesAPI(w, r, path)
	case strings.HasPrefix(path, "/subscriptions"):
		adm.dispatchSubscriptionsAPI(w, r, path)
	case strings.HasPrefix(path, "/proxy-nodes"):
		adm.dispatchProxyNodesAPI(w, r, path)
	case strings.HasSuffix(path, "-bg") || path == "/list-bgs":
		adm.dispatchBackgroundsAPI(w, r, path)
	default:
		adm.dispatchSystemAPI(w, r, path)
	}
}

func (adm *AdminHandler) dispatchNodesAPI(w http.ResponseWriter, r *http.Request, path string) {
	switch path {
	case "/nodes":
		switch r.Method {
		case http.MethodGet:
			adm.adminGetNodes(w, r)
		case http.MethodDelete:
			adm.adminDeleteNode(w, r)
		default:
			adm.adminMethodNotAllowed(w)
		}
	case "/nodes/test":
		adm.adminTestNode(w, r)
	case "/nodes/enable":
		adm.adminEnableNode(w, r)
	case "/nodes/test-all":
		adm.adminTestAll(w, r)
	case "/nodes/test-progress":
		if r.Method == http.MethodGet {
			adm.adminGetTestProgress(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/nodes/test-pause":
		adm.adminTestPause(w, r)
	case "/nodes/test-resume":
		adm.adminTestResume(w, r)
	case "/nodes/test-terminate":
		adm.adminTestTerminate(w, r)
	case "/nodes/deduplicate":
		if r.Method == http.MethodPost {
			adm.adminDedupNodes(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/nodes/deduplicate/preview":
		if r.Method == http.MethodGet {
			adm.adminPreviewDedupNodes(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/nodes/disabled":
		adm.adminDeleteDisabledNodes(w, r)
	case "/nodes/import":
		adm.adminImportNodes(w, r)
	case "/nodes/import-json":
		adm.adminImportNodesJson(w, r)
	case "/use-node":
		adm.adminUseNode(w, r)
	case "/nodes/batch-disable":
		adm.adminBatchDisableNodes(w, r)
	case "/nodes/batch-enable":
		adm.adminBatchEnableNodes(w, r)
	case "/nodes/batch-delete":
		adm.adminBatchDeleteNodes(w, r)
	case "/nodes/sort":
		adm.adminSortNodesByLatency(w, r)
	default:
		writeJSON(w, http.StatusNotFound, adminErr("未知节点接口 (not found)"))
	}
}

func (adm *AdminHandler) dispatchSubscriptionsAPI(w http.ResponseWriter, r *http.Request, path string) {
	switch path {
	case "/subscriptions/fetch":
		if r.Method == http.MethodPost {
			adm.adminFetchSub(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/subscriptions/list":
		if r.Method == http.MethodGet {
			adm.adminListSubscriptions(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/subscriptions/save":
		if r.Method == http.MethodPost {
			adm.adminSaveSubscription(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/subscriptions/delete":
		if r.Method == http.MethodPost {
			adm.adminDeleteSubscription(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/subscriptions/update":
		if r.Method == http.MethodPost {
			adm.adminUpdateSubscriptions(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/subscriptions/custom_ua/save":
		if r.Method == http.MethodPost {
			adm.adminSaveCustomUA(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/subscriptions/custom_ua/delete":
		if r.Method == http.MethodPost {
			adm.adminDeleteCustomUA(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	default:
		writeJSON(w, http.StatusNotFound, adminErr("未知订阅接口 (not found)"))
	}
}

func (adm *AdminHandler) dispatchProxyNodesAPI(w http.ResponseWriter, r *http.Request, path string) {
	switch path {
	case "/proxy-nodes":
		switch r.Method {
		case http.MethodGet:
			adm.adminListProxyNodes(w, r)
		case http.MethodDelete:
			adm.adminDeleteProxyNode(w, r)
		default:
			adm.adminMethodNotAllowed(w)
		}
	case "/proxy-nodes/import":
		if r.Method == http.MethodPost {
			adm.adminImportProxyNode(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/proxy-nodes/import-batch":
		if r.Method == http.MethodPost {
			adm.adminImportProxyNodesBatch(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/proxy-nodes/enable-batch":
		if r.Method == http.MethodPost {
			adm.adminSetProxyNodesEnabled(w, r, true)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/proxy-nodes/disable-batch":
		if r.Method == http.MethodPost {
			adm.adminSetProxyNodesEnabled(w, r, false)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/proxy-nodes/delete-batch":
		if r.Method == http.MethodPost {
			adm.adminDeleteProxyNodesBatch(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/proxy-nodes/disabled":
		if r.Method == http.MethodDelete {
			adm.adminDeleteDisabledProxyNodes(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/proxy-nodes/enable":
		if r.Method == http.MethodPost {
			adm.adminEnableProxyNode(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/proxy-nodes/disable":
		if r.Method == http.MethodPost {
			adm.adminDisableProxyNode(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/proxy-nodes/test":
		if r.Method == http.MethodPost {
			adm.adminTestProxyNode(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/proxy-nodes/test-batch":
		if r.Method == http.MethodPost {
			adm.adminBatchTestProxyNodes(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/proxy-nodes/test-progress":
		if r.Method == http.MethodGet {
			adm.adminGetProxyTestProgress(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	default:
		writeJSON(w, http.StatusNotFound, adminErr("未知入口代理接口 (not found)"))
	}
}

func (adm *AdminHandler) dispatchBackgroundsAPI(w http.ResponseWriter, r *http.Request, path string) {
	switch path {
	case "/upload-bg":
		adm.adminUploadBg(w, r)
	case "/delete-bg":
		adm.adminDeleteBg(w, r)
	case "/list-bgs":
		if r.Method == http.MethodGet {
			adm.adminListBgs(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	default:
		writeJSON(w, http.StatusNotFound, adminErr("未知背景接口 (not found)"))
	}
}

func (adm *AdminHandler) dispatchSystemAPI(w http.ResponseWriter, r *http.Request, path string) {
	switch path {
	case "/logout":
		adm.adminLogout(w, r)
	case "/password":
		if r.Method == http.MethodPost {
			adm.adminChangePassword(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/settings":
		switch r.Method {
		case http.MethodGet:
			adm.adminGetSettings(w, r)
		case http.MethodPut:
			adm.adminPutSettings(w, r)
		default:
			adm.adminMethodNotAllowed(w)
		}
	case "/stats":
		adm.adminGetStats(w, r)
	case "/stats/reset":
		adm.adminResetStats(w, r)
	case "/log":
		if r.Method == http.MethodGet {
			adm.adminGetLog(w, r)
		} else {
			adm.adminMethodNotAllowed(w)
		}
	case "/keys":
		switch r.Method {
		case http.MethodGet:
			adm.adminGetKeys(w, r)
		case http.MethodPost:
			adm.adminAddKey(w, r)
		default:
			adm.adminMethodNotAllowed(w)
		}
	case "/models":
		switch r.Method {
		case http.MethodGet:
			adm.adminGetModels(w, r)
		case http.MethodPut:
			adm.adminPutModels(w, r)
		default:
			adm.adminMethodNotAllowed(w)
		}
	default:
		writeJSON(w, http.StatusNotFound, adminErr("未知接口 (not found)"))
	}
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
	w.Header().Set("Cache-Control", "public, max-age=3600")
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
	err = config.WriteSettings(map[string]any{"background_image": bgURL})
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
