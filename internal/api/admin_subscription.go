package api

import (
	"context"
	"fmt"
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

	if err := config.UpdateSubscription(sub); err != nil {
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

	conf := config.GetSubscriptionConfig()
	var newSubs []config.Subscription
	for _, s := range conf.Subscriptions {
		if s.ID != req.ID {
			newSubs = append(newSubs, s)
		}
	}
	conf.Subscriptions = newSubs
	if err := config.SaveSubscriptions(conf); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("删除订阅失败: "+err.Error()))
		return
	}

	// 清理节点 (可选)
	if req.DeleteNodes {
		nodes.DeleteNodesBySource(req.ID)
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminSaveCustomUA(w http.ResponseWriter, r *http.Request) {
	var cua config.CustomUA
	if !adm.decodeAdminBody(w, r, &cua) {
		return
	}
	cua.Name = strings.TrimSpace(cua.Name)
	cua.UserAgent = strings.TrimSpace(cua.UserAgent)
	if cua.Name == "" || cua.UserAgent == "" {
		writeJSON(w, http.StatusBadRequest, adminErr("UA名称和内容不能为空"))
		return
	}

	conf := config.GetSubscriptionConfig()
	found := false
	for i, u := range conf.CustomUAs {
		if u.Name == cua.Name {
			conf.CustomUAs[i] = cua
			found = true
			break
		}
	}
	if !found {
		conf.CustomUAs = append(conf.CustomUAs, cua)
	}
	
	if err := config.SaveSubscriptions(conf); err != nil {
		writeJSON(w, http.StatusInternalServerError, adminErr("保存自定义UA失败: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) adminDeleteCustomUA(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !adm.decodeAdminBody(w, r, &req) {
		return
	}

	conf := config.GetSubscriptionConfig()
	
	// 查找即将删除的 UA 的内容
	var targetUA string
	for _, u := range conf.CustomUAs {
		if u.Name == req.Name {
			targetUA = u.UserAgent
			break
		}
	}
	if targetUA == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	// 检查是否有订阅正在使用该 UA
	for _, sub := range conf.Subscriptions {
		if sub.UserAgent == targetUA {
			writeJSON(w, http.StatusBadRequest, adminErr(fmt.Sprintf("无法删除：订阅 '%s' 正在使用此自定义 UA", sub.Name)))
			return
		}
	}

	var newUAs []config.CustomUA
	for _, u := range conf.CustomUAs {
		if u.Name != req.Name {
			newUAs = append(newUAs, u)
		}
	}
	conf.CustomUAs = newUAs
	if err := config.SaveSubscriptions(conf); err != nil {
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

	conf := config.GetSubscriptionConfig()
	
	go func() {
		for i, sub := range conf.Subscriptions {
			if req.ID != "" && sub.ID != req.ID {
				continue
			}
			
			log.Printf("[Admin] [UpdateSubscription] 开始拉取订阅: %s (%s)", sub.Name, sub.URL)
			
			text, err := adm.fetchSubWithFallback(context.Background(), sub.URL, sub.UserAgent)
			
			if err != nil {
				log.Printf("[Admin] [UpdateSubscription] 订阅 %s 拉取失败: %v", sub.Name, err)
				sub.LastError = err.Error()
			} else {
				parsedNodes := parseImportedNodes(text)
				if len(parsedNodes) == 0 {
					sub.LastError = "未解析到有效节点"
				} else {
					for idx := range parsedNodes {
						parsedNodes[idx].Source = sub.ID
					}
					nodes.DeleteNodesBySource(sub.ID)
					nodes.MergeNodes(parsedNodes)
					sub.LastError = ""
					sub.LastUpdateTime = time.Now().Unix()
					log.Printf("[Admin] [UpdateSubscription] 订阅 %s 更新成功，解析出 %d 个节点", sub.Name, len(parsedNodes))
				}
			}
			conf.Subscriptions[i] = sub
		}
		_ = config.SaveSubscriptions(conf)
		// We might need to ensure nodes are saved if MergeNodes doesn't save them. I'll verify store.go later.
	}()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (adm *AdminHandler) fetchSubWithFallback(ctx context.Context, rawURL, primaryUA string) (string, error) {
	uasToTry := []string{primaryUA}
	if primaryUA == "" || primaryUA == "Chrome" {
		uasToTry[0] = subscriptionFetchUserAgent
	}
	uasToTry = append(uasToTry, fallbackUAs...)

	var lastErr error
	for _, ua := range uasToTry {
		if ua == "" {
			continue
		}
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

	data := make([]byte, 0, 1024*1024) // 1MB buffer
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if err != nil {
			break
		}
		if len(data) > 10*1024*1024 { // max 10MB
			break
		}
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

var (
	subUpdaterTicker *time.Ticker
	subUpdaterQuit   chan struct{}
)

func (adm *AdminHandler) StartSubscriptionUpdater() {
	if subUpdaterTicker != nil {
		return
	}
	subUpdaterTicker = time.NewTicker(1 * time.Minute)
	subUpdaterQuit = make(chan struct{})

	go func() {
		for {
			select {
			case <-subUpdaterTicker.C:
				adm.checkAndUpdateSubscriptions()
			case <-subUpdaterQuit:
				return
			}
		}
	}()
}

func (adm *AdminHandler) StopSubscriptionUpdater() {
	if subUpdaterTicker != nil {
		subUpdaterTicker.Stop()
		close(subUpdaterQuit)
		subUpdaterTicker = nil
	}
}

func (adm *AdminHandler) checkAndUpdateSubscriptions() {
	conf := config.GetSubscriptionConfig()
	if len(conf.Subscriptions) == 0 {
		return
	}

	now := time.Now().Unix()
	var toUpdate []string // store IDs to update

	for _, sub := range conf.Subscriptions {
		if sub.UpdateInterval <= 0 {
			continue // Auto-update disabled
		}
		intervalSec := int64(sub.UpdateInterval * 60)
		if now >= sub.LastUpdateTime+intervalSec {
			toUpdate = append(toUpdate, sub.ID)
		}
	}

	if len(toUpdate) > 0 {
		// Create a mock request payload to call adminUpdateSubscriptions logic
		go func(ids []string) {
			for _, id := range ids {
				adm.doUpdateSubscription(id)
			}
		}(toUpdate)
	}
}

func (adm *AdminHandler) doUpdateSubscription(id string) {
	conf := config.GetSubscriptionConfig()
	for i, sub := range conf.Subscriptions {
		if sub.ID != id {
			continue
		}
		log.Printf("[Admin] [UpdateSubscription] 开始后台拉取订阅: %s (%s)", sub.Name, sub.URL)
		
		text, err := adm.fetchSubWithFallback(context.Background(), sub.URL, sub.UserAgent)
		
		if err != nil {
			log.Printf("[Admin] [UpdateSubscription] 订阅 %s 拉取失败: %v", sub.Name, err)
			sub.LastError = err.Error()
		} else {
			parsedNodes := parseImportedNodes(text)
			if len(parsedNodes) == 0 {
				sub.LastError = "未解析到有效节点"
			} else {
				for idx := range parsedNodes {
					parsedNodes[idx].Source = sub.ID
				}
				nodes.DeleteNodesBySource(sub.ID)
				nodes.MergeNodes(parsedNodes)
				sub.LastError = ""
				sub.LastUpdateTime = time.Now().Unix()
				log.Printf("[Admin] [UpdateSubscription] 订阅 %s 更新成功，解析出 %d 个节点", sub.Name, len(parsedNodes))
			}
		}
		conf.Subscriptions[i] = sub
		_ = config.SaveSubscriptions(conf)
		break
	}
}
