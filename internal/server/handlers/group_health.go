package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/helper"
	"github.com/lingyuins/octopus/internal/model"
	grp "github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/server/auth"
	"github.com/lingyuins/octopus/internal/server/middleware"
	"github.com/lingyuins/octopus/internal/server/resp"
	"github.com/lingyuins/octopus/internal/server/router"
	"github.com/lingyuins/octopus/internal/utils/log"
	"gorm.io/gorm"
)

var defaultGroupHealthRepo = grp.NewGroupHealthRepository()

func init() {
	router.NewGroupRouter("/api/v1/group").
		Use(middleware.Auth()).
		Use(middleware.RequirePermission(auth.PermGroupsRead)).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/health/run/:id", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermGroupsWrite)).
				Handle(runGroupHealth),
		).
		AddRoute(
			router.NewRoute("/health/run-all", http.MethodPost).
				Use(middleware.RequirePermission(auth.PermGroupsWrite)).
				Handle(runAllGroupHealth),
		).
		AddRoute(
			router.NewRoute("/health/latest/:id", http.MethodGet).
				Handle(getGroupHealthLatest),
		).
		AddRoute(
			router.NewRoute("/health/views", http.MethodGet).
				Handle(listGroupHealthViews),
		)
}

func parseGroupHealthProbeMode(raw string) (model.GroupHealthProbeMode, error) {
	switch model.GroupHealthProbeMode(strings.TrimSpace(raw)) {
	case "", model.GroupHealthProbeModeStandard:
		return model.GroupHealthProbeModeStandard, nil
	case model.GroupHealthProbeModeFull:
		return model.GroupHealthProbeModeFull, nil
	default:
		return "", errors.New("probe_mode must be standard or full")
	}
}

func runGroupHealth(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil || groupID <= 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	var body struct {
		ProbeMode string `json:"probe_mode,omitempty"`
	}
	_ = c.ShouldBindJSON(&body)
	probeMode, err := parseGroupHealthProbeMode(body.ProbeMode)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if _, runningErr := defaultGroupHealthRepo.GetRunningSnapshotByGroupID(c.Request.Context(), groupID); runningErr == nil {
		resp.Error(c, http.StatusConflict, helper.ErrGroupHealthAlreadyRunning.Error())
		return
	} else if !errors.Is(runningErr, gorm.ErrRecordNotFound) {
		resp.InternalError(c)
		return
	}
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if runErr := helper.RunGroupHealth(runCtx, groupID, probeMode); runErr != nil {
			log.Warnf("group health check failed (group=%d): %v", groupID, runErr)
		}
	}()
	resp.Success(c, gin.H{"accepted": true, "group_id": groupID})
}

func runAllGroupHealth(c *gin.Context) {
	var body struct {
		ProbeMode string `json:"probe_mode,omitempty"`
	}
	_ = c.ShouldBindJSON(&body)
	probeMode, err := parseGroupHealthProbeMode(body.ProbeMode)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		helper.RunAllGroupHealth(runCtx, 2, probeMode)
	}()
	resp.Success(c, gin.H{"accepted": true})
}

func getGroupHealthLatest(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("id"))
	if err != nil || groupID <= 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	view, err := defaultGroupHealthRepo.GetGroupHealthViewByID(c.Request.Context(), groupID)
	if err != nil {
		resp.Error(c, http.StatusNotFound, "group not found")
		return
	}
	resp.Success(c, view)
}

func listGroupHealthViews(c *gin.Context) {
	views, err := defaultGroupHealthRepo.ListGroupHealthViews(c.Request.Context())
	if err != nil {
		resp.InternalError(c)
		return
	}
	resp.Success(c, views)
}
