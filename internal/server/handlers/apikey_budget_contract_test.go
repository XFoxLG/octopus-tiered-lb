package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
)

// apiKeyBudgetEnvelope 对应 resp.ResponseStruct 的 JSON 形状。
type apiKeyBudgetEnvelope struct {
	Code int          `json:"code"`
	Data model.APIKey `json:"data"`
}

// callUpdateAPIKey 直接驱动 handler（不经路由/鉴权中间件），并返回状态码与响应体。
func callUpdateAPIKey(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/apikey/update", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user_role", model.UserRoleEditor)
	updateAPIKey(c)
	return recorder
}

func decodeAPIKeyBudgetEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) apiKeyBudgetEnvelope {
	t.Helper()
	var envelope apiKeyBudgetEnvelope
	if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
	}
	return envelope
}

func createAPIKeyForBudgetTest(t *testing.T, ctx context.Context, name string, budget int) model.APIKey {
	t.Helper()
	key := &model.APIKey{
		Name:                  name,
		APIKey:                "sk-octopus-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + name,
		Enabled:               true,
		RequestTimeoutSeconds: budget,
	}
	if err := op.APIKeyCreate(key, ctx); err != nil {
		t.Fatalf("create api key: %v", err)
	}
	return *key
}

// TestUpdateAPIKeyRequestTimeoutPresenceContract 锁定 request_timeout_seconds 的三态契约：
// 字段缺失保留旧预算、显式 0 清空预算、1..86400 覆盖为新预算、越界值 400 且不改动旧值。
// 回归背景：op.Update 走 Save + 普通 int 载荷时，缺失字段的零值会把已配置预算静默清空。
func TestUpdateAPIKeyRequestTimeoutPresenceContract(t *testing.T) {
	ctx := setupAPIKeyHandlerTest(t)

	cases := []struct {
		name       string
		field      string
		wantStatus int
		wantBudget int
	}{
		{name: "missing field keeps existing budget", field: "", wantStatus: http.StatusOK, wantBudget: 300},
		{name: "explicit zero clears budget", field: `,"request_timeout_seconds":0`, wantStatus: http.StatusOK, wantBudget: 0},
		{name: "valid budget sets value", field: `,"request_timeout_seconds":45`, wantStatus: http.StatusOK, wantBudget: 45},
		{name: "upper bound accepted", field: `,"request_timeout_seconds":86400`, wantStatus: http.StatusOK, wantBudget: 86400},
		{name: "negative rejected", field: `,"request_timeout_seconds":-1`, wantStatus: http.StatusBadRequest, wantBudget: 300},
		{name: "above upper bound rejected", field: `,"request_timeout_seconds":86401`, wantStatus: http.StatusBadRequest, wantBudget: 300},
		{name: "non integer rejected", field: `,"request_timeout_seconds":"45"`, wantStatus: http.StatusBadRequest, wantBudget: 300},
		{name: "null rejected", field: `,"request_timeout_seconds":null`, wantStatus: http.StatusBadRequest, wantBudget: 300},
		{name: "fraction rejected", field: `,"request_timeout_seconds":1.5`, wantStatus: http.StatusBadRequest, wantBudget: 300},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := createAPIKeyForBudgetTest(t, ctx, "update-presence", 300)

			recorder := callUpdateAPIKey(t, fmt.Sprintf(
				`{"id":%d,"name":"update-presence","enabled":true%s}`,
				key.ID, tc.field,
			))
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}

			persisted, err := op.APIKeyGet(key.ID, ctx)
			if err != nil {
				t.Fatalf("read back api key: %v", err)
			}
			if persisted.RequestTimeoutSeconds != tc.wantBudget {
				t.Fatalf("persisted budget = %d, want %d (body=%s)",
					persisted.RequestTimeoutSeconds, tc.wantBudget, recorder.Body.String())
			}

			if tc.wantStatus == http.StatusOK {
				envelope := decodeAPIKeyBudgetEnvelope(t, recorder)
				if envelope.Code != http.StatusOK || envelope.Data.RequestTimeoutSeconds != tc.wantBudget {
					t.Fatalf("response budget = %d, want %d (code=%d, body=%s)",
						envelope.Data.RequestTimeoutSeconds, tc.wantBudget, envelope.Code, recorder.Body.String())
				}
			}
		})
	}
}

// TestCreateAPIKeyRequestTimeoutContract 锁定 Create 侧兼容语义：
// 缺失字段默认 0、显式 0 允许、越界 400。
func TestCreateAPIKeyRequestTimeoutContract(t *testing.T) {
	ctx := setupAPIKeyHandlerTest(t)

	cases := []struct {
		name       string
		field      string
		wantStatus int
		wantBudget int
	}{
		{name: "missing field defaults to zero", field: "", wantStatus: http.StatusOK, wantBudget: 0},
		{name: "explicit zero accepted", field: `,"request_timeout_seconds":0`, wantStatus: http.StatusOK, wantBudget: 0},
		{name: "valid budget accepted", field: `,"request_timeout_seconds":1800`, wantStatus: http.StatusOK, wantBudget: 1800},
		{name: "above upper bound rejected", field: `,"request_timeout_seconds":86401`, wantStatus: http.StatusBadRequest, wantBudget: 0},
		{name: "negative rejected", field: `,"request_timeout_seconds":-5`, wantStatus: http.StatusBadRequest, wantBudget: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/apikey/create", strings.NewReader(fmt.Sprintf(
				`{"name":"create-budget-%s","enabled":true%s}`, strings.ReplaceAll(tc.name, " ", "-"), tc.field,
			)))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("user_role", model.UserRoleEditor)

			createAPIKey(c)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}

			envelope := decodeAPIKeyBudgetEnvelope(t, recorder)
			if envelope.Data.RequestTimeoutSeconds != tc.wantBudget {
				t.Fatalf("response budget = %d, want %d (body=%s)",
					envelope.Data.RequestTimeoutSeconds, tc.wantBudget, recorder.Body.String())
			}
			persisted, err := op.APIKeyGet(envelope.Data.ID, ctx)
			if err != nil {
				t.Fatalf("read back api key: %v", err)
			}
			if persisted.RequestTimeoutSeconds != tc.wantBudget {
				t.Fatalf("persisted budget = %d, want %d", persisted.RequestTimeoutSeconds, tc.wantBudget)
			}
		})
	}
}
