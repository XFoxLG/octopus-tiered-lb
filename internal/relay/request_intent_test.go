package relay

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/transformer/inbound"
)

func TestParseRequestRejectsContradictoryThinkingIntent(t *testing.T) {
	for _, body := range []string{
		`{"model":"m","messages":[{"role":"user","content":"hi"}],"enable_thinking":false,"reasoning_effort":"high"}`,
		`{"model":"m","messages":[{"role":"user","content":"hi"}],"enable_thinking":true,"reasoning_effort":"none"}`,
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
		if request, _, err := parseRequest(inbound.InboundTypeOpenAIChat, c); err == nil || request != nil || w.Code != http.StatusBadRequest {
			t.Fatalf("conflicting request reached relay: request=%v error=%v status=%d", request, err, w.Code)
		}
	}
}
