package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2"
	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/helper"
	"github.com/lingyuins/octopus/internal/model"
	ch "github.com/lingyuins/octopus/internal/op/channel"
	grp "github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/server/auth"
	"github.com/lingyuins/octopus/internal/server/middleware"
	"github.com/lingyuins/octopus/internal/server/resp"
	"github.com/lingyuins/octopus/internal/server/router"
	"github.com/lingyuins/octopus/internal/utils/log"
)

func init() {
	router.NewGroupRouter("/api/v1/group").
		Use(middleware.Auth()).
		Use(middleware.RequirePermission(auth.PermGroupsRead)).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(getGroupList),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermGroupsWrite)).
				Handle(createGroup),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermGroupsWrite)).
				Handle(updateGroup),
		).
		AddRoute(
			router.NewRoute("/auto-group", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermGroupsWrite)).
				Handle(autoGroupModels),
		).
		AddRoute(
			router.NewRoute("/test", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermGroupsWrite)).
				Handle(startGroupTest),
		).
		AddRoute(
			router.NewRoute("/test-draft", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermGroupsWrite)).
				Handle(startDraftGroupTest),
		).
		AddRoute(
			router.NewRoute("/test/progress/:id", http.MethodGet).
				Handle(getGroupTestProgress),
		).
		AddRoute(
			router.NewRoute("/delete-all", http.MethodDelete).
				Use(middleware.RequirePermission(auth.PermGroupsWrite)).
				Handle(deleteAllGroups),
		).
		AddRoute(
			router.NewRoute("/purge-unavailable", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermGroupsWrite)).
				Handle(purgeUnavailableGroupItems),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Use(middleware.RequirePermission(auth.PermGroupsWrite)).
				Handle(deleteGroup),
		).
		AddRoute(
			router.NewRoute("/sort", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermGroupsWrite)).
				Handle(sortGroupItems),
		).
		AddRoute(
			router.NewRoute("/apply-defaults", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermGroupsWrite)).
				Handle(applyGroupDefaults),
		)
	// AddRoute(
	// 	router.NewRoute("/auto-add-item", http.MethodPost).
	// 		Handle(autoAddGroupItem),
	// )
}

func getGroupList(c *gin.Context) {
	groups, err := grp.GroupList(c.Request.Context())
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, normalizeGroupListResponse(groups))
}

