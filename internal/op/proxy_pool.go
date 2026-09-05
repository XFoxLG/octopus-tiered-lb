package op

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/utils/cache"
	"github.com/lingyuins/octopus/internal/utils/proxyx"
	"github.com/lingyuins/octopus/internal/utils/xurl"
	"golang.org/x/net/proxy"
)

const defaultProxyTestURL = "https://api.openai.com/v1/models"

var proxyConfigurationCache = cache.New[int, model.ProxyConfiguration](16)

func ProxyConfigurationList(ctx context.Context) ([]model.ProxyConfiguration, error) {
	if proxyConfigurationCache.Len() > 0 {
		counts, err := ProxyConfigurationReferenceCounts(ctx)
		if err != nil {
			return nil, err
		}
		var items []model.ProxyConfiguration
		for _, item := range proxyConfigurationCache.GetAll() {
			item.ReferenceCount = counts[item.ID]
			items = append(items, item)
		}
		return items, nil
	}
	var items []model.ProxyConfiguration
	if err := db.GetDB().WithContext(ctx).Order("id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	counts, err := ProxyConfigurationReferenceCounts(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].ReferenceCount = counts[items[i].ID]
		proxyConfigurationCache.Set(items[i].ID, items[i])
	}
	return items, nil
}

func ProxyConfigurationGet(id int, ctx context.Context) (*model.ProxyConfiguration, error) {
	var item model.ProxyConfiguration
	if err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func ProxyConfigurationCreate(item *model.ProxyConfiguration, ctx context.Context) error {
	if item == nil {
		return fmt.Errorf("proxy configuration is nil")
	}
	if err := item.Validate(); err != nil {
		return err
	}
	if err := db.GetDB().WithContext(ctx).Create(item).Error; err != nil {
		return err
	}
	proxyConfigurationCache.Set(item.ID, *item)
	return nil
}

func ProxyConfigurationUpdate(req *model.ProxyConfigurationUpdateRequest, ctx context.Context) (*model.ProxyConfiguration, error) {
	if req == nil {
		return nil, fmt.Errorf("proxy update request is nil")
	}
	var existing model.ProxyConfiguration
	if err := db.GetDB().WithContext(ctx).First(&existing, req.ID).Error; err != nil {
		return nil, fmt.Errorf("proxy configuration not found")
	}
	merged := existing
	var selectFields []string
	updates := model.ProxyConfiguration{ID: req.ID}
	if req.Name != nil {
		merged.Name = *req.Name
		selectFields = append(selectFields, "name")
	}
	if req.URL != nil {
		merged.URL = *req.URL
		selectFields = append(selectFields, "url")
	}
	if req.Enabled != nil {
		merged.Enabled = *req.Enabled
		selectFields = append(selectFields, "enabled")
	}
	if req.Remark != nil {
		merged.Remark = *req.Remark
		selectFields = append(selectFields, "remark")
	}
	if len(selectFields) > 0 {
		if err := merged.Validate(); err != nil {
			return nil, err
		}
	}
	if req.Name != nil {
		updates.Name = merged.Name
	}
	if req.URL != nil {
		updates.URL = merged.URL
	}
	if req.Enabled != nil {
		updates.Enabled = merged.Enabled
	}
	if req.Remark != nil {
		updates.Remark = merged.Remark
	}
	if len(selectFields) > 0 {
		if err := db.GetDB().WithContext(ctx).Model(&model.ProxyConfiguration{}).Where("id = ?", req.ID).Select(selectFields).Updates(&updates).Error; err != nil {
			return nil, fmt.Errorf("failed to update proxy configuration: %w", err)
		}
	}
	item, err := ProxyConfigurationGet(req.ID, ctx)
	if err != nil {
		return nil, err
	}
	proxyConfigurationCache.Set(item.ID, *item)
	return item, nil
}

func ProxyConfigurationDelete(id int, ctx context.Context) error {
	if _, err := ProxyConfigurationGet(id, ctx); err != nil {
		return fmt.Errorf("proxy configuration not found")
	}
	count, err := ProxyConfigurationReferenceCount(id, ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("proxy configuration is still referenced")
	}
	if err := db.GetDB().WithContext(ctx).Delete(&model.ProxyConfiguration{}, id).Error; err != nil {
		return err
	}
	proxyConfigurationCache.Del(id)
	return nil
}

func ProxyConfigurationReferenceCount(id int, ctx context.Context) (int, error) {
	counts, err := ProxyConfigurationReferenceCounts(ctx)
	if err != nil {
		return 0, err
	}
	return counts[id], nil
}

func ProxyConfigurationReferences(id int, ctx context.Context) ([]model.ProxyConfigurationReference, error) {
	if _, err := ProxyConfigurationGet(id, ctx); err != nil {
		return nil, fmt.Errorf("proxy configuration not found")
	}

	refs := make([]model.ProxyConfigurationReference, 0)

	var channels []model.Channel
	if err := db.GetDB().WithContext(ctx).
		Where("proxy_mode = ? AND proxy_config_id = ?", model.ProxyUsageModePool, id).
		Order("id ASC").Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, channel := range channels {
		ref := model.ProxyConfigurationReference{
			Type:        model.ProxyConfigurationReferenceTypeChannel,
			ChannelID:   channel.ID,
			ChannelName: channel.Name,
		}
		refs = append(refs, ref)
	}

	return refs, nil
}

func ProxyConfigurationReferenceCounts(ctx context.Context) (map[int]int, error) {
	counts := make(map[int]int)
	if err := countManualChannelProxyReferences(ctx, counts); err != nil {
		return nil, err
	}
	return counts, nil
}

func countManualChannelProxyReferences(ctx context.Context, counts map[int]int) error {
	type row struct {
		ProxyConfigID int
		Count         int
	}
	var rows []row
	if err := db.GetDB().WithContext(ctx).Table("channels").
		Select("channels.proxy_config_id, count(*) as count").
		Where("channels.proxy_mode = ? AND channels.proxy_config_id IS NOT NULL", model.ProxyUsageModePool).
		Group("channels.proxy_config_id").Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		counts[r.ProxyConfigID] += r.Count
	}
	return nil
}

