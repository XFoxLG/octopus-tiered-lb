package handlers

import (
	"fmt"
	"github.com/lingyuins/octopus/internal/utils/json"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/server/auth"
	"github.com/lingyuins/octopus/internal/server/middleware"
	"github.com/lingyuins/octopus/internal/server/resp"
	"github.com/lingyuins/octopus/internal/server/router"
)

func init() {
	router.NewGroupRouter("/api/v1/log").
		Use(middleware.Auth()).
		Use(middleware.RequirePermission(auth.PermLogsRead)).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listLog),
		).
		AddRoute(
			router.NewRoute("/detail", http.MethodGet).
				Handle(logDetail),
		).
		AddRoute(
			router.NewRoute("/content", http.MethodGet).
				Handle(logContent),
		).
		AddRoute(
			router.NewRoute("/health", http.MethodGet).
				Handle(logHealth),
		).
		AddRoute(
			router.NewRoute("/clear", http.MethodDelete).
				Use(middleware.RequirePermission(auth.PermLogsWrite)).
				Handle(clearLog),
		).
		AddRoute(
			router.NewRoute("/clear-contents", http.MethodDelete).
				Use(middleware.RequirePermission(auth.PermLogsWrite)).
				Handle(clearLogContents),
		).
		AddRoute(
			router.NewRoute("/stream", http.MethodGet).
				Handle(streamLog),
		)
}

func listLog(c *gin.Context) {
	page, pageSize := parsePagination(c.DefaultQuery("page", "1"), c.DefaultQuery("page_size", "20"))

	filter := relaylog.LogFilter{}

	// include_attempts 默认：显式传 "false" 才关闭；否则当筛选了 channel_id 时默认开启，
	// 让"在渠道A 失败→重试到B 成功"的请求也能被渠道A 命中（issue #67）。
	includeAttemptsExplicit := false
	if v := c.Query("include_attempts"); v != "" {
		switch strings.ToLower(v) {
		case "true", "1":
			filter.IncludeAttempts = true
			includeAttemptsExplicit = true
		case "false", "0":
			filter.IncludeAttempts = false
			includeAttemptsExplicit = true
		default:
			resp.Error(c, http.StatusBadRequest, "invalid include_attempts (must be 'true' or 'false')")
			return
		}
	}

	if v := c.Query("start_time"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, "invalid start_time")
			return
		}
		filter.StartTime = &n
	}
	if v := c.Query("end_time"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, "invalid end_time")
			return
		}
		filter.EndTime = &n
	}
	if v := c.Query("model"); v != "" {
		filter.Model = v
	}
	// models 支持逗号分隔的多个模型名（精确匹配，不区分大小写），issue #117。
	// 空字符串、纯空白条目会被过滤逻辑忽略。
	if v := c.Query("models"); v != "" {
		parts := strings.Split(v, ",")
		filter.Models = make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				filter.Models = append(filter.Models, t)
			}
		}
	}
	if v := c.Query("channel_id"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, "invalid channel_id")
			return
		}
		filter.ChannelID = &n
		// 未显式指定 include_attempts 时，按渠道筛选默认穿透尝试维度（issue #67）。
		if !includeAttemptsExplicit {
			filter.IncludeAttempts = true
		}
	}
	if v := c.Query("api_key_id"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, "invalid api_key_id")
			return
		}
		filter.APIKeyID = &n
	}
	if v := c.Query("endpoint_type"); v != "" {
		filter.EndpointType = v
	}
	if v := c.Query("status"); v != "" {
		switch v {
		case "success":
			b := false
			filter.HasError = &b
		case "error":
			b := true
			filter.HasError = &b
		default:
			resp.Error(c, http.StatusBadRequest, "invalid status (must be 'success' or 'error')")
			return
		}
	}
	if v := c.Query("is_test"); v != "" {
		switch strings.ToLower(v) {
		case "true", "1":
			b := true
			filter.IsTest = &b
		case "false", "0":
			b := false
			filter.IsTest = &b
		default:
			resp.Error(c, http.StatusBadRequest, "invalid is_test (must be 'true' or 'false')")
			return
		}
	}

	logs, err := relaylog.RelayLogList(c.Request.Context(), filter, page, pageSize)
	if err != nil {
		resp.InternalError(c)
		return
	}

	resp.Success(c, logs)
}

func logDetail(c *gin.Context) {
	idStr := c.Query("id")
	if idStr == "" {
		resp.Error(c, http.StatusBadRequest, "id is required")
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid id")
		return
	}

	log, err := relaylog.RelayLogGetByID(c.Request.Context(), id)
	if err != nil {
		resp.InternalError(c)
		return
	}
	if log == nil {
		resp.Error(c, http.StatusNotFound, "log not found")
		return
	}

	resp.Success(c, log)
}

func logContent(c *gin.Context) {
	referenceID, err := strconv.ParseInt(c.Query("ref_id"), 10, 64)
	if err != nil || referenceID <= 0 {
		resp.Error(c, http.StatusBadRequest, "invalid ref_id")
		return
	}
	contentRef, payload, err := relaylog.RelayLogContentGet(c.Request.Context(), referenceID)
	if err != nil {
		resp.InternalError(c)
		return
	}
	if contentRef == nil {
		resp.Error(c, http.StatusNotFound, "log content not found")
		return
	}
	if payload == nil {
		resp.Error(c, http.StatusGone, "log content is no longer available")
		return
	}

	contentType := strings.TrimSpace(contentRef.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if contentRef.FileName != "" {
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": contentRef.FileName}))
	}
	c.Data(http.StatusOK, contentType, payload)
}

func logHealth(c *gin.Context) {
	resp.Success(c, relaylog.RelayLogHealthSnapshot())
}

func clearLog(c *gin.Context) {
	if err := relaylog.RelayLogClear(c.Request.Context()); err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, nil)
}

func clearLogContents(c *gin.Context) {
	if err := relaylog.RelayLogClearContents(c.Request.Context()); err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, nil)
}

func streamLog(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	logChan := relaylog.RelayLogSubscribe()
	defer relaylog.RelayLogUnsubscribe(logChan)

	heartbeatTicker := time.NewTicker(conf.SSEHeartbeatInterval)
	defer heartbeatTicker.Stop()

	if _, err := c.Writer.Write([]byte(": connected\n\n")); err != nil {
		return
	}
	c.Writer.Flush()

	ctx := c.Request.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeatTicker.C:
			if _, err := c.Writer.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			c.Writer.Flush()
		case log, ok := <-logChan:
			if !ok {
				return
			}
			if relaylog.RelayLogStreamExcluded(log.RequestModelName) {
				continue
			}
			// relaylog 已在入队前转换为轻量列表项，正文和附件不会进入
			// SSE 广播队列，避免慢订阅者长期持有大 payload。
			data, err := json.Marshal(log)
			if err != nil {
				continue
			}
			if _, err := c.Writer.Write([]byte(fmt.Sprintf("data: %s\n\n", data))); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}
