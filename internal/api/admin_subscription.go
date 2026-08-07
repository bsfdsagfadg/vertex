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
	"github.com/bsfdsagfadg/vertex/internal/netx"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

var fallbackUAs = []string{
	"clash-verge/v2.5.2",
	"Clash.Meta",
	"v2rayNG/1.8.5",
}

const maxSubscriptionResponseBytes = 10 * 1024 * 1024

func (adm *AdminHandler) adminListSubscriptions(w http.ResponseWriter, _ *http.Request) {
	err := config.LoadSubscriptions()
	if err != nil {
		log.Printf("[Admin] [ListSubscriptions] 无法加载订阅配置: %v", err)
	}
	writeJSON(w, http.StatusOK, config.GetSubscriptionConfig())
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
		if existing, ok := config.FindCustomUAByName(req.OriginalName); ok {
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
		if existing, ok := config.FindCustomUAByName(req.Name); ok {
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
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "triggered": adm.subscriptionService.TriggerAll()})
		return
	}
	if _, ok := config.GetSubscription(req.ID); !ok {
		writeJSON(w, http.StatusNotFound, adminErr("订阅不存在"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "triggered": adm.subscriptionService.Trigger(req.ID)})
}

func (adm *AdminHandler) fetchSubWithFallback(ctx context.Context, rawURL, primaryUA string) (string, error) {
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
	client := netx.NewHTTPClient(30 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		// try via proxy
		proxyURI := subscriptionFallbackProxy(adm.cfg)
		if proxyURI != "" && adm.vc != nil && adm.vc.Net() != nil {
			return adm.fetchSubscriptionDataViaProxyWithUA(ctx, adm.vc.Net(), rawURL, proxyURI, ua)
		}
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

	sess, err := netClient.CreateSession(30, proxyURI, "admin-fetch-sub")
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	defer sess.Close()

	header := transport.Header{
		"user-agent": {ua},
		"accept":     {"*/*"},
	}
	statusCode, data, err := sess.DoAndRead(ctx, http.MethodGet, rawURL, header, nil)
	if err != nil {
		return nil, fmt.Errorf("error: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d", statusCode)
	}
	return data, nil
}
