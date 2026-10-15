package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/op/channelmodellimit"
	"github.com/lingyuins/octopus/internal/server/auth"
	"github.com/lingyuins/octopus/internal/server/middleware"
	"github.com/lingyuins/octopus/internal/server/resp"
	"github.com/lingyuins/octopus/internal/server/router"
)

// v2.9.0：渠道×模型上下文上限（manual 人工声明 / learned 学习）读写接口。
// manual 永不被学习覆盖；max_context_tokens <= 0 等价于删除声明。
func init() {
	router.NewGroupRouter("/api/v1/channel-model-limit").
		Use(middleware.Auth()).
		Use(middleware.RequirePermission(auth.PermChannelsRead)).
		AddRoute(router.NewRoute("", http.MethodGet).Handle(listChannelModelLimits)).
		AddRoute(router.NewRoute("", http.MethodPut).
			Use(middleware.RequirePermission(auth.PermChannelsWrite)).
			Handle(setChannelModelLimit)).
		AddRoute(router.NewRoute("", http.MethodDelete).
			Use(middleware.RequirePermission(auth.PermChannelsWrite)).
			Handle(deleteChannelModelLimit))
}

type channelModelLimitSetRequest struct {
	ChannelID        int    `json:"channel_id"`
	ModelName        string `json:"model_name"`
	MaxContextTokens int    `json:"max_context_tokens"`
}

func listChannelModelLimits(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Query("channel_id"))
	if err != nil || channelID <= 0 {
		resp.Error(c, http.StatusBadRequest, "channel_id is required")
		return
	}
	entries, err := channelmodellimit.ListByChannel(c.Request.Context(), channelID)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, entries)
}

func setChannelModelLimit(c *gin.Context) {
	var req channelModelLimitSetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.ChannelID <= 0 || strings.TrimSpace(req.ModelName) == "" {
		resp.Error(c, http.StatusBadRequest, "channel_id and model_name are required")
		return
	}
	if err := channelmodellimit.SetManual(c.Request.Context(), req.ChannelID, req.ModelName, req.MaxContextTokens); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nil)
}

func deleteChannelModelLimit(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Query("channel_id"))
	if err != nil || channelID <= 0 {
		resp.Error(c, http.StatusBadRequest, "channel_id is required")
		return
	}
	modelName := strings.TrimSpace(c.Query("model_name"))
	if modelName == "" {
		resp.Error(c, http.StatusBadRequest, "model_name is required")
		return
	}
	if err := channelmodellimit.Delete(c.Request.Context(), channelID, modelName); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nil)
}
