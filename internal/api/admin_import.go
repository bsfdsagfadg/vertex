package api

import (
	"log"
	"net/http"
	"strings"
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

	newNodes := adm.deps.Imports.Parse(strings.TrimSpace(body.Text))
	adm.markUnsupportedImports(newNodes)
	if body.Replace {
		log.Printf("[Admin] [ImportNodes] 替换模式，正在清除全部已有候选节点")
		existing := adm.deps.Exit.LoadNodes()
		uris := make([]string, 0, len(existing))
		for _, cn := range existing {
			uris = append(uris, cn.RawURI)
		}
		adm.deps.Exit.BatchDeleteNodes(uris)
	}

	log.Printf("[Admin] [ImportNodes] 正在合并导入的新节点数量: %d", len(newNodes))
	adm.deps.Exit.MergeNodes(newNodes)
	uris := make([]string, 0, len(newNodes))
	for _, cn := range newNodes {
		uris = append(uris, cn.RawURI)
	}
	adm.deps.IR.Prewarm(uris)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(newNodes)})
}
