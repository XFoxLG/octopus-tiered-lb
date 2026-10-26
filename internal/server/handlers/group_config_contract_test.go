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
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
	grp "github.com/lingyuins/octopus/internal/op/group"
)

// TestGetGroupListReturnsFullConfigContract 红测：/api/v1/group/list 必须返回
// model.Group 的全部可编辑配置字段（包括零值和 false），否则前端"保存后回显
// 丢失"（用户报告的 CFG-01）。此测试锁定以下字段必须存在于响应 JSON 中：
// category / condition / reasoning_buffer_strategy / param_override /
// default_reasoning_effort / reasoning_force_override / custom_header /
// relay_retry_count / relay_route_retries / last_test_all_failed。
//
// 期望行为：即使字段值为零值 / false / 空串，也应作为 JSON 显式字段出现
//（不带 omitempty）。若当前实现缺失任一字段，该测试失败。
func TestGetGroupListReturnsFullConfigContract(t *testing.T) {
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

	forceOverride := false
	group := &model.Group{
		Name:                    "config-contract",
		Category:                "",
		EndpointType:            model.EndpointTypeChat,
		Mode:                    model.GroupModeRoundRobin,
		Condition:               "",
		ReasoningBufferStrategy: "",
		ParamOverride:           nil,
		DefaultReasoningEffort:  "low",
		ReasoningForceOverride:  forceOverride,
		RelayRetryCount:         -1,
		RelayRouteRetries:       -1,
		CustomHeader:            nil,
	}
	if err := grp.GroupCreate(group, ctx); err != nil {
		t.Fatalf("create group: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/group/list", nil)

	getGroupList(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 1 {
		t.Fatalf("unexpected response payload: %d groups", len(response.Data))
	}

	// 每个字段都必须是显式 JSON key（哪怕值是零值/false/空串/null）。
	// 这保证前端保存"false"后重新打开表单时能看到 false，而不是把它当
	// 成"字段缺失"而回落到默认 true 或跳过更新。
	requiredFields := []string{
		"category",
		"condition",
		"reasoning_buffer_strategy",
		"param_override",
		"default_reasoning_effort",
		"reasoning_force_override",
		"custom_header",
		"relay_retry_count",
		"relay_route_retries",
		"last_test_all_failed",
		"stream_idle_timeout", "request_timeout_seconds", "stream_keepalive_enabled", "assistant_prefill_compat", "forward_mode",
	}
	first := response.Data[0]
	for _, field := range requiredFields {
		if _, ok := first[field]; !ok {
			t.Errorf("response missing required field %q (CFG-01 regression)", field)
		}
	}

	// 值语义校验：DefaultReasoningEffort="low" 应原样返回。
	var effort string
	if err := json.Unmarshal(first["default_reasoning_effort"], &effort); err != nil {
		t.Fatalf("decode default_reasoning_effort: %v", err)
	}
	if effort != "low" {
		t.Errorf("default_reasoning_effort = %q, want %q", effort, "low")
	}

	// false 必须显式出现（不是被省略）。
	var forceOverrideVal bool
	if err := json.Unmarshal(first["reasoning_force_override"], &forceOverrideVal); err != nil {
		t.Fatalf("decode reasoning_force_override: %v", err)
	}
	if forceOverrideVal != false {
		t.Errorf("reasoning_force_override = %v, want false", forceOverrideVal)
	}
}
