package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Routes returns the precompiled Chi router for /api/admin sub-routes.
func (adm *AdminHandler) Routes() http.Handler {
	adm.routesOnce.Do(func() {
		adm.router = adm.buildAdminRoutes()
	})
	return adm.router
}

func (adm *AdminHandler) buildAdminRoutes() http.Handler {
	r := chi.NewRouter()
	// Public authentication endpoints
	r.Post("/login", adm.adminLogin)
	r.Get("/check-auth", adm.adminCheckAuth)

	// Protected admin sub-router
	r.Group(func(protected chi.Router) {
		protected.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if !requireAdmin(req) {
					adm.adminUnauthorized(w)
					return
				}
				next.ServeHTTP(w, req)
			})
		})

		// Dynamic keys deletion
		protected.Delete("/keys/{id}", func(w http.ResponseWriter, req *http.Request) {
			id := chi.URLParam(req, "id")
			adm.adminDeleteKey(w, req, id)
		})

		// Keys
		protected.Route("/keys", func(kr chi.Router) {
			kr.Get("/", adm.adminGetKeys)
			kr.Post("/", adm.adminAddKey)
		})

		// Nodes
		protected.Route("/nodes", func(nr chi.Router) {
			nr.Get("/", adm.adminGetNodes)
			nr.Delete("/", adm.adminDeleteNode)
			nr.Post("/test", adm.adminTestNode)
			nr.Post("/enable", adm.adminEnableNode)
			nr.Post("/test-all", adm.adminTestAll)
			nr.Get("/test-progress", adm.adminGetTestProgress)
			nr.Post("/test-pause", adm.adminTestPause)
			nr.Post("/test-resume", adm.adminTestResume)
			nr.Post("/test-terminate", adm.adminTestTerminate)
			nr.Post("/deduplicate", adm.adminDedupNodes)
			nr.Get("/deduplicate/preview", adm.adminPreviewDedupNodes)
			nr.Delete("/disabled", adm.adminDeleteDisabledNodes)
			nr.Post("/import", adm.adminImportNodes)
			nr.Post("/import-json", adm.adminImportNodesJson)
			nr.Post("/batch-disable", adm.adminBatchDisableNodes)
			nr.Post("/batch-enable", adm.adminBatchEnableNodes)
			nr.Post("/batch-delete", adm.adminBatchDeleteNodes)
			nr.Post("/sort", adm.adminSortNodesByLatency)
		})
		protected.Post("/use-node", adm.adminUseNode)

		// Subscriptions
		protected.Route("/subscriptions", func(sr chi.Router) {
			sr.Post("/fetch", adm.adminFetchSub)
			sr.Get("/list", adm.adminListSubscriptions)
			sr.Post("/save", adm.adminSaveSubscription)
			sr.Post("/delete", adm.adminDeleteSubscription)
			sr.Post("/update", adm.adminUpdateSubscriptions)
			sr.Post("/custom_ua/save", adm.adminSaveCustomUA)
			sr.Post("/custom_ua/delete", adm.adminDeleteCustomUA)
		})

		// Proxy Nodes
		protected.Route("/proxy-nodes", func(pr chi.Router) {
			pr.Get("/", adm.adminListProxyNodes)
			pr.Delete("/", adm.adminDeleteProxyNode)
			pr.Post("/import", adm.adminImportProxyNode)
			pr.Post("/import-batch", adm.adminImportProxyNodesBatch)
			pr.Post("/enable-batch", func(w http.ResponseWriter, req *http.Request) {
				adm.adminSetProxyNodesEnabled(w, req, true)
			})
			pr.Post("/disable-batch", func(w http.ResponseWriter, req *http.Request) {
				adm.adminSetProxyNodesEnabled(w, req, false)
			})
			pr.Post("/delete-batch", adm.adminDeleteProxyNodesBatch)
			pr.Delete("/disabled", adm.adminDeleteDisabledProxyNodes)
			pr.Post("/enable", adm.adminEnableProxyNode)
			pr.Post("/disable", adm.adminDisableProxyNode)
			pr.Post("/test", adm.adminTestProxyNode)
			pr.Post("/test-batch", adm.adminBatchTestProxyNodes)
			pr.Get("/test-progress", adm.adminGetProxyTestProgress)
		})

		// Backgrounds
		protected.Post("/upload-bg", adm.adminUploadBg)
		protected.Post("/delete-bg", adm.adminDeleteBg)
		protected.Get("/list-bgs", adm.adminListBgs)

		// System
		protected.Post("/logout", adm.adminLogout)
		protected.Post("/password", adm.adminChangePassword)
		protected.Get("/settings", adm.adminGetSettings)
		protected.Put("/settings", adm.adminPutSettings)
		protected.Get("/stats", adm.adminGetStats)
		protected.Post("/stats/reset", adm.adminResetStats)
		protected.Get("/log", adm.adminGetLog)
		protected.Get("/models", adm.adminGetModels)
		protected.Put("/models", adm.adminPutModels)
	})

	return r
}

func (adm *AdminHandler) handleAdminAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin")
	if path == "" {
		path = "/"
	}
	if path != r.URL.Path {
		r2 := r.Clone(r.Context())
		r2.URL.Path = path
		adm.Routes().ServeHTTP(w, r2)
		return
	}
	adm.Routes().ServeHTTP(w, r)
}
