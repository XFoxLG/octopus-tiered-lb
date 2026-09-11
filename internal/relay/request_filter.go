package relay

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/transformer/model"
)

type requestFilterConfig struct {
	Enabled      bool
	Keywords     []string
	ErrorMessage string
}

func loadRequestFilterConfig() requestFilterConfig {
	enabled, _ := setting.GetBool(dbmodel.SettingKeyRequestFilterEnabled)
	filterConfig := requestFilterConfig{
		Enabled:      enabled,
		ErrorMessage: dbmodel.DefaultRequestFilterErrorMessage,
	}
	if !enabled {
		return filterConfig
	}

	keywordsJSON, _ := setting.GetString(dbmodel.SettingKeyRequestFilterKeywords)
	_ = jsonAPI.Unmarshal([]byte(keywordsJSON), &filterConfig.Keywords)
	if errorMessage, _ := setting.GetString(dbmodel.SettingKeyRequestFilterErrorMessage); strings.TrimSpace(errorMessage) != "" {
		filterConfig.ErrorMessage = errorMessage
	}
	return filterConfig
}

func extractLatestUserRequestText(request *model.InternalLLMRequest) string {
	if request == nil {
		return ""
	}
	for messageIndex := len(request.Messages) - 1; messageIndex >= 0; messageIndex-- {
		message := request.Messages[messageIndex]
		if message.Role != "user" {
			continue
		}

		// Do not let system prompts, old turns, tool results, or attachment URLs
		// turn a later, unrelated conversation request into a false positive.
		var text strings.Builder
		if message.Content.Content != nil {
			text.WriteString(*message.Content.Content)
		}
		for _, contentPart := range message.Content.MultipleContent {
			if contentPart.Type == "text" && contentPart.Text != nil {
				text.WriteString(*contentPart.Text)
			}
		}
		return text.String()
	}
	return ""
}

func shouldBlockRequest(request *model.InternalLLMRequest, filterConfig requestFilterConfig) bool {
	if !filterConfig.Enabled || len(filterConfig.Keywords) == 0 {
		return false
	}
	requestText := strings.ToLower(extractLatestUserRequestText(request))
	if requestText == "" {
		return false
	}
	for _, keyword := range filterConfig.Keywords {
		normalizedKeyword := strings.ToLower(strings.TrimSpace(keyword))
		if normalizedKeyword != "" && strings.Contains(requestText, normalizedKeyword) {
			return true
		}
	}
	return false
}

func rejectBlockedRequest(requestContext *gin.Context, request *model.InternalLLMRequest) bool {
	filterConfig := loadRequestFilterConfig()
	if !shouldBlockRequest(request, filterConfig) {
		return false
	}

	// Reject before sessions, caches, routing, or retries can send anything
	// upstream. Never mask a probe and forward it under different wording.
	requestContext.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error": gin.H{
			"message": filterConfig.ErrorMessage,
			"type":    "invalid_request_error",
			"code":    "input_keyword_blocked",
		},
	})
	return true
}
