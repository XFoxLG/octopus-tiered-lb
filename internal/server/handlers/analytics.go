package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/analytics"
	"github.com/lingyuins/octopus/internal/server/auth"
	"github.com/lingyuins/octopus/internal/server/middleware"
	"github.com/lingyuins/octopus/internal/server/resp"
	"github.com/lingyuins/octopus/internal/server/router"
)

func init() {
	router.NewGroupRouter("/api/v1/analytics").
		Use(middleware.Auth()).
		Use(middleware.RequirePermission(auth.PermStatsRead)).
		AddRoute(
			router.NewRoute("/overview", http.MethodGet).
				Handle(getAnalyticsOverview),
		).
		AddRoute(
			router.NewRoute("/utilization", http.MethodGet).
				Handle(getAnalyticsUtilization),
		).
		AddRoute(
			router.NewRoute("/group-health", http.MethodGet).
				Handle(getAnalyticsGroupHealth),
		).
		AddRoute(
			router.NewRoute("/provider-breakdown", http.MethodGet).
				Handle(getAnalyticsProviderBreakdown),
		).
		AddRoute(
			router.NewRoute("/model-breakdown", http.MethodGet).
				Handle(getAnalyticsModelBreakdown),
		).
		AddRoute(
			router.NewRoute("/channel-model", http.MethodGet).
				Handle(getAnalyticsChannelModel),
		).
		AddRoute(
			router.NewRoute("/auto-strategy", http.MethodGet).
				Handle(getAnalyticsAutoStrategy),
		).
		AddRoute(
			router.NewRoute("/apikey-breakdown", http.MethodGet).
				Handle(getAnalyticsAPIKeyBreakdown),
		).
		AddRoute(
			router.NewRoute("/latency-distribution", http.MethodGet).
				Handle(getAnalyticsLatencyDistribution),
		)
}

func getAnalyticsOverview(c *gin.Context) {
	analyticsRange, ok := parseAnalyticsRange(c)
	if !ok {
		return
	}

	data, err := analytics.CachedAnalyticsOverviewGet(c.Request.Context(), analyticsRange, parseCacheTTL(c))
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, data)
}

func getAnalyticsUtilization(c *gin.Context) {
	analyticsRange, ok := parseAnalyticsRange(c)
	if !ok {
		return
	}

	data, err := analytics.CachedAnalyticsUtilizationGet(c.Request.Context(), analyticsRange, parseCacheTTL(c))
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, data)
}

func getAnalyticsGroupHealth(c *gin.Context) {
	data, err := analytics.CachedAnalyticsGroupHealthGet(c.Request.Context(), parseCacheTTL(c))
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, data)
}

func getAnalyticsProviderBreakdown(c *gin.Context) {
	analyticsRange, ok := parseAnalyticsRange(c)
	if !ok {
		return
	}

	data, err := analytics.CachedAnalyticsProviderBreakdownGet(c.Request.Context(), analyticsRange, parseCacheTTL(c))
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, data)
}

func getAnalyticsModelBreakdown(c *gin.Context) {
	analyticsRange, ok := parseAnalyticsRange(c)
	if !ok {
		return
	}

	data, err := analytics.CachedAnalyticsModelBreakdownGet(c.Request.Context(), analyticsRange, parseCacheTTL(c))
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, data)
}

func getAnalyticsChannelModel(c *gin.Context) {
	analyticsRange, ok := parseAnalyticsRange(c)
	if !ok {
		return
	}
	var groupID *int
	if v := c.Query("group_id"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, "invalid group_id")
			return
		}
		groupID = &n
	}

	data, err := analytics.CachedAnalyticsChannelModelBreakdownGet(c.Request.Context(), analyticsRange, groupID, parseCacheTTL(c))
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, data)
}

func getAnalyticsAutoStrategy(c *gin.Context) {
	var groupID *int
	if v := c.Query("group_id"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, "invalid group_id")
			return
		}
		groupID = &n
	}

	data, err := analytics.AnalyticsAutoStrategyGet(c.Request.Context(), groupID)
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, data)
}

func getAnalyticsAPIKeyBreakdown(c *gin.Context) {
	analyticsRange, ok := parseAnalyticsRange(c)
	if !ok {
		return
	}

	data, err := analytics.CachedAnalyticsAPIKeyBreakdownGet(c.Request.Context(), analyticsRange, parseCacheTTL(c))
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, data)
}

func getAnalyticsLatencyDistribution(c *gin.Context) {
	analyticsRange, ok := parseAnalyticsRange(c)
	if !ok {
		return
	}
	data, err := analytics.CachedAnalyticsLatencyDistributionGet(c.Request.Context(), analyticsRange, parseCacheTTL(c))
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, data)
}

func parseAnalyticsRange(c *gin.Context) (model.AnalyticsRange, bool) {
	analyticsRange, err := model.ParseAnalyticsRange(c.Query("range"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return "", false
	}
	return analyticsRange, true
}

// parseCacheTTL 解析 cache_ttl 查询参数（10s/30s/1m/off）。空值回退到默认 30s。
func parseCacheTTL(c *gin.Context) analytics.CacheTTL {
	return analytics.ParseCacheTTL(c.Query("cache_ttl"))
}
