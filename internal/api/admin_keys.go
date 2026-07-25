package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/config"
)

func (adm *AdminHandler) adminGetKeys(w http.ResponseWriter, _ *http.Request) {
	entries, err := adm.keys.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("读取密钥失败 (failed to read keys)"))
		return
	}
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"name":        e.Name,
			"key":         e.Key,
			"key_masked":  maskKey(e.Key),
			"description": e.Description,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func (adm *AdminHandler) adminAddKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Key         string `json:"key"`
		Description string `json:"description"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	key := strings.TrimSpace(body.Key)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, adminErr("名称不能为空 (name is required)"))
		return
	}
	if strings.Contains(name, ":") {
		writeJSON(w, http.StatusBadRequest, adminErr("名称不能包含冒号 (name must not contain ':')"))
		return
	}
	if key == "" {
		key = generateAPIKey()
	} else if !strings.HasPrefix(key, "sk-") {
		writeJSON(w, http.StatusBadRequest, adminErr("密钥必须以 sk- 开头 (key must start with 'sk-')"))
		return
	}
	if err := adm.keys.Add(name, key, body.Description); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("写入密钥失败 (failed to write keys)"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": key})
}

func (adm *AdminHandler) adminDeleteKey(w http.ResponseWriter, r *http.Request, rawName string) {
	if r.Method != http.MethodDelete {
		adm.adminMethodNotAllowed(w)
		return
	}
	name := rawName
	if dec, err := url.PathUnescape(rawName); err == nil {
		name = dec
	}
	ok, err := adm.keys.Delete(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("删除密钥失败 (failed to delete key)"))
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, adminErr("未找到该密钥 (key not found)"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminGetModels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"models":    config.BaseModels(),
		"alias_map": config.AliasMap(),
	})
}

func (adm *AdminHandler) adminPutModels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Models   []string          `json:"models"`
		AliasMap map[string]string `json:"alias_map"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	cleaned := make([]string, 0, len(body.Models))
	for _, m := range body.Models {
		if m = strings.TrimSpace(m); m != "" {
			cleaned = append(cleaned, m)
		}
	}
	if len(cleaned) == 0 {
		writeJSON(w, http.StatusBadRequest, adminErr("模型列表不能为空 (models must not be empty)"))
		return
	}
	alias := map[string]string{}
	for k, v := range body.AliasMap {
		if k, v = strings.TrimSpace(k), strings.TrimSpace(v); k != "" && v != "" {
			alias[k] = v
		}
	}
	if err := config.WriteModels(cleaned, alias); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("写入模型失败 (failed to write models)"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
