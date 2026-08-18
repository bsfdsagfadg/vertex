package api

import (
	"log"
	"net/http"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

type nodeView struct {
	nodes.Node
	Supported bool `json:"supported"`
}

func (adm *AdminHandler) adminGetNodes(w http.ResponseWriter, _ *http.Request) {
	list := nodes.LoadNodes()
	var enabledCount, disabledCount int
	uris := make([]string, 0, len(list))
	for _, n := range list {
		if n.Disabled {
			disabledCount++
		} else {
			enabledCount++
		}
		uris = append(uris, n.RawURI)
	}
	supportedMap := transport.CheckSupportedBatch(uris)
	views := make([]nodeView, 0, len(list))
	for _, n := range list {
		views = append(views, nodeView{Node: n, Supported: supportedMap[n.RawURI]})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":          views,
		"health":         nodes.LoadHealth(),
		"total":          len(list),
		"enabled_count":  enabledCount,
		"disabled_count": disabledCount,
	})
}

func (adm *AdminHandler) adminEnableNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	ok := nodes.EnableNode(body.RawURI)
	log.Printf("[Admin] [EnableNode] 启用节点 %s: %v", nodes.GetNodeName(body.RawURI), ok)
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok})
}

func (adm *AdminHandler) adminDedupNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed_count": nodes.DedupNodes()})
}

func (adm *AdminHandler) adminDeleteDisabledNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_count": nodes.DeleteDisabled()})
}

func (adm *AdminHandler) adminUseNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if body.RawURI == "" {
		_ = config.WriteSettings(map[string]any{"active_node_uri": "", "parallel_pool_enabled": true})
	} else {
		_ = config.WriteSettings(map[string]any{"active_node_uri": body.RawURI, "parallel_pool_enabled": false})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminSortNodesByLatency(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Desc bool `json:"desc"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	if body.Desc {
		nodes.SortNodesByLatencyDesc()
	} else {
		nodes.SortNodesByLatency()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminDeleteNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RawURI string `json:"raw_uri"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	nodes.DeleteNode(body.RawURI)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminBatchDisableNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URIs []string `json:"uris"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [BatchDisable] 批量禁用 %d 个节点", len(body.URIs))
	nodes.BatchUpdateNodesDisabled(body.URIs, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminBatchEnableNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URIs []string `json:"uris"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [BatchEnable] 批量启用 %d 个节点", len(body.URIs))
	nodes.BatchUpdateNodesDisabled(body.URIs, false)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminBatchDeleteNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URIs []string `json:"uris"`
	}
	if !adm.decodeAdminBody(w, r, &body) {
		return
	}
	log.Printf("[Admin] [BatchDelete] 批量删除 %d 个节点", len(body.URIs))
	nodes.BatchDeleteNodes(body.URIs)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
