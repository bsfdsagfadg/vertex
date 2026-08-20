package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

//nolint:gochecknoglobals // Fallback UA list
var fallbackUAs = []string{
	"clash-verge/v2.5.2",
	"Clash.Meta",
	"v2rayNG/1.8.5",
}

const maxSubscriptionResponseBytes = 10 * 1024 * 1024

func (adm *AdminHandler) adminListSubscriptions(w http.ResponseWriter, _ *http.Request) {
	var (
		conf config.SubscriptionConfig
		err  error
	)
	if adm.subscriptionService != nil {
		conf, err = adm.subscriptionService.List(context.Background())
	} else {
		err = config.LoadSubscriptions()
		conf = config.GetSubscriptionConfig()
	}
	if err != nil {
		log.Printf("[Admin] [ListSubscriptions] 无法加载订阅配置: %v", err)
		writeJSON(w, http.StatusInternalServerError, adminErr("加载订阅失败: "+err.Error()))
		return
	}
	updatingIDs := []string{}
	if adm.subscriptionService != nil {
		updatingIDs = adm.subscriptionService.RunningIDs()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"subscriptions": conf.Subscriptions,
		"custom_uas":    conf.CustomUAs,
		"updating_ids":  updatingIDs,
	})
}

func (adm *AdminHandler) adminSaveSubscription(w http.ResponseWriter, r *http.Request) {
	var sub config.Subscription
	if !adm.decodeAdminBody(w, r, &sub) {
		return
	}
	sub.Name = strings.TrimSpace(sub.Name)
	sub.URL = strings.TrimSpace(sub.URL)
	if sub.Name == "" || sub.URL == "" {
		writeJSON(w, http.StatusBadRequest, adminErr("订阅名称和链接不能为空"))
		return
	}
	if err := validateSubscriptionURL(sub.URL); err != nil {
		writeJSON(w, http.StatusBadRequest, adminErr(err.Error()))
		return
	}
	if sub.ID == "" {
		sub.ID = fmt.Sprintf("sub_%d", time.Now().UnixNano())
	}

	var err error
	if adm.subscriptionService != nil {
		err = adm.subscriptionService.SaveSubscription(sub)
	} else {
		err = config.UpdateSubscription(sub)
	}
	if err != nil {
		if errors.Is(err, config.ErrCustomUANotFound) {
			writeJSON(w, http.StatusBadRequest, adminErr("选择的自定义 UA 不存在"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, adminErr("保存订阅失败: "+err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": sub.ID})
}

func (adm *AdminHandler) adminDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string `json:"id"`
		DeleteNodes bool   `json:"delete_nodes"`
	}
	if !adm.decodeAdminBody(w, r, &req) {
		return
	}

	var err error
	if adm.subscriptionService != nil {
		err = adm.subscriptionService.DeleteSubscription(req.ID, req.DeleteNodes)
	} else {
		var deleted config.Subscription
		if _, getErr := config.GetSubscription(req.ID); !getErr {
			err = config.ErrSubscriptionNotFound
		} else if deleted, err = config.DeleteSubscription(req.ID); err == nil {
			if err = nodes.RemoveSubscriptionSource(req.ID, req.DeleteNodes); err != nil {
				if restoreErr := config.UpdateSubscription(deleted); restoreErr != nil {
					err = fmt.Errorf("remove subscription nodes: %v; restore subscription: %w", err, restoreErr)
				}
			}
		}
	}
	if errors.Is(err, config.ErrSubscriptionNotFound) {
		writeJSON(w, http.StatusNotFound, adminErr("订阅不存在"))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("删除订阅失败: "+err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminSaveCustomUA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		UserAgent    string `json:"user_agent"`
		OriginalName string `json:"original_name"`
	}
	if !adm.decodeAdminBody(w, r, &req) {
		return
	}
	cua := config.CustomUA{
		ID:        strings.TrimSpace(req.ID),
		Name:      strings.TrimSpace(req.Name),
		UserAgent: strings.TrimSpace(req.UserAgent),
	}
	if cua.Name == "" || cua.UserAgent == "" {
		writeJSON(w, http.StatusBadRequest, adminErr("UA名称和内容不能为空"))
		return
	}
	if cua.ID == "" && strings.TrimSpace(req.OriginalName) != "" {
		var existing config.CustomUA
		var ok bool
		if adm.subscriptionService != nil {
			existing, ok, _ = adm.subscriptionService.FindCustomUAByName(r.Context(), req.OriginalName)
		} else {
			existing, ok = config.FindCustomUAByName(req.OriginalName)
		}
		if ok {
			cua.ID = existing.ID
		}
	}
	var saved config.CustomUA
	var err error
	if adm.subscriptionService != nil {
		saved, err = adm.subscriptionService.SaveCustomUA(cua)
	} else {
		saved, err = config.SaveCustomUA(cua)
	}
	if errors.Is(err, config.ErrCustomUANameConflict) {
		writeJSON(w, http.StatusConflict, adminErr("自定义 UA 名称不能重复"))
		return
	}
	if errors.Is(err, config.ErrCustomUANotFound) {
		writeJSON(w, http.StatusNotFound, adminErr("要编辑的自定义 UA 不存在"))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("保存自定义UA失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": saved.ID})
}

func (adm *AdminHandler) adminDeleteCustomUA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if !adm.decodeAdminBody(w, r, &req) {
		return
	}

	id := strings.TrimSpace(req.ID)
	if id == "" {
		var existing config.CustomUA
		var ok bool
		if adm.subscriptionService != nil {
			existing, ok, _ = adm.subscriptionService.FindCustomUAByName(r.Context(), req.Name)
		} else {
			existing, ok = config.FindCustomUAByName(req.Name)
		}
		if ok {
			id = existing.ID
		}
	}
	var err error
	if adm.subscriptionService != nil {
		err = adm.subscriptionService.DeleteCustomUA(id)
	} else {
		err = config.DeleteCustomUA(id)
	}
	if errors.Is(err, config.ErrCustomUAInUse) {
		writeJSON(w, http.StatusBadRequest, adminErr("无法删除：仍有订阅正在使用此自定义 UA"))
		return
	}
	if errors.Is(err, config.ErrCustomUANotFound) {
		writeJSON(w, http.StatusNotFound, adminErr("自定义 UA 不存在"))
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("删除自定义UA失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminUpdateSubscriptions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !adm.decodeAdminBody(w, r, &req) {
		return
	}

	if adm.subscriptionService == nil {
		writeJSON(w, http.StatusServiceUnavailable, adminErr("订阅更新服务不可用"))
		return
	}
	if req.ID == "" {
		conf, err := adm.subscriptionService.List(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, adminErr("加载订阅失败: "+err.Error()))
			return
		}
		targetIDs := make([]string, 0, len(conf.Subscriptions))
		for _, sub := range conf.Subscriptions {
			targetIDs = append(targetIDs, sub.ID)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"triggered":    adm.subscriptionService.TriggerAll(),
			"target_ids":   targetIDs,
			"updating_ids": adm.subscriptionService.RunningIDs(),
		})
		return
	}
	if _, ok, err := adm.subscriptionService.Get(r.Context(), req.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("加载订阅失败: "+err.Error()))
		return
	} else if !ok {
		writeJSON(w, http.StatusNotFound, adminErr("订阅不存在"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"triggered":    adm.subscriptionService.Trigger(req.ID),
		"target_ids":   []string{req.ID},
		"updating_ids": adm.subscriptionService.RunningIDs(),
	})
}

func (adm *AdminHandler) fetchSubWithFallback(ctx context.Context, rawURL, primaryUA string) (string, error) {
	if err := validateSubscriptionURLResolved(ctx, rawURL); err != nil {
		return "", err
	}
	uasToTry := []string{primaryUA}
	if primaryUA == "" || primaryUA == "Chrome" {
		uasToTry[0] = subscriptionFetchUserAgent
	}
	uasToTry = append(uasToTry, fallbackUAs...)

	var lastErr error
	seen := make(map[string]struct{}, len(uasToTry))
	for _, ua := range uasToTry {
		if ua == "" {
			continue
		}
		if _, duplicate := seen[ua]; duplicate {
			continue
		}
		seen[ua] = struct{}{}
		data, err := adm.fetchSubDataWithUA(ctx, rawURL, ua)
		if err == nil {
			text := strings.TrimSpace(string(data))
			if text != "" {
				parsed := parseImportedNodes(text)
				if len(parsed) > 0 {
					return text, nil
				}
				lastErr = fmt.Errorf("使用 UA %s 拉取成功但未解析到任何节点", ua)
			} else {
				lastErr = fmt.Errorf("使用 UA %s 响应内容为空", ua)
			}
		} else {
			lastErr = fmt.Errorf("使用 UA %s 拉取失败: %v", ua, err)
		}
		log.Printf("[Admin] [FetchFallback] %v, 尝试下一个...", lastErr)
	}

	return "", fmt.Errorf("所有 UA 均拉取失败，最后错误: %v", lastErr)
}

func (adm *AdminHandler) fetchSubDataWithUA(ctx context.Context, rawURL, ua string) ([]byte, error) {
	if err := validateSubscriptionURLResolved(ctx, rawURL); err != nil {
		return nil, err
	}
	proxyURI, direct, err := adm.planSubscriptionRoute(ctx, adm.cfg)
	if err != nil {
		return nil, err
	}
	if !direct {
		if adm.vc == nil || adm.vc.Net() == nil {
			return nil, fmt.Errorf("network client unavailable for global proxy subscription route")
		}
		data, fetchErr := adm.fetchSubscriptionDataViaProxyWithUA(ctx, adm.vc.Net(), rawURL, proxyURI, ua)
		if fetchErr == nil {
			_ = adm.markGlobalProxySuccess(proxyURI)
		}
		return data, fetchErr
	}

	if err := validateSubscriptionURLResolved(ctx, rawURL); err != nil {
		return nil, err
	}
	client := newSubscriptionHTTPClient(30 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read subscription response: %w", err)
	}
	if len(data) > maxSubscriptionResponseBytes {
		return nil, fmt.Errorf("subscription response exceeds %d bytes", maxSubscriptionResponseBytes)
	}
	return data, nil
}

func (adm *AdminHandler) fetchSubscriptionDataViaProxyWithUA(ctx context.Context, net interface{}, rawURL, proxyURI, ua string) ([]byte, error) {
	netClient, ok := net.(*transport.NetworkClient)
	if !ok || netClient == nil {
		return nil, fmt.Errorf("network client unavailable")
	}

	sess, err := netClient.CreateSessionWithoutRedirects(30, proxyURI, "admin-fetch-sub")
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	defer sess.Close()

	header := transport.Header{
		"user-agent": {ua},
		"accept":     {"*/*"},
	}
	statusCode, data, err := sess.DoAndReadLimit(ctx, http.MethodGet, rawURL, header, nil, maxSubscriptionResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d", statusCode)
	}
	return data, nil
}
