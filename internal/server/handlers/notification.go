package handlers

import (
	"fmt"
	"github.com/lingyuins/octopus/internal/utils/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/conf"
	notifop "github.com/lingyuins/octopus/internal/op/notification"
	"github.com/lingyuins/octopus/internal/server/auth"
	"github.com/lingyuins/octopus/internal/server/middleware"
	"github.com/lingyuins/octopus/internal/server/resp"
	"github.com/lingyuins/octopus/internal/server/router"
)

func init() {
	router.NewGroupRouter("/api/v1/notification").
		Use(middleware.Auth()).
		Use(middleware.RequirePermission(auth.PermNotificationsRead)).
		AddRoute(router.NewRoute("/list", http.MethodGet).Handle(listNotifications)).
		AddRoute(router.NewRoute("/unread-count", http.MethodGet).Handle(getNotificationUnreadCount)).
		AddRoute(router.NewRoute("/detail/:id", http.MethodGet).Handle(getNotificationDetail)).
		AddRoute(router.NewRoute("/stream", http.MethodGet).Handle(streamNotifications)).
		AddRoute(router.NewRoute("/mark-read", http.MethodPost).Handle(markNotificationsRead)).
		AddRoute(router.NewRoute("/mark-unread", http.MethodPost).Handle(markNotificationsUnread)).
		AddRoute(router.NewRoute("/mark-all-read", http.MethodPost).Handle(markAllNotificationsRead)).
		AddRoute(router.NewRoute("/archive", http.MethodPost).Handle(archiveNotifications)).
		AddRoute(router.NewRoute("/unarchive", http.MethodPost).Handle(unarchiveNotifications)).
		AddRoute(router.NewRoute("/delete/:id", http.MethodDelete).Use(middleware.RequirePermission(auth.PermNotificationsWrite)).Handle(deleteNotification)).
		AddRoute(router.NewRoute("/archived", http.MethodDelete).Use(middleware.RequirePermission(auth.PermNotificationsWrite)).Handle(deleteArchivedNotifications))
}

type notificationIDsPayload struct {
	IDs []int64 `json:"ids"`
}

func listNotifications(c *gin.Context) {
	page, pageSize := parsePagination(c.DefaultQuery("page", "1"), c.DefaultQuery("page_size", "20"))
	filter := parseNotificationFilter(c)
	items, err := notifop.List(c.Request.Context(), filter, page, pageSize)
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, items)
}

func getNotificationUnreadCount(c *gin.Context) {
	archived := strings.EqualFold(c.Query("archived"), "true") || c.Query("archived") == "1"
	count, err := notifop.UnreadCount(c.Request.Context(), archived)
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, gin.H{"count": count})
}

func getNotificationDetail(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		resp.Error(c, http.StatusBadRequest, "invalid id")
		return
	}
	item, err := notifop.Get(c.Request.Context(), id)
	if err != nil {
		resp.InternalError(c)
		return
	}
	if item == nil {
		resp.Error(c, http.StatusNotFound, "notification not found")
		return
	}
	resp.Success(c, item)
}

func markNotificationsRead(c *gin.Context) {
	ids, ok := bindNotificationIDs(c)
	if !ok {
		return
	}
	if err := notifop.MarkRead(c.Request.Context(), ids); err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, nil)
}

func markNotificationsUnread(c *gin.Context) {
	ids, ok := bindNotificationIDs(c)
	if !ok {
		return
	}
	if err := notifop.MarkUnread(c.Request.Context(), ids); err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, nil)
}

func markAllNotificationsRead(c *gin.Context) {
	if err := notifop.MarkAllRead(c.Request.Context()); err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, nil)
}

func archiveNotifications(c *gin.Context) {
	ids, ok := bindNotificationIDs(c)
	if !ok {
		return
	}
	if err := notifop.Archive(c.Request.Context(), ids); err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, nil)
}

func unarchiveNotifications(c *gin.Context) {
	ids, ok := bindNotificationIDs(c)
	if !ok {
		return
	}
	if err := notifop.Unarchive(c.Request.Context(), ids); err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, nil)
}

func deleteNotification(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		resp.Error(c, http.StatusBadRequest, "invalid id")
		return
	}
	if err := notifop.Delete(c.Request.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			resp.Error(c, http.StatusNotFound, err.Error())
			return
		}
		resp.InternalError(c)
		return
	}
	resp.Success(c, nil)
}

func deleteArchivedNotifications(c *gin.Context) {
	if err := notifop.DeleteArchived(c.Request.Context()); err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, nil)
}

func streamNotifications(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	notificationChan := notifop.Subscribe()
	defer notifop.Unsubscribe(notificationChan)

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
		case item, ok := <-notificationChan:
			if !ok {
				return
			}
			data, err := json.Marshal(item)
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

func parseNotificationFilter(c *gin.Context) notifop.ListFilter {
	filter := notifop.ListFilter{
		Type:     c.Query("type"),
		Severity: c.Query("severity"),
		Source:   c.Query("source"),
		Search:   c.Query("search"),
	}
	if v := c.Query("read"); v != "" {
		b := v == "true" || v == "1"
		filter.Read = &b
	}
	if v := c.Query("archived"); v != "" {
		b := v == "true" || v == "1"
		filter.Archived = &b
	}
	if v := c.Query("start_time"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.StartTime = &n
		}
	}
	if v := c.Query("end_time"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.EndTime = &n
		}
	}
	return filter
}

func bindNotificationIDs(c *gin.Context) ([]int64, bool) {
	var payload notificationIDsPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request body")
		return nil, false
	}
	ids := make([]int64, 0, len(payload.IDs))
	for _, id := range payload.IDs {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		resp.Error(c, http.StatusBadRequest, "ids is required")
		return nil, false
	}
	return ids, true
}
