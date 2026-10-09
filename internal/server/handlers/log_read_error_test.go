package handlers

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogDatabaseTimeoutIsNotMissingRecord(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	logReadError(ginContext, context.DeadlineExceeded)
	if recorder.Code != http.StatusGatewayTimeout || !strings.Contains(recorder.Body.String(), "log.detailDatabaseTimeout") {
		t.Fatalf("incorrect timeout: %d %s", recorder.Code, recorder.Body.String())
	}
}
