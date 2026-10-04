package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op"
	"github.com/lingyuins/octopus/internal/op/backup"
	stg "github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/task"
)

// setupSettingTestDB 为单测准备独立 SQLite 与设置缓存（op.InitCache 的第一阶段
// 会 RefreshCache，把 DefaultSettings 补齐进缓存）。
func setupSettingTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// 每个用例从干净的任务注册表开始；结束前停掉本用例启动的调度循环，
	// 再重置注册表，避免跨用例污染。
	task.ResetForTest()
	t.Cleanup(func() {
		task.Shutdown()
		task.ResetForTest()
	})

	dsn := filepath.Join(t.TempDir(), "setting-test.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
}

func postSetting(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/setting/set", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	setSetting(c)
	return recorder
}

// TestSetSettingInvalidIntervalNotPersisted 锁住：周期设置非法值返回 400 且
// 不得覆盖库内/缓存中的现值（后续 task.Update 的目标状态不被脏值污染）。
func TestSetSettingInvalidIntervalNotPersisted(t *testing.T) {
	setupSettingTestDB(t)

	for _, tc := range []struct {
		name string
		key  model.SettingKey
		body string
	}{
		{name: "stats save zero", key: model.SettingKeyStatsSaveInterval, body: `{"key":"stats_save_interval","value":"0"}`},
		{name: "stats save not a number", key: model.SettingKeyStatsSaveInterval, body: `{"key":"stats_save_interval","value":"abc"}`},
		{name: "model info zero", key: model.SettingKeyModelInfoUpdateInterval, body: `{"key":"model_info_update_interval","value":"0"}`},
		{name: "LLM sync zero", key: model.SettingKeySyncLLMInterval, body: `{"key":"sync_llm_interval","value":"0"}`},
		{name: "key health not a number", key: model.SettingKeyKeyHealthCheckInterval, body: `{"key":"key_health_check_interval","value":"abc"}`},
		{name: "model info overflow", key: model.SettingKeyModelInfoUpdateInterval, body: `{"key":"model_info_update_interval","value":"2562048"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := stg.GetString(tc.key)
			if err != nil {
				t.Fatalf("get current value: %v", err)
			}

			recorder := postSetting(t, tc.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"message_key":"errors.inputValidationFailed"`) {
				t.Fatal("validation failure must include a localized message key")
			}
			var persisted model.Setting
			if err := db.GetDB().Where("key = ?", tc.key).First(&persisted).Error; err != nil {
				t.Fatalf("read persisted setting: %v", err)
			}
			if persisted.Value != before {
				t.Fatalf("invalid value written to database: %q", persisted.Value)
			}

			after, err := stg.GetString(tc.key)
			if err != nil {
				t.Fatalf("get value after rejected set: %v", err)
			}
			if after != before {
				t.Fatalf("invalid value must not be persisted: before=%q after=%q", before, after)
			}
		})
	}
}

