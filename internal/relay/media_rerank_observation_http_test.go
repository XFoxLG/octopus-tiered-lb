package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
)

func TestRerankObservationHTTPPreservesLargeAndMalformedResponses(t *testing.T) {
	document := strings.Repeat(`safe text \\ escaped `, 120000)
	results := `"results":[{"index":0,"relevance_score":0.9,"document":{"text":"` + document + `"}}]`
	for index, body := range []string{`{"usage":{"total_tokens":1000},` + results + `}`, `{` + results + `,"meta":{"tokens":{"input_tokens":700,"total_tokens":1000}}}`, `{` + results + `,"usage":{"total_tokens":1000`, `{"results":[],"usage":{"input_tokens":-5}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			for p := 0; p < len(body); p += 4093 {
				end := min(p+4093, len(body))
				_, _ = io.WriteString(w, body[p:end])
				w.(http.Flusher).Flush()
			}
		}))
		req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
		req = transmodel.StartNonChatTiming(req)
		response, err := server.Client().Do(req)
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		transmodel.AttachNonChatTiming(response, req)
		rr := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rr)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)
		status, err := handleRerankResponse(c, response)
		_ = response.Body.Close()
		server.Close()
		if err != nil || status != 200 || rr.Body.String() != body {
			t.Fatalf("observer changed response: status=%d err=%v len=%d/%d", status, err, rr.Body.Len(), len(body))
		}
		o := mediaRerankUsageFromContext(c).observation
		if o == nil || o.Summary.FirstByteMS == nil || o.Summary.ResponseMS == nil {
			t.Fatal("missing observation timing")
		}
		if index < 2 {
			if o.Summary.TotalTokens == nil || *o.Summary.TotalTokens != 1000 || o.Summary.Counts.RerankResults == nil || *o.Summary.Counts.RerankResults != 1 {
				t.Fatalf("large response lost usage: %+v", o)
			}
		} else if o.Summary.TotalTokens != nil {
			t.Fatalf("invalid response usage accepted: %+v", o)
		}
	}
}

type nonChatCancelBody struct {
	entered chan struct{}
	closed  chan struct{}
}

func (b *nonChatCancelBody) Read([]byte) (int, error) {
	close(b.entered)
	<-b.closed
	return 0, context.Canceled
}
func (b *nonChatCancelBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}
func TestRerankObservationCancellationClosesUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil).WithContext(ctx)
	b := &nonChatCancelBody{make(chan struct{}), make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := handleRerankResponse(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: b})
		done <- err
	}()
	<-b.entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled response marked success")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop upstream read")
	}
	o := mediaRerankUsageFromContext(c).observation
	if o.ResponseState != "interrupted" || o.Summary.TotalTokens != nil {
		t.Fatalf("cancelled observation used as complete: %+v", o)
	}
}

type nonChatFailWriter struct{ gin.ResponseWriter }

func (w nonChatFailWriter) Write([]byte) (int, error) { return 0, errors.New("fixture write failure") }
func TestRerankObservationWriteFailureIsNotSuccess(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)
	c.Writer = nonChatFailWriter{c.Writer}
	_, err := handleRerankResponse(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"results":[],"usage":{"total_tokens":4}}`))})
	if err == nil || mediaRerankUsageFromContext(c).observation.ResponseState != "interrupted" {
		t.Fatal("writer failure marked complete")
	}
}
