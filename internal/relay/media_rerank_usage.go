package relay

import (
	"bytes"
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/transformer/model"
	"io"
	"net/http"
)

const mediaRerankUsageKey = "relay_rerank_usage"

type mediaRerankUsage struct{ observation *model.NonChatObservation }

func mediaRerankUsageFromContext(c *gin.Context) mediaRerankUsage {
	v, _ := c.Get(mediaRerankUsageKey)
	u, _ := v.(mediaRerankUsage)
	return u
}

type rerankForwardReader struct {
	body   io.Reader
	writer io.Writer
	err    error
}

func (r *rerankForwardReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	n, e := r.body.Read(p)
	if n > 0 {
		written, we := r.writer.Write(p[:n])
		if we == nil && written != n {
			we = io.ErrShortWrite
		}
		if we != nil {
			r.err = we
			return 0, we
		}
	}
	if e != nil && e != io.EOF {
		r.err = e
	}
	return n, e
}
func handleRerankResponse(c *gin.Context, response *http.Response) (int, error) {
	if ct := response.Header.Get("Content-Type"); ct != "" {
		c.Header("Content-Type", ct)
	}
	stop := context.AfterFunc(c.Request.Context(), func() { _ = response.Body.Close() })
	defer stop()
	r := &rerankForwardReader{body: response.Body, writer: c.Writer}
	o := model.ObserveNonChatResponse(r, "rerank")
	// Observation failures do not alter the original response bytes.
	if r.err == nil {
		_, _ = io.Copy(io.Discard, r)
	}
	if r.err != nil {
		o.ResponseState = "interrupted"
		o.Summary.Completeness = "invalid"
		o.Summary.NonChatCounters = model.NonChatCounters{}
		o.Summary.Counts = model.NonChatCounts{}
		o.Issues = append(o.Issues, model.NonChatUsageIssue{Path: "$", Reason: "response_interrupted"})
	}
	model.ApplyNonChatTiming(o, response)
	c.Set(mediaRerankUsageKey, mediaRerankUsage{observation: o})
	if r.err != nil {
		return 0, writeErrorOutcome(c, fmt.Errorf("failed to stream response: %w", r.err))
	}
	return response.StatusCode, nil
}
func parseMediaRerankUsage(body []byte) mediaRerankUsage {
	return mediaRerankUsage{observation: model.ObserveNonChatResponse(bytes.NewReader(body), "rerank")}
}
