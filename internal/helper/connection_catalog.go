package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lingyuins/octopus/internal/model"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func fetchConnectionModels(client *http.Client, ctx context.Context, channel model.Channel) ([]string, error) {
	cfg := channel.ConnectionConfig
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Catalog.Format == "manual" {
		return nil, fmt.Errorf("model_catalog_manual: models are maintained manually")
	}
	var endpoint *model.ChannelEndpoint
	for i := range cfg.Endpoints {
		if cfg.Endpoints[i].ID == cfg.Catalog.EndpointID {
			endpoint = &cfg.Endpoints[i]
			break
		}
	}
	if endpoint == nil {
		return nil, fmt.Errorf("model catalog endpoint not configured")
	}
	raw := cfg.Catalog.URL
	if raw == "" {
		if endpoint.URLMode == "full" {
			return nil, fmt.Errorf("full generation URL requires an explicit model catalog URL")
		}
		u, err := url.Parse(endpoint.URL)
		if err != nil {
			return nil, err
		}
		root := strings.TrimRight(u.Path, "/")
		if cfg.Catalog.Format == "cloudflare" {
			root = strings.TrimSuffix(root, "/ai/v1")
			u.Path = root + "/ai/models/search"
		} else {
			if root == "" {
				root = "/v1"
				if cfg.Catalog.Format == "gemini" {
					root = "/v1beta"
				}
			}
			u.Path = root + "/models"
		}
		u.RawPath = ""
		raw = u.String()
	}
	key := channel.GetChannelKey().ChannelKey
	result := []string{}
	cursor := ""
	seen := map[string]bool{}
	for page := 1; page <= 100; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return nil, err
		}
		switch cfg.Catalog.Format {
		case "anthropic":
			req.Header.Set("x-api-key", key)
			req.Header.Set("anthropic-version", "2023-06-01")
		case "gemini":
			req.Header.Set("x-goog-api-key", key)
		default:
			req.Header.Set("Authorization", "Bearer "+key)
		}
		channel.ApplyConnectionHeaders(req, model.ConnectionPlan{Endpoint: endpoint, AdapterType: endpoint.AdapterType()}, key)
		q := req.URL.Query()
		if cursor != "" {
			if cfg.Catalog.Format == "gemini" {
				q.Set("pageToken", cursor)
			} else {
				q.Set("after_id", cursor)
			}
		}
		if cfg.Catalog.Format == "cloudflare" {
			q.Set("page", strconv.Itoa(page))
			q.Set("per_page", "100")
		}
		if cursor != "" || cfg.Catalog.Format == "cloudflare" {
			req.URL.RawQuery = q.Encode()
		}
		response, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
		response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(body) > 4<<20 {
			return nil, fmt.Errorf("model catalog response too large")
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("model catalog HTTP %d", response.StatusCode)
		}
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
			Result []struct {
				Name string `json:"name"`
			} `json:"result"`
			Next       string `json:"nextPageToken"`
			HasMore    bool   `json:"has_more"`
			LastID     string `json:"last_id"`
			Success    *bool  `json:"success"`
			ResultInfo struct {
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		// An error envelope / HTML / unrelated object is not an empty model list.
		var shape map[string]json.RawMessage
		_ = json.Unmarshal(body, &shape)
		field := "data"
		if cfg.Catalog.Format == "gemini" {
			field = "models"
		}
		if cfg.Catalog.Format == "cloudflare" {
			field = "result"
		}
		if data, ok := shape[field]; !ok || strings.TrimSpace(string(data)) == "null" {
			return nil, fmt.Errorf("model catalog response missing %s", field)
		}
		if data, ok := shape["error"]; ok && strings.TrimSpace(string(data)) != "null" {
			return nil, fmt.Errorf("model catalog returned an error envelope")
		}
		switch cfg.Catalog.Format {
		case "gemini":
			for _, m := range payload.Models {
				result = append(result, strings.TrimPrefix(m.Name, "models/"))
			}
			cursor = payload.Next
		case "cloudflare":
			if payload.Success != nil && !*payload.Success {
				return nil, fmt.Errorf("Cloudflare model catalog failed")
			}
			for _, m := range payload.Result {
				result = append(result, m.Name)
			}
			if page >= payload.ResultInfo.TotalPages {
				return result, nil
			}
			continue
		default:
			for _, m := range payload.Data {
				result = append(result, m.ID)
			}
			if payload.HasMore {
				cursor = payload.LastID
				if cursor == "" {
					return nil, fmt.Errorf("model catalog pagination missing cursor")
				}
			} else {
				cursor = ""
			}
		}
		if cursor == "" {
			return result, nil
		}
		if seen[cursor] {
			return nil, fmt.Errorf("model catalog repeated cursor")
		}
		seen[cursor] = true
	}
	return nil, fmt.Errorf("model catalog exceeded pagination limit")
}
