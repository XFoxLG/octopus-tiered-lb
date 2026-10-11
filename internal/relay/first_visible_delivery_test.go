package relay

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type milestoneTestWriter struct {
	gin.ResponseWriter
	metrics *RelayMetrics
	test    *testing.T
	fail    bool
	flushed bool
}

func (w *milestoneTestWriter) Write(data []byte) (int, error) {
	if !w.flushed && !w.metrics.FirstTokenTime.IsZero() {
		w.test.Error("client milestone recorded before writing")
	}
	if w.fail {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(data)
}
func (w *milestoneTestWriter) Flush() {
	if !w.flushed && !w.metrics.FirstTokenTime.IsZero() {
		w.test.Error("client milestone recorded before flush")
	}
	w.ResponseWriter.Flush()
	w.flushed = true
}
func TestFirstVisibleMilestonesFollowClientDelivery(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "delivered", true: "write_failed"}[fail], func(t *testing.T) {
			text := "visible answer"
			response := &transmodel.InternalLLMResponse{Object: "chat.completion.chunk", Choices: []transmodel.Choice{{Delta: &transmodel.Message{Content: transmodel.MessageContent{Content: &text}}}}}
			attempt := newTerminationFidelityRelayAttempt(httptest.NewRecorder(), response, []byte("data: visible answer\n\n"))
			attempt.metrics.StartTime = time.Now()
			writer := &milestoneTestWriter{ResponseWriter: attempt.c.Writer, metrics: attempt.metrics, test: t, fail: fail}
			attempt.c.Writer = writer
			err := attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: upstream\n\n"))})
			if attempt.metrics.UpstreamFirstVisibleTime.IsZero() {
				t.Fatal("missing upstream milestone")
			}
			if fail {
				if !errors.Is(err, errClientDisconnected) {
					t.Fatalf("write failure: %v", err)
				}
				if !attempt.metrics.FirstTokenTime.IsZero() {
					t.Fatal("failed delivery recorded client milestone")
				}
			} else {
				if !writer.flushed || attempt.metrics.FirstTokenTime.IsZero() {
					t.Fatal("successful flush missing client milestone")
				}
				if attempt.metrics.FirstTokenTime.Before(attempt.metrics.UpstreamFirstVisibleTime) {
					t.Fatal("client milestone precedes upstream arrival")
				}
			}
		})
	}
}
func TestMetadataDoesNotRecordFirstVisibleMilestones(t *testing.T) {
	attempt := newTerminationFidelityRelayAttempt(httptest.NewRecorder(), &transmodel.InternalLLMResponse{Object: "chat.completion.chunk"}, []byte("data: metadata\n\n"))
	_ = attempt.handleStreamResponse(context.Background(), &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: upstream\n\n"))})
	if !attempt.metrics.FirstTokenTime.IsZero() || !attempt.metrics.UpstreamFirstVisibleTime.IsZero() {
		t.Fatal("metadata counted as visible content")
	}
}