func ProxyURLForConfig(id int, ctx context.Context) (string, error) {
	if cached, ok := proxyConfigurationCache.Get(id); ok {
		if !cached.Enabled {
			return "", fmt.Errorf("proxy configuration is disabled")
		}
		return cached.URL, nil
	}
	item, err := ProxyConfigurationGet(id, ctx)
	if err != nil {
		proxyConfigurationCache.Del(id)
		return "", fmt.Errorf("proxy configuration not found")
	}
	proxyConfigurationCache.Set(item.ID, *item)
	if !item.Enabled {
		return "", fmt.Errorf("proxy configuration is disabled")
	}
	return item.URL, nil
}

func proxyConfigurationRefreshCache(ctx context.Context) error {
	var items []model.ProxyConfiguration
	if err := db.GetDB().WithContext(ctx).Find(&items).Error; err != nil {
		return err
	}
	// 存量规范化：旧数据中的 socks:// 统一改写为 socks5://（代理池新格式
	// 支持后 socks 仅作别名保留，避免重复 scheme 导致展示/解析歧义）。
	for i := range items {
		normalized, err := model.NormalizeProxyURL(items[i].URL)
		if err == nil && normalized != items[i].URL {
			items[i].URL = normalized
			if err := db.GetDB().WithContext(ctx).Model(&model.ProxyConfiguration{}).
				Where("id = ?", items[i].ID).Update("url", normalized).Error; err != nil {
				return err
			}
		}
	}
	proxyConfigurationCache.Clear()
	for _, item := range items {
		proxyConfigurationCache.Set(item.ID, item)
	}
	return nil
}

func newProxyTestHTTPClient(proxyURLStr string) (*http.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport is not *http.Transport")
	}
	cloned := transport.Clone()
	proxyURL, err := url.Parse(proxyURLStr)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy url: %w", err)
	}
	switch proxyURL.Scheme {
	case "http", "https":
		cloned.Proxy = http.ProxyURL(proxyURL)
	case "socks", "socks5":
		socksDialer, err := proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("invalid socks proxy: %w", err)
		}
		cloned.Proxy = nil
		cloned.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return socksDialer.Dial(network, addr)
		}
	case "ss", "vmess", "vless", "trojan":
		dialContext, err := proxyx.NewDialContext(proxyURLStr)
		if err != nil {
			return nil, err
		}
		cloned.Proxy = nil
		cloned.DialContext = dialContext
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", proxyURL.Scheme)
	}
	return &http.Client{Transport: cloned}, nil
}

func ProxyConfigurationTest(req model.ProxyTestRequest, ctx context.Context) (model.ProxyTestResult, error) {
	targetURL := strings.TrimSpace(req.URL)
	if targetURL == "" {
		targetURL = defaultProxyTestURL
	}
	parsedTarget, err := url.Parse(targetURL)
	if err != nil || parsedTarget.Scheme == "" || parsedTarget.Host == "" || (parsedTarget.Scheme != "http" && parsedTarget.Scheme != "https") {
		return model.ProxyTestResult{Success: false, Message: "test url must be a valid http or https url"}, nil
	}
	if err := xurl.AssertSafeHost(parsedTarget); err != nil {
		return model.ProxyTestResult{Success: false, Message: err.Error()}, nil
	}

	proxyURL := strings.TrimSpace(req.ProxyURL)
	if req.ProxyConfigID != nil && *req.ProxyConfigID > 0 {
		item, getErr := ProxyConfigurationGet(*req.ProxyConfigID, ctx)
		if getErr != nil {
			return model.ProxyTestResult{Success: false, Message: "proxy configuration not found"}, nil
		}
		if !item.Enabled {
			return model.ProxyTestResult{Success: false, Message: "proxy configuration is disabled"}, nil
		}
		proxyURL = item.URL
	}
	if proxyURL == "" {
		return model.ProxyTestResult{Success: false, Message: "proxy url is required"}, nil
	}
	normalizedProxyURL, err := model.NormalizeProxyURL(proxyURL)
	if err != nil {
		return model.ProxyTestResult{Success: false, Message: err.Error()}, nil
	}

	httpClient, err := newProxyTestHTTPClient(normalizedProxyURL)
	if err != nil {
		return model.ProxyTestResult{Success: false, Message: err.Error()}, nil
	}
	httpClient.Timeout = 20 * time.Second

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return model.ProxyTestResult{Success: false, Message: err.Error()}, nil
	}
	httpReq.Header.Set("User-Agent", "Octopus Proxy Pool Tester")
	resp, err := httpClient.Do(httpReq)
	durationMS := time.Since(start).Milliseconds()
	if err != nil {
		return model.ProxyTestResult{Success: false, DurationMS: durationMS, Message: err.Error()}, nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return model.ProxyTestResult{Success: true, StatusCode: resp.StatusCode, DurationMS: durationMS, Message: "proxy is reachable"}, nil
}