func createGroup(c *gin.Context) {
	var group model.Group
	if err := c.ShouldBindJSON(&group); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	group.EndpointType = model.NormalizeEndpointType(group.EndpointType)
	if group.EndpointType == model.EndpointTypeAll {
		resp.Error(c, http.StatusBadRequest, "endpoint_type '*' is no longer supported; use a specific type (e.g. 'chat')")
		return
	}
	group.EndpointProvider = strings.ToLower(strings.TrimSpace(group.EndpointProvider))
	group.OutboundFormat = strings.ToLower(strings.TrimSpace(group.OutboundFormat))
	if group.MatchRegex != "" {
		_, err := regexp2.Compile(group.MatchRegex, regexp2.ECMAScript)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := grp.GroupCreate(&group, c.Request.Context()); err != nil {
		if status, message, ok := classifyGroupMutationError(err); ok {
			resp.Error(c, status, message)
			return
		}
		log.Errorf("create group failed: %v", err)
		resp.InternalError(c)
		return
	}
	resp.Success(c, group)
}

func updateGroup(c *gin.Context) {
	var req model.GroupUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if req.EndpointType != nil {
		normalized := model.NormalizeEndpointType(*req.EndpointType)
		if normalized == model.EndpointTypeAll {
			resp.Error(c, http.StatusBadRequest, "endpoint_type '*' is no longer supported; use a specific type (e.g. 'chat')")
			return
		}
		req.EndpointType = &normalized
	}
	if req.EndpointProvider != nil {
		normalizedProvider := strings.ToLower(strings.TrimSpace(*req.EndpointProvider))
		req.EndpointProvider = &normalizedProvider
	}
	if req.OutboundFormat != nil {
		normalizedFormat := strings.ToLower(strings.TrimSpace(*req.OutboundFormat))
		req.OutboundFormat = &normalizedFormat
	}
	if req.MatchRegex != nil {
		_, err := regexp2.Compile(*req.MatchRegex, regexp2.ECMAScript)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	group, err := grp.GroupUpdate(&req, c.Request.Context())
	if err != nil {
		if status, message, ok := classifyGroupMutationError(err); ok {
			resp.Error(c, status, message)
			return
		}
		log.Errorf("update group failed: %v", err)
		resp.InternalError(c)
		return
	}
	resp.Success(c, group)
}

func classifyGroupMutationError(err error) (int, string, bool) {
	if err == nil {
		return 0, "", false
	}

	msg := strings.ToLower(err.Error())

	switch {
	case strings.Contains(msg, "group name is required"):
		return http.StatusBadRequest, "group name is required", true
	case (strings.Contains(msg, "endpoint_type") || strings.Contains(msg, "endpoint_provider")) &&
		(strings.Contains(msg, "no such column") ||
			strings.Contains(msg, "has no column named") ||
			strings.Contains(msg, "unknown column") ||
			strings.Contains(msg, "does not exist")):
		return http.StatusServiceUnavailable, "database schema is outdated; restart the service to apply the latest migrations", true
	case strings.Contains(msg, "unique constraint failed: groups.endpoint_type, groups.name") ||
		strings.Contains(msg, "idx_groups_endpoint_name") ||
		(strings.Contains(msg, "duplicate entry") && strings.Contains(msg, "idx_groups_endpoint_name")) ||
		(strings.Contains(msg, "duplicate key value violates unique constraint") && strings.Contains(msg, "idx_groups_endpoint_name")):
		return http.StatusConflict, "a group with this name and endpoint type already exists", true
	case strings.Contains(msg, "unique constraint failed: group_items.group_id, group_items.channel_id, group_items.model_name") ||
		(strings.Contains(msg, "duplicate entry") && strings.Contains(msg, "idx_group_channel_model")) ||
		(strings.Contains(msg, "duplicate key value violates unique constraint") && strings.Contains(msg, "idx_group_channel_model")):
		return http.StatusConflict, "group contains duplicate channel/model items", true
	default:
		return 0, "", false
	}
}

func startGroupTest(c *gin.Context) {
	var req helper.GroupModelTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}

	group, err := grp.GroupGet(req.GroupID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusNotFound, "group not found")
		return
	}

	channels := make(map[int]model.Channel, len(group.Items))
	for _, item := range group.Items {
		if _, ok := channels[item.ChannelID]; ok {
			continue
		}
		channel, err := ch.Get(item.ChannelID, c.Request.Context())
		if err != nil {
			continue
		}
		channels[item.ChannelID] = *channel
	}

	progress, err := helper.StartGroupModelTest(group, channels)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "failed to start group test")
		return
	}
	resp.Success(c, progress)
}

func startDraftGroupTest(c *gin.Context) {
	var req helper.GroupModelDraftTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}

	if len(req.Items) == 0 {
		resp.Error(c, http.StatusBadRequest, "group has no items")
		return
	}

	channels := make(map[int]model.Channel, len(req.Items))
	for _, item := range req.Items {
		if _, ok := channels[item.ChannelID]; ok {
			continue
		}
		channel, err := ch.Get(item.ChannelID, c.Request.Context())
		if err != nil {
			continue
		}
		channels[item.ChannelID] = *channel
	}

	progress, err := helper.StartDraftGroupModelTest(req.EndpointType, req.Items, channels)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "failed to start group test")
		return
	}
	resp.Success(c, progress)
}

func getGroupTestProgress(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		resp.Error(c, http.StatusBadRequest, "missing progress id")
		return
	}

	progress, ok := helper.GetGroupModelTestProgress(id)
	if !ok {
		resp.Error(c, http.StatusNotFound, "group test progress not found")
		return
	}

	resp.Success(c, progress)
}

type autoGroupRequest struct {
	Category string `json:"category,omitempty"`
}

func autoGroupModels(c *gin.Context) {
	var req autoGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	result, err := grp.AutoGroupModelsWithCategory(c.Request.Context(), req.Category)
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, result)
}

func deleteGroup(c *gin.Context) {
	id := c.Param("id")
	idNum, err := strconv.Atoi(id)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid group id")
		return
	}
	if err := grp.GroupDel(idNum, c.Request.Context()); err != nil {
		log.Errorf("delete group %d failed: %v", idNum, err)
		resp.InternalError(c)
		return
	}
	resp.Success(c, "group deleted successfully")
}

