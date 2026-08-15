package api

import (
	"log"
	"net/http"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/importer"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

const subscriptionFetchUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func (adm *AdminHandler) adminImportNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text    string `json:"text"`
		Replace bool   `json:"replace"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [ImportNodes] 收到优选节点文件导入请求, 替换模式: %v", body.Replace)

	newNodes := importer.ParseImportedNodes(strings.TrimSpace(body.Text))
	if body.Replace {
		log.Printf("[Admin] [ImportNodes] 替换模式，正在清除全部已有候选节点")
		for _, cn := range nodes.LoadNodes() {
			nodes.DeleteNode(cn.RawURI)
		}
	}

	log.Printf("[Admin] [ImportNodes] 正在合并导入的新节点数量: %d", len(newNodes))
	nodes.MergeNodes(newNodes)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(newNodes)})
}