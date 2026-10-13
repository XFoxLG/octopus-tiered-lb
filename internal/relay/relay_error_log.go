package relay

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/errorlog"
	"github.com/lingyuins/octopus/internal/utils/log"
)

const (
	relayErrorLogLevelError   = "error"
	relayErrorLogLevelWarning = "warning"
)

// relayDispatchDelayWarnThreshold is the "received -> first upstream attempt"
// budget. Above it, the request gets a management-visible warning instead of
// silently sitting in "已接收" with nothing in the logs.
const relayDispatchDelayWarnThreshold = 5 * time.Second

// writeRelayErrorLog records one management-visible error-log entry for a relay
// request. It never blocks the caller's request context: the write uses a
// detached context because the client may already be gone when we log.
func writeRelayErrorLog(c *gin.Context, level, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	entry := model.ErrorLog{
		Source:  "backend",
		Level:   level,
		Message: message,
		Version: conf.Version,
	}
	if c != nil {
		if c.Request != nil {
			entry.RequestMethod = c.Request.Method
			if c.Request.URL != nil {
				entry.RequestPath = c.Request.URL.Path
			}
			entry.UserAgent = c.Request.UserAgent()
		}
		entry.ClientIP = c.ClientIP()
	}
	if err := errorlog.Add(ctx, entry); err != nil {
		log.Warnf("failed to save relay error log entry: %v", err)
	}
}

// reportRelayDispatchDelay is a diagnostic probe, not a limiter: it measures
// the time between the trace middleware accepting the request and the relay
// beginning its first upstream dispatch. Slow dispatches (CPU starvation,
// database stalls, credential lookups) become visible in the error-log page.
func reportRelayDispatchDelay(c *gin.Context, requestModel string, endpointType string) {
	trace := relayRequestTraceFromContext(c)
	if trace == nil || trace.startedAt.IsZero() {
		return
	}
	delay := time.Since(trace.startedAt)
	if delay < relayDispatchDelayWarnThreshold {
		return
	}
	apiKeyID := 0
	if c != nil {
		apiKeyID = c.GetInt("api_key_id")
	}
	writeRelayErrorLog(c, relayErrorLogLevelWarning, fmt.Sprintf(
		"relay dispatch delayed %s before the first upstream attempt: endpoint=%s model=%s api_key_id=%d",
		delay.Round(time.Millisecond),
		endpointType,
		requestModel,
		apiKeyID,
	))
}
