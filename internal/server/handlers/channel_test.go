package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
)

func TestClassifyChannelMutationError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
		wantOK     bool
	}{
		{
			name:       "channel not found",
			err:        errors.New("channel not found"),
			wantStatus: http.StatusNotFound,
			wantMsg:    "channel not found",
			wantOK:     true,
		},
		{
			name:       "invalid request rewrite profile",
			err:        errors.New("unsupported request rewrite profile: broken"),
			wantStatus: http.StatusBadRequest,
			wantMsg:    "unsupported request rewrite profile: broken",
			wantOK:     true,
		},
		{
			name:       "unsupported request rewrite channel type",
			err:        errors.New("request rewrite profile openai_chat_compat is not supported for channel type 1"),
			wantStatus: http.StatusBadRequest,
			wantMsg:    "request rewrite profile openai_chat_compat is not supported for channel type 1",
			wantOK:     true,
		},
		{
			name:       "duplicate channel name",
			err:        errors.New("UNIQUE constraint failed: channels.name"),
			wantStatus: http.StatusConflict,
			wantMsg:    "channel name already exists",
			wantOK:     true,
		},
		{
			name:       "channel group not found",
			err:        errors.New("channel group not found"),
			wantStatus: http.StatusNotFound,
			wantMsg:    "channel group not found",
			wantOK:     true,
		},
		{
			name:       "default channel group cannot be deleted",
			err:        errors.New("default channel group cannot be deleted"),
			wantStatus: http.StatusBadRequest,
			wantMsg:    "default channel group cannot be deleted",
			wantOK:     true,
		},
		{
			name:       "duplicate channel group name",
			err:        errors.New("UNIQUE constraint failed: channel_groups.name"),
			wantStatus: http.StatusConflict,
			wantMsg:    "channel group name already exists",
			wantOK:     true,
		},
		{
			name:       "legacy schema missing request_rewrite",
			err:        errors.New("SQL logic error: table channels has no column named request_rewrite (1)"),
			wantStatus: http.StatusServiceUnavailable,
			wantMsg:    "database schema is outdated",
			wantOK:     true,
		},
		{
			name:    "unexpected error",
			err:     errors.New("database is locked"),
			wantOK:  false,
			wantMsg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, msg, ok := classifyChannelMutationError(tt.err)
			if ok != tt.wantOK {
				t.Fatalf("expected ok=%t, got %t", tt.wantOK, ok)
			}
			if status != tt.wantStatus {
				t.Fatalf("expected status=%d, got %d", tt.wantStatus, status)
			}
			if tt.wantMsg == "" {
				if msg != "" {
					t.Fatalf("expected empty message, got %q", msg)
				}
				return
			}
			if !strings.Contains(msg, tt.wantMsg) {
				t.Fatalf("expected message containing %q, got %q", tt.wantMsg, msg)
			}
		})
	}
}

