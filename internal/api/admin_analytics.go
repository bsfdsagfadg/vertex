package api

import (
	"github.com/bsfdsagfadg/vertex/internal/db"
	"net/http"
	"strconv"
	"time"
)

func (adm *AdminHandler) adminGetCallLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("page_size"))
	now := time.Now()
	var start, end int64
	switch q.Get("range") {
	case "today":
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	case "7d":
		start = now.AddDate(0, 0, -7).Unix()
	case "30d":
		start = now.AddDate(0, 0, -30).Unix()
	}
	res, err := db.QueryCallLogs(db.CallLogQuery{KeyName: q.Get("key_name"), Model: q.Get("model"), Status: q.Get("status"), StartTime: start, EndTime: end, Page: page, PageSize: size})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("查询调用统计失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, res)
}
func (adm *AdminHandler) adminClearCallLogs(w http.ResponseWriter, _ *http.Request) {
	if err := db.ClearCallLogs(); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("清空调用统计失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