// TestSetSettingValidIntervalPersisted 锁住：合法周期值持久化成功，并热更新
// Key 巡检任务周期（无需重启）。
func TestSetSettingValidIntervalPersisted(t *testing.T) {
	setupSettingTestDB(t)

	recorder := postSetting(t, `{"key":"key_health_check_interval","value":"45"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	v, err := stg.GetString(model.SettingKeyKeyHealthCheckInterval)
	if err != nil {
		t.Fatalf("get value: %v", err)
	}
	if v != "45" {
		t.Fatalf("persisted value = %q, want \"45\"", v)
	}
}

// TestSetSettingWebDAVConfigGenericPath 覆盖通用保存路径的 webdav_config：
// interval_hours 非法返回 400 且不持久化；合法值持久化并可被备份配置读取。
func TestSetSettingWebDAVConfigGenericPath(t *testing.T) {
	setupSettingTestDB(t)

	before, err := stg.GetString(model.SettingKeyWebDAVConfig)
	if err != nil {
		t.Fatalf("get webdav config: %v", err)
	}

	recorder := postSetting(t, `{"key":"webdav_config","value":"{\"enabled\":true,\"interval_hours\":0}"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	after, err := stg.GetString(model.SettingKeyWebDAVConfig)
	if err != nil {
		t.Fatalf("get webdav config after rejected set: %v", err)
	}
	if after != before {
		t.Fatalf("invalid webdav config must not be persisted: before=%q after=%q", before, after)
	}

	recorder = postSetting(t, `{"key":"webdav_config","value":"{\"enabled\":true,\"base_url\":\"https://dav.example.com\",\"remote_path\":\"/bk/\",\"interval_hours\":12,\"max_backups\":5}"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	cfg, err := backup.GetWebDAVConfig()
	if err != nil {
		t.Fatalf("GetWebDAVConfig: %v", err)
	}
	if cfg.IntervalHours != 12 {
		t.Fatalf("IntervalHours = %d, want 12", cfg.IntervalHours)
	}
	if !cfg.Enabled {
		t.Fatal("Enabled = false, want true")
	}
}

// TestSetWebDAVConfigDedicatedEndpointValidates 覆盖专用保存端点：
// interval 越界返回 400 且不持久化；合法值持久化成功。
func TestSetWebDAVConfigDedicatedEndpointValidates(t *testing.T) {
	setupSettingTestDB(t)

	post := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/backup/webdav/config", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		setWebDAVConfig(c)
		return recorder
	}

	recorder := post(`{"enabled":true,"base_url":"https://dav.example.com","interval_hours":0,"max_backups":10}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if cfg, err := backup.GetWebDAVConfig(); err != nil || cfg.IntervalHours != 6 {
		t.Fatalf("rejected save must leave default config intact: cfg=%+v err=%v", cfg, err)
	}

	recorder = post(`{"enabled":true,"base_url":"https://dav.example.com","interval_hours":72,"max_backups":3}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	cfg, err := backup.GetWebDAVConfig()
	if err != nil {
		t.Fatalf("GetWebDAVConfig: %v", err)
	}
	if cfg.IntervalHours != 72 || cfg.MaxBackups != 3 {
		t.Fatalf("persisted cfg = %+v, want interval=72 max_backups=3", cfg)
	}
}

func waitForCalls(t *testing.T, calls *atomic.Int32, want int32, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if calls.Load() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task did not reach %d calls within %v (got %d)", want, timeout, calls.Load())
}

// TestSetWebDAVConfigUpdatesRunningTaskInterval 锁住行为目标 a：通用设置保存
// 路径修改 webdav_config 后，已在运行的备份任务间隔立即更新（无需重启）。
func TestSetWebDAVConfigUpdatesRunningTaskInterval(t *testing.T) {
	setupSettingTestDB(t)

	var calls atomic.Int32
	task.Register(task.TaskWebDAVBackup, 20*time.Millisecond, false, func() { calls.Add(1) })
	go task.RUN()
	waitForCalls(t, &calls, 2, 3*time.Second)

	recorder := postSetting(t, `{"key":"webdav_config","value":"{\"enabled\":false,\"remote_path\":\"/bk/\",\"interval_hours\":6,\"max_backups\":10}"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	// 旧间隔 20ms 下 250ms 会累积多次执行；间隔切到 6h 后不得再新增。
	time.Sleep(250 * time.Millisecond)
	settled := calls.Load()
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if calls.Load() != settled {
			t.Fatalf("webdav task kept old interval after runtime update: before=%d after=%d", settled, calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSetKeyHealthCheckIntervalUpdatesRunningTask 锁住行为目标 a：修改
// key_health_check_interval 后，已在运行的 Key 巡检任务间隔立即更新。
func TestSetKeyHealthCheckIntervalUpdatesRunningTask(t *testing.T) {
	setupSettingTestDB(t)

	var calls atomic.Int32
	task.Register(task.TaskKeyHealthCheck, 20*time.Millisecond, false, func() { calls.Add(1) })
	go task.RUN()
	waitForCalls(t, &calls, 2, 3*time.Second)

	recorder := postSetting(t, `{"key":"key_health_check_interval","value":"45"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}

	time.Sleep(250 * time.Millisecond)
	settled := calls.Load()
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if calls.Load() != settled {
			t.Fatalf("key health task kept old interval after runtime update: before=%d after=%d", settled, calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