func TestListChannelMasksKeysForViewer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx := context.Background()
	testName := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(t.Name())
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", testName)

	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	channel := &model.Channel{
		Name:      "viewer-check",
		Type:      0,
		Enabled:   true,
		BaseUrls:  []model.BaseUrl{{URL: "https://example.com", Delay: 0}},
		Model:     "gpt-4o",
		AutoGroup: model.AutoGroupTypeNone,
		Keys: []model.ChannelKey{
			{Enabled: true, ChannelKey: "sk-secret-12345678", Remark: "primary"},
		},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel/list", nil)
	c.Set("user_role", model.UserRoleViewer)

	listChannel(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response struct {
		Code int             `json:"code"`
		Data []model.Channel `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 1 || len(response.Data[0].Keys) != 1 {
		t.Fatalf("unexpected response payload: %+v", response.Data)
	}

	got := response.Data[0].Keys[0].ChannelKey
	if got == "sk-secret-12345678" {
		t.Fatalf("expected masked key, got raw value %q", got)
	}
	if !strings.HasPrefix(got, "sk-s") || !strings.HasSuffix(got, "5678") {
		t.Fatalf("expected masked key to retain edges, got %q", got)
	}
}

func TestListChannelKeepsRawKeysForEditor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx := context.Background()
	testName := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(t.Name())
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", testName)

	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	channel := &model.Channel{
		Name:      "editor-check",
		Type:      0,
		Enabled:   true,
		BaseUrls:  []model.BaseUrl{{URL: "https://example.com", Delay: 0}},
		Model:     "gpt-4o",
		AutoGroup: model.AutoGroupTypeNone,
		Keys: []model.ChannelKey{
			{Enabled: true, ChannelKey: "sk-secret-abcdef12", Remark: "primary"},
		},
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel/list", nil)
	c.Set("user_role", model.UserRoleEditor)

	listChannel(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response struct {
		Code int             `json:"code"`
		Data []model.Channel `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 1 || len(response.Data[0].Keys) != 1 {
		t.Fatalf("unexpected response payload: %+v", response.Data)
	}
	if got := response.Data[0].Keys[0].ChannelKey; got != "sk-secret-abcdef12" {
		t.Fatalf("expected raw key for editor, got %q", got)
	}
}

func TestListChannelEncodesEmptySlicesAsArrays(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx := context.Background()
	testName := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(t.Name())
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", testName)

	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	channel := &model.Channel{
		Name:      "empty-slices",
		Type:      0,
		Enabled:   true,
		Model:     "gpt-4o",
		AutoGroup: model.AutoGroupTypeNone,
	}
	if err := op.ChannelCreate(channel, ctx); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/channel/list", nil)
	c.Set("user_role", model.UserRoleEditor)

	listChannel(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response struct {
		Data []struct {
			BaseUrls     json.RawMessage `json:"base_urls"`
			Keys         json.RawMessage `json:"keys"`
			CustomHeader json.RawMessage `json:"custom_header"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 1 {
		t.Fatalf("unexpected response payload: %+v", response.Data)
	}
	if string(response.Data[0].BaseUrls) != "[]" {
		t.Fatalf("base_urls = %s, want []", response.Data[0].BaseUrls)
	}
	if string(response.Data[0].Keys) != "[]" {
		t.Fatalf("keys = %s, want []", response.Data[0].Keys)
	}
	if string(response.Data[0].CustomHeader) != "[]" {
		t.Fatalf("custom_header = %s, want []", response.Data[0].CustomHeader)
	}
}

func TestChannelPayloadToModelDropsReadonlyAndRuntimeFields(t *testing.T) {
	payload := channelRequestPayload{
		ID:      99,
		Name:    "sanitized",
		Type:    0,
		Enabled: true,
		BaseUrls: []model.BaseUrl{
			{URL: "https://example.com", Delay: 123, SuffixMode: "custom"},
		},
		Keys: []channelKeyRequestPayload{
			{
				ID:               88,
				ChannelID:        77,
				Enabled:          true,
				ChannelKey:       "sk-secret",
				StatusCode:       429,
				LastUseTimeStamp: 123456,
				TotalCost:        99.9,
				Remark:           "primary",
			},
		},
		Stats: &model.StatsChannel{ChannelID: 99, StatsMetrics: model.StatsMetrics{RequestSuccess: 10}},
	}

	channel := payload.toChannel()

	if channel.ID != 0 {
		t.Fatalf("channel.ID = %d, want 0", channel.ID)
	}
	if channel.Stats != nil {
		t.Fatalf("channel.Stats should be nil")
	}
	if len(channel.Keys) != 1 {
		t.Fatalf("keys len = %d, want 1", len(channel.Keys))
	}
	key := channel.Keys[0]
	if key.ID != 0 || key.ChannelID != 0 || key.StatusCode != 0 || key.LastUseTimeStamp != 0 || key.TotalCost != 0 {
		t.Fatalf("runtime key fields were not sanitized: %+v", key)
	}
	if key.ChannelKey != "sk-secret" || key.Remark != "primary" || !key.Enabled {
		t.Fatalf("writable key fields not preserved: %+v", key)
	}
}

// TestChannelPayloadToModelWithKeyIDsPreservesKeyID 锁住诊断端点的关键行为：
// toChannelWithKeyIDs() 必须保留每个 key 的 ID，且与 payload.Keys 按索引一一对应。
// POST /channel/fetch-models-per-key 靠它把逐 key 抓取结果回映射到具体 key
// （helper.KeyModelResult.KeyID）；丢了 ID，前端拿到的 key_id 恒为 0。
//
// 同时确认它**不**放开其它只读/运行时字段的清洗（与 toChannel 一致），
// 只额外透传 ID 这一项。
func TestChannelPayloadToModelWithKeyIDsPreservesKeyID(t *testing.T) {
	payload := channelRequestPayload{
		ID:      99,
		Name:    "diagnostic",
		Enabled: true,
		Keys: []channelKeyRequestPayload{
			{ID: 11, Enabled: true, ChannelKey: "sk-a", Remark: "first", StatusCode: 429, TotalCost: 5},
			{ID: 22, Enabled: true, ChannelKey: "sk-b", Remark: "second"},
			{ID: 0, Enabled: true, ChannelKey: "sk-unsaved", Remark: "unsaved"}, // 未保存的新 key，ID=0
		},
		Stats: &model.StatsChannel{ChannelID: 99},
	}

	channel := payload.toChannelWithKeyIDs()

	if channel.ID != 0 {
		t.Fatalf("channel.ID = %d, want 0 (payload channel id must still be dropped)", channel.ID)
	}
	if channel.Stats != nil {
		t.Fatal("channel.Stats should still be sanitized to nil")
	}
	if len(channel.Keys) != 3 {
		t.Fatalf("keys len = %d, want 3", len(channel.Keys))
	}

	wantIDs := []int{11, 22, 0}
	wantRemarks := []string{"first", "second", "unsaved"}
	for i, key := range channel.Keys {
		if key.ID != wantIDs[i] {
			t.Errorf("keys[%d].ID = %d, want %d", i, key.ID, wantIDs[i])
		}
		if key.Remark != wantRemarks[i] {
			t.Errorf("keys[%d].Remark = %q, want %q (ID must line up with the right key)", i, key.Remark, wantRemarks[i])
		}
		// 其它运行时字段仍须清洗：StatusCode / TotalCost / ChannelID 不来自请求体。
		if key.StatusCode != 0 || key.TotalCost != 0 || key.ChannelID != 0 {
			t.Errorf("keys[%d] runtime fields not sanitized: %+v", i, key)
		}
	}

	// 可写字段照常保留。
	if channel.Keys[0].ChannelKey != "sk-a" || !channel.Keys[0].Enabled {
		t.Errorf("writable key fields not preserved: %+v", channel.Keys[0])
	}
}

// TestChannelPayloadToModelStillDropsKeyID 回归守卫：普通的 toChannel()（创建/
// 更新路径用）必须继续丢弃请求体里的 key.ID，不能被诊断端点的变体带偏。
func TestChannelPayloadToModelStillDropsKeyID(t *testing.T) {
	payload := channelRequestPayload{
		Name: "create",
		Keys: []channelKeyRequestPayload{
			{ID: 12345, Enabled: true, ChannelKey: "sk-a", Remark: "first"},
		},
	}

	channel := payload.toChannel()

	if len(channel.Keys) != 1 {
		t.Fatalf("keys len = %d, want 1", len(channel.Keys))
	}
	if channel.Keys[0].ID != 0 {
		t.Fatalf("toChannel() keys[0].ID = %d, want 0 (create path must not trust client-supplied primary keys)", channel.Keys[0].ID)
	}
}