func deleteAllGroups(c *gin.Context) {
	deletedCount, err := grp.GroupDelAll(c.Request.Context())
	if err != nil {
		log.Errorf("delete all groups failed: %v", err)
		resp.InternalError(c)
		return
	}
	resp.Success(c, gin.H{"deleted_count": deletedCount})
}

func purgeUnavailableGroupItems(c *gin.Context) {
	result, err := grp.PurgeUnavailableItems(c.Request.Context())
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, result)
}

// sortGroupItems 按各自分组 sort_strategy（空则跟随全局）重排成员并回写 priority。
// 请求体 {"group_ids":[...]}，空数组=全部的分组成员为空直接返回 0，不报错。
func sortGroupItems(c *gin.Context) {
	var req struct {
		GroupIDs []int `json:"group_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	sorted, err := grp.SortGroupsByStrategy(req.GroupIDs, c.Request.Context())
	if err != nil {
		log.Errorf("sort group items failed: %v", err)
		resp.InternalError(c)
		return
	}
	resp.Success(c, gin.H{"sorted_count": sorted})
}

// applyGroupDefaults 一键应用分组默认：负载模式全量覆盖 + 全部分组重排 + 倍率上限阻断重算。
func applyGroupDefaults(c *gin.Context) {
	result, err := grp.ApplyGroupDefaults(c.Request.Context())
	if err != nil {
		log.Errorf("apply group defaults failed: %v", err)
		resp.InternalError(c)
		return
	}
	resp.Success(c, result)
}

// func autoAddGroupItem(c *gin.Context) {
// 	var req struct {
// 		ID int `json:"id"`
// 	}
// 	if err := c.ShouldBindJSON(&req); err != nil {
// 		resp.Error(c, http.StatusBadRequest, err.Error())
// 		return
// 	}
// 	if req.ID <= 0 {
// 		resp.Error(c, http.StatusBadRequest, "invalid id")
// 		return
// 	}
// 	err := worker.AutoAddGroupItem(req.ID, c.Request.Context())
// 	if err != nil {
// 		resp.Error(c, http.StatusInternalServerError, err.Error())
// 		return
// 	}
// 	resp.Success(c, nil)
// }

type groupListResponseItem struct {
	ID                int               `json:"id"`
	Name              string            `json:"name"`
	EndpointType      string            `json:"endpoint_type"`
	EndpointProvider  string            `json:"endpoint_provider,omitempty"`
	OutboundFormat    string            `json:"outbound_format,omitempty"`
	Mode              model.GroupMode   `json:"mode"`
	MatchRegex        string            `json:"match_regex"`
	SortStrategy      string            `json:"sort_strategy,omitempty"`
	FirstTokenTimeOut int               `json:"first_token_time_out"`
	AttemptTimeOut    int               `json:"attempt_time_out"`
	StreamIdleTimeout int               `json:"stream_idle_timeout"`
	SessionKeepTime   int               `json:"session_keep_time"`
	Condition         string            `json:"condition,omitempty"`
	Items             []model.GroupItem `json:"items"`
	LastTestPassed    *bool             `json:"last_test_passed,omitempty"`
	LastTestAt        int64             `json:"last_test_at,omitempty"`
}

func normalizeGroupListResponse(groups []model.Group) []groupListResponseItem {
	if len(groups) == 0 {
		return make([]groupListResponseItem, 0)
	}

	items := make([]groupListResponseItem, len(groups))
	for i, group := range groups {
		groupItems := group.Items
		if groupItems == nil {
			groupItems = make([]model.GroupItem, 0)
		}
		items[i] = groupListResponseItem{
			ID:                group.ID,
			Name:              group.Name,
			EndpointType:      group.EndpointType,
			EndpointProvider:  group.EndpointProvider,
			OutboundFormat:    group.OutboundFormat,
			Mode:              group.Mode,
			MatchRegex:        group.MatchRegex,
			SortStrategy:      group.SortStrategy,
			FirstTokenTimeOut: group.FirstTokenTimeOut,
			AttemptTimeOut:    group.AttemptTimeOut,
			StreamIdleTimeout: group.StreamIdleTimeout,
			SessionKeepTime:   group.SessionKeepTime,
			Items:             groupItems,
			LastTestPassed:    group.LastTestPassed,
			LastTestAt:        group.LastTestAt,
		}
	}
	return items
}
