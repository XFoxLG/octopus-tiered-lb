package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/op"
	"github.com/lingyuins/octopus/internal/op/backup"
	"github.com/lingyuins/octopus/internal/server/auth"
	"github.com/lingyuins/octopus/internal/server/middleware"
	"github.com/lingyuins/octopus/internal/server/resp"
	"github.com/lingyuins/octopus/internal/server/router"
	"github.com/lingyuins/octopus/internal/task"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func init() {
	router.NewGroupRouter("/api/v1/backup/webdav").
		Use(middleware.Auth()).
		Use(middleware.RequirePermission(auth.PermSettingsRead)).
		AddRoute(
			router.NewRoute("/config", http.MethodGet).
				Handle(getWebDAVConfig),
		).
		AddRoute(
			router.NewRoute("/config", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermSettingsWrite)).
				Use(middleware.RequireJSON()).
				Handle(setWebDAVConfig),
		).
		AddRoute(
			router.NewRoute("/test", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermSettingsWrite)).
				Handle(testWebDAVConnection),
		).
		AddRoute(
			router.NewRoute("/backup", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermSettingsWrite)).
				Handle(triggerWebDAVBackup),
		).
		AddRoute(
			router.NewRoute("/restore", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermSettingsWrite)).
				Use(middleware.RequireJSON()).
				Handle(restoreFromWebDAV),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listWebDAVBackups),
		).
		AddRoute(
			router.NewRoute("/delete", http.MethodDelete).
				Use(middleware.RequirePermission(auth.PermSettingsWrite)).
				Handle(deleteWebDAVBackup),
		)
}

func getWebDAVConfig(c *gin.Context) {
	cfg, err := backup.GetWebDAVConfig()
	if err != nil {
		resp.InternalError(c)
		return
	}
	// Mask password in response
	masked := *cfg
	if masked.Password != "" {
		masked.Password = "******"
	}
	resp.Success(c, masked)
}

func setWebDAVConfig(c *gin.Context) {
	var cfg backup.WebDAVBackupConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}

	// If password is masked, keep existing password
	if cfg.Password == "******" {
		existing, err := backup.GetWebDAVConfig()
		if err != nil {
			resp.InternalError(c)
			return
		}
		cfg.Password = existing.Password
	}

	// Validate
	if cfg.IntervalHours < 1 || cfg.IntervalHours > 168 {
		resp.Error(c, http.StatusBadRequest, "interval_hours must be between 1 and 168")
		return
	}
	if cfg.MaxBackups < 1 {
		cfg.MaxBackups = 10
	}
	if cfg.RemotePath == "" {
		cfg.RemotePath = "/octopus-backup/"
	}

	if err := backup.SetWebDAVConfig(&cfg); err != nil {
		resp.InternalError(c)
		return
	}

	// 配置已持久化。备份任务周期同步为尽力而为：失败只记日志，不向客户端返回
	// 错误（那会误导为"配置未保存"）。interval 已在上方校验为 1..168。
	task.Update(task.TaskWebDAVBackup, time.Duration(cfg.IntervalHours)*time.Hour)

	resp.Success(c, true)
}

func testWebDAVConnection(c *gin.Context) {
	cfg, err := backup.GetWebDAVConfig()
	if err != nil {
		resp.InternalError(c)
		return
	}

	// Allow override from request body
	var override struct {
		BaseURL  string `json:"base_url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	_ = c.ShouldBindJSON(&override)

	baseURL := override.BaseURL
	if baseURL == "" {
		baseURL = cfg.BaseURL
	}
	username := override.Username
	if username == "" {
		username = cfg.Username
	}
	password := override.Password
	if password == "" || password == "******" {
		password = cfg.Password
	}

	if baseURL == "" {
		resp.Error(c, http.StatusBadRequest, "base_url is required")
		return
	}

	if err := xurl.AssertSafeURL(baseURL); err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid base_url: "+err.Error())
		return
	}
	client := backup.NewWebDAVClient(baseURL, username, password)
	if err := client.Test(); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	resp.Success(c, true)
}

func triggerWebDAVBackup(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	if err := backup.PerformWebDAVBackupManual(ctx); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, true)
}

func restoreFromWebDAV(c *gin.Context) {
	var req struct {
		Filename string `json:"filename" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}

	result, err := backup.RestoreFromWebDAV(c.Request.Context(), req.Filename)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	// Refresh caches after restore
	if err := op.InitCache(); err != nil {
		resp.InternalError(c)
		return
	}

	resp.Success(c, result)
}

func listWebDAVBackups(c *gin.Context) {
	files, err := backup.ListWebDAVBackups()
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, files)
}

func deleteWebDAVBackup(c *gin.Context) {
	filename := c.Query("filename")
	if filename == "" {
		resp.Error(c, http.StatusBadRequest, "filename is required")
		return
	}

	if err := backup.DeleteWebDAVBackup(filename); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	resp.Success(c, true)
}
